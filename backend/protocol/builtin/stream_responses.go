package builtin

import (
	"crypto/sha256"
	"fmt"
	"strconv"

	p "github.com/elysia-api/backend/protocol"
)

func (stream *streamModule) decodeResponsesFrame(fields p.Object, options p.EvaluationContext) ([]p.Event, error) {
	if value := fields["sequence_number"]; !value.IsZero() {
		var sequence int64
		if err := value.Decode(&sequence); err != nil || value.IsNull() || sequence < 0 || (stream.hasSequence && sequence <= stream.sequence) {
			return nil, unsupported("/sequence_number", "Responses event sequence must increase; repeated or reordered frames cannot be replayed")
		}
		stream.sequence, stream.hasSequence = sequence, true
	}
	kind, err := stringValue(fields["type"])
	if err != nil {
		return nil, err
	}
	outputIndex, err := frameIndex(fields["output_index"])
	if err != nil {
		return nil, err
	}
	contentIndex, err := frameIndex(fields["content_index"])
	if err != nil {
		return nil, err
	}
	if err := stream.checkResponsesItemID(fields, outputIndex); err != nil {
		return nil, err
	}
	key := "output:" + strconv.Itoa(outputIndex)
	contentKey := key + ":" + strconv.Itoa(contentIndex)
	if item := stream.items[contentKey]; item != nil && item.partClosed {
		switch kind {
		case "response.output_text.delta", "response.refusal.delta", "response.output_text.done", "response.refusal.done", "response.content_part.done", "response.output_text.annotation.added":
			return nil, unsupported("/content_index", "content arrived after message part completion")
		}
	}
	if item := stream.items[key]; item != nil && item.node.Kind == p.ReasoningNode {
		switch kind {
		case "response.content_part.added", "response.reasoning_text.delta", "response.reasoning_text.done", "response.content_part.done":
			return stream.decodeVisibleReasoningFrame(kind, key, fields)
		}
	}
	switch kind {
	case "response.created":
		response, err := stream.module.decodeResponse(fields["response"], options)
		if err != nil {
			return nil, err
		}
		stream.attributes = response.Attributes
		events := stream.beginResponse(response.ID, response.Model)
		if len(events) > 0 {
			events[0].Response.Metadata = response.Metadata
			events[0].Response.Usage = response.Usage
		}
		return events, nil
	case "response.in_progress":
		response, err := fields["response"].ReadObject()
		if err != nil {
			return nil, err
		}
		return stream.usageEvent(response["usage"])
	case "response.output_item.added":
		item, err := fields["item"].ReadObject()
		if err != nil {
			return nil, err
		}
		itemKind, err := stringValue(item["type"])
		if err != nil {
			return nil, err
		}
		if itemKind == "reasoning" {
			node, err := stream.module.decodeReasoningItem(fields["item"], "/item", p.DecodeResponse, options)
			if err != nil {
				return nil, err
			}
			event, err := stream.itemEvent(p.ItemStarted, key, &node, p.Value{})
			return []p.Event{event}, err
		}
		if itemKind == "message" {
			if stream.responseMessages[outputIndex] != nil {
				return nil, unsupported("/output_index", "message started twice")
			}
			_, err := stream.updateMessageMetadata(outputIndex, item)
			return nil, err
		}
		if itemKind != "function_call" && itemKind != "custom_tool_call" {
			return []p.Event{{Type: p.NativeEvent}}, nil
		}
		node := p.Node{Kind: p.ToolCallNode, ID: item["id"], CallID: item["call_id"], Name: item["name"], Status: item["status"], Input: &p.ToolInput{Kind: p.JSONInput}}
		if itemKind == "custom_tool_call" {
			node.Input.Kind = p.TextInput
		}
		event, err := stream.itemEvent(p.ItemStarted, key, &node, p.Value{})
		return []p.Event{event}, err
	case "response.reasoning_summary_part.added", "response.reasoning_summary_text.delta", "response.reasoning_summary_text.done", "response.reasoning_summary_part.done":
		return stream.decodeSummaryFrame(kind, key, fields)
	case "response.reasoning_text.delta", "response.reasoning_text.done":
		return nil, unsupported("/item_id", "reasoning text has no preceding visible reasoning item")
	case "response.content_part.added":
		message, err := stream.responseMessage(outputIndex)
		if err != nil {
			return nil, err
		}
		if message.finished || contentIndex != len(message.parts) {
			return nil, unsupported("/content_index", "message part must append to its open message")
		}
		part, err := fields["part"].ReadObject()
		if err != nil {
			return nil, err
		}
		node := p.Node{Kind: p.TextNode, Payload: part["text"]}
		node.Metadata, err = stream.module.extractMetadata(part, "content", "/part")
		if err != nil {
			return nil, err
		}
		if string(part["type"].Bytes()) == `"refusal"` {
			node.Kind, node.Payload = p.RefusalNode, part["refusal"]
		}
		node.Metadata = p.MergeNodeMetadata(node.Metadata, message.metadata, false)
		event, err := stream.itemEvent(p.ItemStarted, contentKey, &node, p.Value{})
		if err == nil {
			message.parts = append(message.parts, contentKey)
		}
		return []p.Event{event}, err
	case "response.output_text.delta", "response.refusal.delta":
		if item := stream.items[contentKey]; item != nil && item.textClosed {
			return nil, unsupported("/content_index", "text arrived after its completion")
		}
		event, err := stream.itemEvent(p.ItemDelta, contentKey, nil, fields["delta"])
		if !fields["logprobs"].IsZero() {
			event.Metadata = []p.ResponseMetadata{{Name: "logprobs", Location: "content", Codec: Responses, SourceCodec: Responses, Path: "/logprobs", Value: fields["logprobs"]}}
		}
		return []p.Event{event}, err
	case "response.output_text.annotation.added":
		item := stream.items[contentKey]
		if item == nil {
			return nil, unsupported("/item_id", "citation has no text item")
		}
		var entries []p.Value
		for _, m := range item.node.Metadata {
			if m.Name == "annotations" {
				_ = m.Value.Decode(&entries)
			}
		}
		var index int
		if fields["annotation_index"].Decode(&index) != nil || index != len(entries) {
			return nil, unsupported("/annotation_index", "citation index must append to its text item")
		}
		entries = append(entries, fields["annotation"])
		node := item.node
		m := p.ResponseMetadata{Name: "annotations", Location: "content", Codec: Responses, SourceCodec: Responses, Path: "/annotation", Value: array(entries)}
		if err := p.ValidateMetadataValue(Responses, "content", "annotations", m.Value, m.Path); err != nil {
			return nil, err
		}
		node.Metadata = p.MergeNodeMetadata(node.Metadata, []p.ResponseMetadata{m}, false)
		event, err := stream.itemEvent(p.ItemSnapshot, contentKey, &node, p.Value{})
		return []p.Event{event}, err
	case "response.function_call_arguments.delta", "response.custom_tool_call_input.delta":
		event, err := stream.itemEvent(p.ItemDelta, key, nil, fields["delta"])
		return []p.Event{event}, err
	case "response.function_call_arguments.done", "response.custom_tool_call_input.done":
		item := stream.items[key]
		if item == nil {
			return nil, fmt.Errorf("completed input has no tool item")
		}
		node := item.node
		value := fields["input"]
		if node.Input.Kind == p.JSONInput {
			value, err = readJSONArguments(fields["arguments"])
			if err != nil {
				return nil, err
			}
		}
		node.Input = &p.ToolInput{Kind: node.Input.Kind, Value: value}
		event, err := stream.itemEvent(p.ItemSnapshot, key, &node, p.Value{})
		return []p.Event{event}, err
	case "response.output_text.done", "response.refusal.done":
		item := stream.items[contentKey]
		if item == nil {
			return nil, fmt.Errorf("completed text has no content item")
		}
		node := item.node
		node.Payload = fields["text"]
		if kind == "response.refusal.done" {
			node.Payload = fields["refusal"]
		}
		text, err := responsePartText(node)
		if err != nil {
			return nil, err
		}
		if item.textClosed {
			return nil, unsupported("/content_index", "text completed twice")
		}
		item.textClosed, item.closedTextDigest = true, sha256.Sum256([]byte(text))
		event, err := stream.itemEvent(p.ItemSnapshot, contentKey, &node, p.Value{})
		return []p.Event{event}, err
	case "response.content_part.done":
		current := stream.items[contentKey]
		if current == nil {
			return nil, fmt.Errorf("completed part has no start")
		}
		node := current.node
		if !fields["part"].IsZero() {
			part, e := fields["part"].ReadObject()
			if e != nil {
				return nil, e
			}
			node.Metadata, e = stream.module.extractMetadata(part, "content", "/part")
			if e != nil {
				return nil, e
			}
			node.Payload = part["text"]
			if node.Kind == p.RefusalNode {
				node.Payload = part["refusal"]
			}
		}
		text, err := responsePartText(node)
		if err != nil {
			return nil, err
		}
		digest := sha256.Sum256([]byte(text))
		if current.textClosed && current.closedTextDigest != digest {
			return nil, unsupported("/part", "completed message text changed")
		}
		current.partClosed, current.closedTextDigest = true, digest
		node.Metadata = p.MergeNodeMetadata(current.node.Metadata, node.Metadata, false)
		event, err := stream.itemEvent(p.ItemSnapshot, contentKey, &node, p.Value{})
		return []p.Event{event}, err
	case "response.output_item.done":
		item, err := fields["item"].ReadObject()
		if err != nil {
			return nil, err
		}
		itemKind, err := stringValue(item["type"])
		if err != nil {
			return nil, err
		}
		if itemKind == "message" {
			if message := stream.responseMessages[outputIndex]; message != nil && message.finished {
				return nil, unsupported("/output_index", "message completed twice")
			}
			return stream.finishResponseMessage(outputIndex, item, options)
		}
		if itemKind != "function_call" && itemKind != "custom_tool_call" && itemKind != "reasoning" {
			return []p.Event{{Type: p.NativeEvent}}, nil
		}
		node, err := stream.module.decodeResponseItem(fields["item"], "/item", p.DecodeResponse, options, &historyState{calls: map[string][]p.Value{}})
		if itemKind == "reasoning" {
			node, err = stream.module.decodeReasoningItem(fields["item"], "/item", p.DecodeResponse, options)
			if err == nil {
				err = stream.checkReasoningCompletion(key, node)
			}
		}
		if err != nil {
			return nil, err
		}
		event, err := stream.itemEvent(p.ItemFinished, key, &node, p.Value{})
		return []p.Event{event}, err
	case "response.completed", "response.incomplete":
		response, err := stream.module.decodeResponse(fields["response"], options)
		if err != nil {
			return nil, err
		}
		stream.isFinished = true
		var events []p.Event
		terminal, _ := fields["response"].ReadObject()
		output, _ := readArray(terminal["output"])
		for i := range response.Content {
			node := &response.Content[i]
			if node.Kind != p.MessageNode {
				continue
			}
			item, err := output[i].ReadObject()
			if err != nil {
				return nil, err
			}
			if err := stream.checkResponsesItemID(p.Object{"item": output[i]}, i); err != nil {
				return nil, err
			}
			batch, err := stream.finishResponseMessage(i, item, options)
			if err != nil {
				return nil, err
			}
			node.Metadata = p.MergeNodeMetadata(node.Metadata, stream.responseMessages[i].metadata, false)
			events = append(events, batch...)
		}
		return append(events, p.Event{Type: p.ResponseFinished, Response: response, Usage: response.Usage}), nil
	case "response.failed":
		response, err := fields["response"].ReadObject()
		if err != nil {
			return nil, err
		}
		return stream.decodeFailureEvent(response["error"], options)
	case "error":
		payload := copyFields(fields)
		delete(payload, "type")
		return stream.decodeFailureEvent(object(payload), options)
	default:
		return []p.Event{{Type: p.NativeEvent}}, nil
	}
}

func (stream *streamModule) checkResponsesItemID(fields p.Object, index int) error {
	id := fields["item_id"]
	if value := fields["item"]; !value.IsZero() {
		item, err := value.ReadObject()
		if err != nil {
			return err
		}
		if !id.IsZero() && !item["id"].IsZero() && id != item["id"] {
			return unsupported("/item_id", "frame and item IDs disagree")
		}
		if id.IsZero() {
			id = item["id"]
		}
	}
	if id.IsZero() {
		return nil
	}
	if value, err := stringValue(id); err != nil || value == "" {
		return unsupported("/item_id", "item ID must be a nonempty string")
	}
	if previous := stream.responseItemIDs[index]; !previous.IsZero() {
		if previous != id {
			return unsupported("/item_id", "assigned output item ID changed")
		}
		return nil
	}
	for _, previous := range stream.responseItemIDs {
		if previous == id {
			return unsupported("/output_index", "item ID was reused at another output index")
		}
	}
	if len(stream.responseItemIDs) >= stream.limits.StateItems || stream.buffered+len(id.Bytes()) > stream.limits.BufferBytes {
		return unsupported("/item_id", "output identity state exceeds the stream limit")
	}
	if stream.responseItemIDs == nil {
		stream.responseItemIDs = map[int]p.Value{}
	}
	stream.responseItemIDs[index] = id
	stream.buffered += len(id.Bytes())
	return nil
}

func (stream *streamModule) decodeGeminiFrame(fields p.Object, options p.EvaluationContext) ([]p.Event, error) {
	if failure := fields["error"]; !failure.IsZero() {
		return stream.decodeFailureEvent(failure, options)
	}
	candidates, err := readArray(fields["candidates"])
	if err != nil {
		return nil, err
	}
	var events []p.Event
	if len(candidates) > 0 {
		events = stream.beginResponse(fields["responseId"], fields["modelVersion"])
	}
	for _, value := range candidates {
		candidate, err := value.ReadObject()
		if err != nil {
			return nil, err
		}
		index, err := frameIndex(candidate["index"])
		if err != nil {
			return nil, err
		}
		if index != 0 {
			return nil, unsupported("/candidates/index", "multi-candidate streams require an explicit choice lifecycle")
		}
		content, err := nestedObject(candidate, "content")
		if err != nil {
			return nil, err
		}
		parts, err := readArray(content["parts"])
		if err != nil {
			return nil, err
		}
		for _, part := range parts {
			partFields, err := part.ReadObject()
			if err != nil {
				return nil, err
			}
			// A terminal empty text delta is a framing placeholder, not a new
			// assistant item. Creating one breaks the following tool-result turn.
			// Signed or annotated parts, and empty parts before terminal, retain
			// their identity because a later signature may belong to them.
			if len(partFields) == 1 && partFields["text"] == p.StringValue("") && !candidate["finishReason"].IsZero() && !candidate["finishReason"].IsNull() {
				continue
			}
			// A signature-only part applies only to the immediately preceding
			// open part. Never associate by tool name or an arbitrary index.
			if len(partFields) == 1 && !partFields["thoughtSignature"].IsZero() {
				item := stream.items[stream.geminiLastKey]
				if item == nil || item.isFinished {
					return nil, unsupported("/parts/thoughtSignature", "signature has no open part association")
				}
				synthetic := p.Object{"text": p.StringValue(""), "thoughtSignature": partFields["thoughtSignature"]}
				signed, err := stream.module.decodeBlock(object(synthetic), "/parts", p.DecodeResponse, options, &historyState{})
				if err != nil {
					return nil, err
				}
				node := item.node
				node.Resources = signed.Resources
				event, err := stream.itemEvent(p.ItemSnapshot, stream.geminiLastKey, &node, p.Value{})
				if err != nil {
					return nil, err
				}
				events = append(events, event)
				continue
			}
			history := &historyState{calls: map[string][]p.Value{}, next: stream.nextTool}
			node, err := stream.module.decodeBlock(part, "/parts", p.DecodeResponse, options, history)
			if err != nil {
				return nil, err
			}
			stream.nextTool = history.next
			switch node.Kind {
			case p.TextNode, p.ReasoningNode:
				key := stream.geminiLastKey
				previous := stream.items[key]
				if previous == nil || previous.node.Kind != node.Kind || p.HasSignature(previous.node) {
					key = string(node.Kind)
					if _, exists := stream.items[key]; exists {
						key = fmt.Sprintf("%s:%d", node.Kind, stream.geminiPartSerial)
					}
					stream.geminiPartSerial++
				}
				stream.geminiLastKey = key
				batch, err := stream.textEvents(key, node.Kind, node.Payload)
				if err != nil {
					return nil, err
				}
				events = append(events, batch...)
				if len(node.Resources) > 0 {
					node.Payload = p.Value{}
					event, err := stream.itemEvent(p.ItemSnapshot, key, &node, p.Value{})
					if err != nil {
						return nil, err
					}
					events = append(events, event)
				}
			case p.ToolCallNode:
				id, err := stringValue(node.CallID)
				if err != nil {
					return nil, err
				}
				key := "tool:" + id
				stream.geminiLastKey = key
				kind := p.ItemStarted
				if previous := stream.items[key]; previous != nil {
					kind = p.ItemSnapshot
				}
				event, err := stream.itemEvent(kind, key, &node, p.Value{})
				if err != nil {
					return nil, err
				}
				events = append(events, event)
			default:
				return nil, unsupported("/parts", "Gemini stream part requires a supported typed event mapping")
			}
		}
		if finish := candidate["finishReason"]; !finish.IsZero() && !finish.IsNull() {
			for _, key := range stream.order {
				item := stream.items[key]
				if item.node.Kind == p.ToolCallNode && !item.isFinished {
					event, err := stream.itemEvent(p.ItemFinished, key, nil, p.Value{})
					if err != nil {
						return nil, err
					}
					events = append(events, event)
				}
			}
			stream.finish, err = decodeFinishReason(Gemini, finish)
			if err != nil {
				return nil, err
			}
			if string(stream.finish.Bytes()) == `"stop"` {
				for _, item := range stream.items {
					if item.node.Kind == p.ToolCallNode {
						stream.finish = p.StringValue("tool_calls")
						break
					}
				}
			}
			stream.isFinished = true
			events = append(events, p.Event{Type: p.ResponseFinished, Response: &p.Response{SchemaVersion: p.SemanticSchemaVersion, ID: stream.id, Model: stream.model, Status: p.StringValue("completed"), Attributes: p.Object{"finishReason": stream.finish}}})
		}
	}
	usage, err := stream.usageEvent(fields["usageMetadata"])
	if err != nil {
		return nil, err
	}
	return append(events, usage...), nil
}
