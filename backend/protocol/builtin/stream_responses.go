package builtin

import (
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
	switch kind {
	case "response.created":
		response, err := stream.module.decodeResponse(fields["response"], options)
		if err != nil {
			return nil, err
		}
		stream.attributes = response.Attributes
		return stream.begin(response.ID, response.Model), nil
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
			node, err := stream.module.decodeResponseItem(fields["item"], "/item", p.DecodeResponse, options, &historyState{})
			if err != nil {
				return nil, err
			}
			event, err := stream.itemEvent(p.ItemStarted, key, &node, p.Value{})
			return []p.Event{event}, err
		}
		if itemKind == "message" {
			return nil, nil
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
	case "response.content_part.added":
		part, err := fields["part"].ReadObject()
		if err != nil {
			return nil, err
		}
		node := p.Node{Kind: p.TextNode, Payload: part["text"]}
		if string(part["type"].Bytes()) == `"refusal"` {
			node.Kind, node.Payload = p.RefusalNode, part["refusal"]
		}
		event, err := stream.itemEvent(p.ItemStarted, contentKey, &node, p.Value{})
		return []p.Event{event}, err
	case "response.output_text.delta", "response.refusal.delta":
		event, err := stream.itemEvent(p.ItemDelta, contentKey, nil, fields["delta"])
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
		event, err := stream.itemEvent(p.ItemSnapshot, contentKey, &node, p.Value{})
		return []p.Event{event}, err
	case "response.content_part.done":
		event, err := stream.itemEvent(p.ItemFinished, contentKey, nil, p.Value{})
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
			return nil, nil
		}
		if itemKind != "function_call" && itemKind != "custom_tool_call" && itemKind != "reasoning" {
			return []p.Event{{Type: p.NativeEvent}}, nil
		}
		node, err := stream.module.decodeResponseItem(fields["item"], "/item", p.DecodeResponse, options, &historyState{calls: map[string][]p.Value{}})
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
		return []p.Event{{Type: p.ResponseFinished, Response: response, Usage: response.Usage}}, nil
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
		events = stream.begin(fields["responseId"], fields["modelVersion"])
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
			history := &historyState{calls: map[string][]p.Value{}, next: stream.nextTool}
			node, err := stream.module.decodeBlock(part, "/parts", p.DecodeResponse, options, history)
			if err != nil {
				return nil, err
			}
			stream.nextTool = history.next
			switch node.Kind {
			case p.TextNode, p.ReasoningNode:
				key := string(node.Kind)
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
				event, err := stream.itemEvent(p.ItemStarted, key, &node, p.Value{})
				if err != nil {
					return nil, err
				}
				events = append(events, event)
				event, err = stream.itemEvent(p.ItemFinished, key, nil, p.Value{})
				if err != nil {
					return nil, err
				}
				events = append(events, event)
			default:
				return nil, unsupported("/parts", "Gemini stream part requires a supported typed event mapping")
			}
		}
		if finish := candidate["finishReason"]; !finish.IsZero() && !finish.IsNull() {
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
			events = append(events, p.Event{Type: p.ResponseFinished, Response: &p.Response{SchemaVersion: 1, ID: stream.id, Model: stream.model, Status: p.StringValue("completed"), Attributes: p.Object{"finishReason": stream.finish}}})
		}
	}
	usage, err := stream.usageEvent(fields["usageMetadata"])
	if err != nil {
		return nil, err
	}
	return append(events, usage...), nil
}
