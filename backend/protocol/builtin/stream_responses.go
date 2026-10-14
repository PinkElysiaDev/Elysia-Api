package builtin

import (
	"crypto/sha256"
	"fmt"
	"strconv"

	p "github.com/elysia-api/backend/protocol"
)

func (stream *streamModule) decodeResponsesFrame(fields p.Object, options p.EvaluationContext) ([]p.Event, error) {
	if err := stream.observeResponsesSequence(fields["sequence_number"]); err != nil {
		return nil, err
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
	case "keepalive":
		// A declared payload-free transport heartbeat has no generation
		// event. Sequence validation above still applies; extension capture
		// below rejects any additional data instead of silently discarding it.
		return nil, nil
	case "response.created":
		return stream.startResponsesResponse(fields, options)
	case "response.in_progress":
		return stream.updateResponsesProgress(fields, options)
	case "response.output_item.added":
		return stream.startResponsesItem(fields, options, outputIndex, key)
	case "response.reasoning_summary_part.added", "response.reasoning_summary_text.delta", "response.reasoning_summary_text.done", "response.reasoning_summary_part.done":
		return stream.decodeSummaryFrame(kind, key, fields)
	case "response.reasoning_text.delta", "response.reasoning_text.done":
		return nil, unsupported("/item_id", "reasoning text has no preceding visible reasoning item")
	case "response.content_part.added":
		return stream.startResponsesContent(fields, outputIndex, contentIndex, key, contentKey)
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
		return stream.appendResponsesAnnotation(fields, contentKey)
	case "response.function_call_arguments.delta", "response.custom_tool_call_input.delta":
		event, err := stream.itemEvent(p.ItemDelta, key, nil, fields["delta"])
		return []p.Event{event}, err
	case "response.function_call_arguments.done", "response.custom_tool_call_input.done":
		return stream.finishResponsesToolInput(fields, key)
	case "response.output_text.done", "response.refusal.done":
		return stream.finishResponsesText(fields, kind, contentKey)
	case "response.content_part.done":
		return stream.finishResponsesContent(fields, contentKey)
	case "response.output_item.done":
		return stream.finishResponsesItem(fields, options, outputIndex, key)
	case "response.completed", "response.incomplete":
		return stream.finishResponsesResponse(fields, options)
	case "response.failed":
		response, err := fields["response"].ReadObject()
		if err != nil {
			return nil, err
		}
		return stream.decodeFailureEvent(response["error"], options)
	case "error":
		payload, err := responsesFailurePayload(fields)
		if err != nil {
			return nil, err
		}
		return stream.decodeFailureEvent(payload, options)
	default:
		if err := stream.rejectUnknownFailedEvent(fields, options); err != nil {
			return nil, err
		}
		return []p.Event{{Type: p.NativeEvent}}, nil
	}
}

func (stream *streamModule) startResponsesResponse(fields p.Object, options p.EvaluationContext) ([]p.Event, error) {
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
}

func (stream *streamModule) updateResponsesProgress(fields p.Object, options p.EvaluationContext) ([]p.Event, error) {
	if !fields["safety_buffering"].IsZero() {
		response, err := stream.module.decodeResponse(fields["response"], options)
		if err != nil {
			return nil, err
		}
		return []p.Event{{Type: p.MetadataUpdated, Response: response, Usage: response.Usage}}, nil
	}
	response, err := fields["response"].ReadObject()
	if err != nil {
		return nil, err
	}
	if !response["output_text"].IsZero() {
		if _, err := stream.module.decodeResponse(fields["response"], options); err != nil {
			return nil, err
		}
	}
	usage, err := stream.decodeUsageUpdate(response["usage"])
	if err != nil {
		return nil, err
	}
	usage, err = decodeResponsesToolUsage(response, usage)
	if err != nil || usage == nil {
		return nil, err
	}
	return []p.Event{{Type: p.UsageUpdated, Usage: usage}}, nil
}

func (stream *streamModule) finishResponsesToolInput(fields p.Object, key string) ([]p.Event, error) {
	var err error
	item := stream.items[key]
	if item == nil || item.node.Kind != p.ToolCallNode || item.node.Input == nil {
		return nil, unsupported("/item_id", "completed input requires a preceding tool call item")
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
}

func (stream *streamModule) finishResponsesText(fields p.Object, kind, contentKey string) ([]p.Event, error) {
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

func (stream *streamModule) startResponsesItem(fields p.Object, options p.EvaluationContext, outputIndex int, key string) ([]p.Event, error) {
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
		m, err := stream.updateMessageMetadata(outputIndex, item)
		if err != nil {
			return nil, err
		}
		nodes, err := stream.module.decodeMessages(array([]p.Value{fields["item"]}), "/output", p.DecodeResponse, options, &historyState{})
		if err != nil {
			return nil, err
		}
		node := nodes[0]
		node.Native.Source.Path = fmt.Sprintf("/output/%d", outputIndex)
		if len(node.Children) != 0 {
			return nil, unsupported("/item/content", "message start must precede content parts")
		}
		node.Children, node.Metadata = nil, m.metadata
		event, err := stream.itemEvent(p.ItemStarted, key, &node, p.Value{})
		return []p.Event{event}, err
	}
	if itemKind != "function_call" && itemKind != "custom_tool_call" {
		return []p.Event{{Type: p.NativeEvent}}, nil
	}
	node := p.Node{Kind: p.ToolCallNode, ID: item["id"], CallID: item["call_id"], Name: item["name"], Status: item["status"], Input: &p.ToolInput{Kind: p.JSONInput}}
	node.Metadata, err = stream.module.extractMetadata(item, "item", "/item")
	if err != nil {
		return nil, err
	}
	if itemKind == "custom_tool_call" {
		node.Input.Kind = p.TextInput
	}
	event, err := stream.itemEvent(p.ItemStarted, key, &node, p.Value{})
	return []p.Event{event}, err
}

func (stream *streamModule) startResponsesContent(fields p.Object, outputIndex, contentIndex int, key, contentKey string) ([]p.Event, error) {
	if parent := stream.items[key]; parent == nil || parent.node.Kind != p.MessageNode || parent.isFinished {
		return nil, unsupported("/output_index", "content part requires an open message")
	}
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
	if part["type"].IsString("refusal") {
		node.Kind, node.Payload = p.RefusalNode, part["refusal"]
	}
	event, err := stream.itemEvent(p.ItemStarted, contentKey, &node, p.Value{})
	if err == nil {
		stream.items[contentKey].parent = key
		event.ParentID = p.StringValue(key)
		message.parts = append(message.parts, contentKey)
	}
	return []p.Event{event}, err
}

func (stream *streamModule) appendResponsesAnnotation(fields p.Object, contentKey string) ([]p.Event, error) {
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
}

func (stream *streamModule) finishResponsesContent(fields p.Object, contentKey string) ([]p.Event, error) {
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
	event, err := stream.itemEvent(p.ItemFinished, contentKey, &node, p.Value{})
	return []p.Event{event}, err
}

func (stream *streamModule) finishResponsesItem(fields p.Object, options p.EvaluationContext, outputIndex int, key string) ([]p.Event, error) {
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
}

func (stream *streamModule) finishResponsesResponse(fields p.Object, options p.EvaluationContext) ([]p.Event, error) {
	response, err := stream.module.decodeResponse(fields["response"], options)
	if err != nil {
		return nil, err
	}
	stream.isFinished = true
	var events []p.Event
	terminal, _ := fields["response"].ReadObject()
	output, _ := readArray(terminal["output"])
	for index := range stream.responseMessages {
		if index >= len(output) {
			return nil, unsupported("/response/output", "terminal snapshot omitted a started message")
		}
		item, err := output[index].ReadObject()
		if err != nil || !item["type"].IsString("message") {
			return nil, unsupported("/response/output", "terminal snapshot replaced a started message")
		}
	}
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
}
