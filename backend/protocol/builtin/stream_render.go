package builtin

import (
	"context"
	"fmt"

	p "github.com/elysia-api/backend/protocol"
)

func (stream *streamModule) encodeEvent(event p.Event, options p.EvaluationContext) ([]p.Value, error) {
	if event.Unmapped != nil {
		return nil, unsupported("/unmapped", "unmapped wire fields require original frame preservation or an explicit mapping")
	}
	stream.usage = p.MergeUsage(stream.usage, event.Usage)
	if event.Response != nil {
		if err := stream.module.preserveExtensions(p.Object{}, event.Response.Attributes); err != nil {
			return nil, err
		}
		stream.attributes = mergeAttributes(stream.attributes, event.Response.Attributes)
		stream.usage = p.MergeUsage(stream.usage, event.Response.Usage)
		if !event.Response.Model.IsZero() {
			stream.model = event.Response.Model
		}
		if !event.Response.ID.IsZero() {
			stream.id = event.Response.ID
		}
	}
	if !event.ResponseID.IsZero() {
		stream.id = event.ResponseID
	}
	switch event.Type {
	case p.OperationFailed, p.OperationCancelled:
		stream.hasFailed = true
		stream.pending = nil
		failure := event.Error
		if event.Type == p.OperationCancelled {
			failure = object(p.Object{"category": p.StringValue(string(ErrorClassInvalidRequest)), "message": p.StringValue("generation cancelled")})
		}
		failure, err := stream.module.encodeFailure(failure, options)
		if err != nil {
			return nil, err
		}
		if stream.name == Anthropic || stream.name == Responses {
			return []p.Value{object(p.Object{"type": p.StringValue("error"), "error": failure})}, nil
		}
		return []p.Value{object(p.Object{"error": failure})}, nil
	case p.ResponseStarted:
		stream.isStarted = true
		return stream.encodeStart(options)
	case p.UsageUpdated, p.MetadataUpdated:
		return nil, nil
	case p.ResponseFinished:
		if stream.pending != nil {
			return nil, fmt.Errorf("duplicate semantic terminal")
		}
		stream.pending = &event
		return nil, nil
	case p.ItemStarted, p.ItemDelta, p.ItemSnapshot, p.ItemFinished:
		return stream.encodeItem(event, options)
	case p.NativeEvent:
		return nil, unsupported("/event", "native event cannot be converted to a different wire protocol")
	default:
		return nil, unsupported("/event", "target has no equivalent streaming event")
	}
}

func (stream *streamModule) encodeStart(options p.EvaluationContext) ([]p.Value, error) {
	switch stream.name {
	case Chat:
		return []p.Value{stream.chatChunk(object(p.Object{"role": p.StringValue("assistant")}), p.Value{}, p.Value{})}, nil
	case Anthropic:
		usage, err := stream.module.encodeUsage(stream.usage, options)
		if err != nil {
			return nil, err
		}
		message := object(p.Object{"id": stream.id, "model": stream.model, "type": p.StringValue("message"), "role": p.StringValue("assistant"), "content": array(nil), "usage": usage, "stop_reason": nullValue(), "stop_sequence": nullValue()})
		return []p.Value{object(p.Object{"type": p.StringValue("message_start"), "message": message})}, nil
	case Responses:
		return []p.Value{object(p.Object{"type": p.StringValue("response.created"), "response": object(p.Object{"id": stream.id, "model": stream.model, "object": p.StringValue("response"), "status": p.StringValue("in_progress"), "created_at": stream.attributes["created_at"], "output": array(nil), "store": responsesStorageValue(options)})})}, nil
	case Gemini:
		return nil, nil
	}
	return nil, fmt.Errorf("unsupported stream target")
}

func (stream *streamModule) encodeItem(event p.Event, options p.EvaluationContext) ([]p.Value, error) {
	if event.Item != nil && stream.name != Responses && (event.Item.ReasoningForm != "" || event.Item.ReasoningContent != nil) {
		return nil, unsupported("/reasoningForm", "structured reasoning requires an explicit projection to visible thinking")
	}
	key, err := stream.identities.Resolve(event)
	if err != nil {
		return nil, err
	}
	item := stream.items[key]
	if event.Type == p.ItemStarted {
		if item != nil {
			return nil, fmt.Errorf("duplicate target item")
		}
		if len(stream.items) >= stream.limits.StateItems {
			return nil, unsupported("/items", "target stream item limit exceeded")
		}
		item = &streamItem{node: itemMetadata(*event.Item), index: len(stream.order)}
		stream.items[key] = item
		stream.order = append(stream.order, key)
	} else if item == nil {
		return nil, fmt.Errorf("target item has no start")
	}
	if event.Item != nil {
		if err := checkResourceProtocol(*event.Item, options); err != nil {
			return nil, err
		}
		if len(event.Item.Attributes) > 0 {
			return nil, unsupported("/item/attributes", "stream item extensions require original frame preservation or an explicit event mapping")
		}
		item.node.Metadata = mergeResponseMetadata(item.node.Metadata, event.Item.Metadata)
		if !event.Item.Name.IsZero() {
			item.node.Name = event.Item.Name
		}
		if !event.Item.CallID.IsZero() {
			item.node.CallID = event.Item.CallID
		}
		if !event.Item.ID.IsZero() {
			item.node.ID = event.Item.ID
		}
		if !event.Item.Status.IsZero() {
			item.node.Status = event.Item.Status
		}
		if event.Item.Children != nil {
			item.node.Children = event.Item.Children
		}
		if event.Item.ReasoningContent != nil {
			item.node.ReasoningContent = event.Item.ReasoningContent
		}
		if event.Item.Resources != nil {
			item.node.Resources = event.Item.Resources
		}
	}
	item.node.Metadata = p.MergeNodeMetadata(item.node.Metadata, event.Metadata, event.Type == p.ItemDelta)
	if !event.CallID.IsZero() {
		item.node.CallID = event.CallID
	}
	if event.Item != nil || !event.CallID.IsZero() {
		if err := stream.storeItemMetadata(item, item.node); err != nil {
			return nil, err
		}
	}
	value := event.Delta
	isSnapshot := p.IsItemSnapshot(event.Type)
	if isSnapshot && event.Item != nil {
		value = event.Item.Payload
		if event.Item.Input != nil {
			value = event.Item.Input.Value
		}
	}
	var delta string
	if !value.IsZero() {
		if item.node.Kind == p.ToolCallNode && item.node.Input.Kind == p.JSONInput && isSnapshot {
			delta = string(value.Bytes())
		} else {
			delta, err = stringValue(value)
			if err != nil {
				return nil, err
			}
		}
		if isSnapshot {
			if item.node.Kind == p.ToolCallNode && item.node.Input.Kind == p.JSONInput {
				delta, err = p.JSONToolSnapshotSuffix(item.buffer.String(), delta)
				if err == nil {
					item.text.Append(delta)
				}
			} else {
				delta, err = item.text.Snapshot(delta)
			}
			if err != nil {
				return nil, err
			}
		} else {
			item.text.Append(delta)
		}
		if stream.needsPayload(item) {
			if stream.buffered+len(delta) > stream.limits.BufferBytes {
				return nil, unsupported("/content", "target cumulative response exceeds buffer limit")
			}
			item.buffer.WriteString(delta)
			stream.buffered += len(delta)
		}
	}
	var frames []p.Value
	if !item.hasEmittedStart {
		if item.node.Kind == p.ToolCallNode && (item.node.Name.IsZero() || item.node.CallID.IsZero()) {
			return nil, nil
		}
		item.wireID = item.node.ID
		if item.wireID.IsZero() {
			item.wireID = p.StringValue(key)
		}
		start, err := stream.encodeItemStart(key, item, options)
		if err != nil {
			return nil, err
		}
		frames = append(frames, start...)
		item.hasEmittedStart = true
		if item.node.Kind == p.ToolCallNode {
			delta = item.buffer.String()
		}
	}
	if item.node.ReasoningForm == p.SummaryReasoning || item.node.ReasoningForm == p.StructuredReasoning {
		updates, err := stream.encodeSummaryParts(item)
		if err != nil {
			return nil, err
		}
		frames = append(frames, updates...)
		if item.node.ReasoningForm == p.StructuredReasoning {
			updates, err = stream.encodeVisibleReasoningParts(item)
			if err != nil {
				return nil, err
			}
			frames = append(frames, updates...)
		}
	}
	if delta != "" {
		update, err := stream.encodeDelta(key, item, delta)
		if err != nil {
			return nil, err
		}
		frames = append(frames, update...)
	}
	signatureFrames, err := stream.encodeSignatureUpdates(event, item)
	if err != nil {
		return nil, err
	}
	frames = append(frames, signatureFrames...)
	if event.Type == p.ItemFinished {
		end, err := stream.encodeItemEnd(key, item, options)
		if err != nil {
			return nil, err
		}
		frames = append(frames, end...)
	}
	return frames, nil
}

func (stream *streamModule) encodeItemStart(key string, item *streamItem, options p.EvaluationContext) ([]p.Value, error) {
	node := item.node
	if node.Kind != p.TextNode && node.Kind != p.ReasoningNode && node.Kind != p.RefusalNode && node.Kind != p.ToolCallNode {
		return nil, unsupported("/item", "stream target cannot express this item kind")
	}
	switch stream.name {
	case Chat:
		if node.Kind != p.ToolCallNode {
			return nil, nil
		}
		if node.Input.Kind != p.JSONInput {
			return nil, unsupported("/input", "Chat streaming requires JSON function arguments")
		}
		position, _ := p.EncodeValue(item.index)
		return []p.Value{stream.chatChunk(object(p.Object{"tool_calls": array([]p.Value{object(p.Object{"index": position, "id": node.CallID, "type": p.StringValue("function"), "function": object(p.Object{"name": node.Name, "arguments": p.StringValue("")})})})}), p.Value{}, p.Value{})}, nil
	case Anthropic:
		block := p.Object{"type": p.StringValue("text"), "text": p.StringValue("")}
		if node.Kind == p.ReasoningNode {
			block = p.Object{"type": p.StringValue("thinking"), "thinking": p.StringValue("")}
			for _, resource := range node.Resources {
				if resource.Kind == "encrypted_content" {
					block = p.Object{"type": p.StringValue("redacted_thinking"), "data": resource.ID}
				}
			}
		}
		if node.Kind == p.ToolCallNode {
			if node.Input.Kind != p.JSONInput {
				return nil, unsupported("/input", "Anthropic streaming requires JSON function input")
			}
			block = p.Object{"type": p.StringValue("tool_use"), "id": node.CallID, "name": node.Name, "input": object(p.Object{})}
		}
		if node.Kind == p.RefusalNode {
			return nil, unsupported("/item", "Anthropic cannot express refusal blocks")
		}
		return []p.Value{stream.anthropicEvent("content_block_start", item, object(block), "content_block")}, nil
	case Gemini:
		return nil, nil
	case Responses:
		id := item.wireID
		block := p.Object{"id": id, "type": p.StringValue("message"), "role": p.StringValue("assistant"), "status": p.StringValue("in_progress"), "content": array(nil)}
		if node.Kind == p.ReasoningNode {
			block = p.Object{"id": id, "type": p.StringValue("reasoning"), "status": p.StringValue("in_progress"), "summary": array(nil)}
			if node.ReasoningForm == "" || node.ReasoningContent != nil {
				block["content"] = array(nil)
			}
		}
		if node.Kind == p.ToolCallNode {
			block = p.Object{"id": id, "type": p.StringValue("function_call"), "name": node.Name, "call_id": node.CallID, "arguments": p.StringValue(""), "status": p.StringValue("in_progress")}
			if node.Input.Kind == p.TextInput {
				delete(block, "arguments")
				block["type"], block["input"] = p.StringValue("custom_tool_call"), p.StringValue("")
			}
		}
		frames := []p.Value{stream.responsesEvent("response.output_item.added", key, item, "item", object(block))}
		if node.Kind == p.ReasoningNode && node.ReasoningForm == "" {
			frames = append(frames, stream.responsesEvent("response.content_part.added", key, item, "part", object(p.Object{"type": p.StringValue("reasoning_text"), "text": p.StringValue("")})))
		}
		if node.Kind != p.ToolCallNode && node.Kind != p.ReasoningNode {
			part := p.Object{"type": p.StringValue("output_text"), "text": p.StringValue(""), "annotations": array(nil)}
			if node.Kind == p.RefusalNode {
				part = p.Object{"type": p.StringValue("refusal"), "refusal": p.StringValue("")}
			}
			frames = append(frames, stream.responsesEvent("response.content_part.added", key, item, "part", object(part)))
		}
		return frames, nil
	}
	return nil, fmt.Errorf("unsupported stream target")
}

func (stream *streamModule) encodeDelta(key string, item *streamItem, delta string) ([]p.Value, error) {
	kind := item.node.Kind
	switch stream.name {
	case Chat:
		fields := p.Object{"content": p.StringValue(delta)}
		if kind == p.ReasoningNode {
			fields = p.Object{"reasoning_content": p.StringValue(delta)}
		}
		if kind == p.RefusalNode {
			fields = p.Object{"refusal": p.StringValue(delta)}
		}
		if kind == p.ToolCallNode {
			position, _ := p.EncodeValue(item.index)
			// Chat clients concatenate string deltas, including name and id.
			// Identity was emitted once by encodeItemStart; only input grows here.
			fields = p.Object{"tool_calls": array([]p.Value{object(p.Object{"index": position, "function": object(p.Object{"arguments": p.StringValue(delta)})})})}
		}
		return []p.Value{stream.chatChunk(object(fields), p.Value{}, p.Value{})}, nil
	case Anthropic:
		deltaKind, field := "text_delta", "text"
		if kind == p.ReasoningNode {
			deltaKind, field = "thinking_delta", "thinking"
		}
		if kind == p.ToolCallNode {
			deltaKind, field = "input_json_delta", "partial_json"
		}
		return []p.Value{stream.anthropicEvent("content_block_delta", item, object(p.Object{"type": p.StringValue(deltaKind), field: p.StringValue(delta)}), "delta")}, nil
	case Responses:
		eventKind := "response.output_text.delta"
		if kind == p.ReasoningNode {
			eventKind = "response.reasoning_text.delta"
		}
		if kind == p.RefusalNode {
			eventKind = "response.refusal.delta"
		}
		if kind == p.ToolCallNode {
			eventKind = "response.function_call_arguments.delta"
			if item.node.Input.Kind == p.TextInput {
				eventKind = "response.custom_tool_call_input.delta"
			}
		}
		return []p.Value{stream.responsesEvent(eventKind, key, item, "delta", p.StringValue(delta))}, nil
	case Gemini:
		if kind == p.ToolCallNode {
			return nil, nil
		}
		fields := p.Object{"text": p.StringValue(delta)}
		if kind == p.ReasoningNode {
			flag, _ := p.EncodeValue(true)
			fields["thought"] = flag
		}
		return []p.Value{stream.geminiChunk(array([]p.Value{object(fields)}), p.Value{}, p.Value{})}, nil
	}
	return nil, fmt.Errorf("unsupported stream target")
}

func (stream *streamModule) materialize(item *streamItem) (p.Node, error) {
	node := item.node
	if node.ReasoningForm != "" {
		return node, nil
	}
	if node.Kind != p.ToolCallNode {
		node.Payload = p.StringValue(item.buffer.String())
		return node, nil
	}
	value := p.StringValue(item.buffer.String())
	if node.Input.Kind == p.JSONInput {
		var err error
		value, err = p.ParseValue([]byte(item.buffer.String()))
		if err != nil {
			return node, err
		}
	}
	node.Input = &p.ToolInput{Kind: node.Input.Kind, Value: value}
	return node, nil
}

// Responses requires full output in its terminal snapshot. Other targets only
// retain incomplete tool input; text already forwarded is tracked by digest.
func (stream *streamModule) needsPayload(item *streamItem) bool {
	return stream.name == Responses || item.node.Kind == p.ToolCallNode
}

func (stream *streamModule) encodeItemEnd(key string, item *streamItem, options p.EvaluationContext) ([]p.Value, error) {
	if item.isFinished {
		return nil, fmt.Errorf("target item completed twice")
	}
	if !item.hasEmittedStart {
		return nil, unsupported("/item", "tool completed without a name and call identity")
	}
	node := item.node
	if stream.needsPayload(item) {
		var err error
		node, err = stream.materialize(item)
		if err != nil {
			return nil, err
		}
	}
	if stream.name != Responses {
		stream.buffered -= item.buffer.Len()
		item.buffer.Reset()
	}
	item.isFinished = true
	switch stream.name {
	case Chat:
		return nil, nil
	case Anthropic:
		return []p.Value{stream.anthropicEvent("content_block_stop", item, p.Value{}, "")}, nil
	case Gemini:
		if node.Kind != p.ToolCallNode {
			return nil, nil
		}
		block, err := stream.module.encodeBlock(node, p.EncodeResponse, options)
		if err != nil {
			return nil, err
		}
		return []p.Value{stream.geminiChunk(array([]p.Value{block}), p.Value{}, p.Value{})}, nil
	case Responses:
		if node.Kind == p.ToolCallNode || node.Kind == p.ReasoningNode {
			node.ID = item.wireID
			node.Status = p.StringValue("completed")
		}
		block, err := stream.module.encodeBlock(node, p.EncodeResponse, options)
		if err != nil {
			return nil, err
		}
		if node.Kind == p.ToolCallNode {
			return []p.Value{stream.responsesEvent("response.output_item.done", key, item, "item", block)}, nil
		}
		if node.Kind == p.ReasoningNode && node.ReasoningForm == "" {
			part := object(p.Object{"type": p.StringValue("reasoning_text"), "text": node.Payload})
			return []p.Value{stream.responsesEvent("response.reasoning_text.done", key, item, "text", node.Payload), stream.responsesEvent("response.content_part.done", key, item, "part", part), stream.responsesEvent("response.output_item.done", key, item, "item", block)}, nil
		}
		if node.ReasoningForm == p.SummaryReasoning || node.ReasoningForm == p.StructuredReasoning {
			frames := []p.Value{}
			for index, child := range node.Children {
				frames = append(frames, stream.summaryEvent("response.reasoning_summary_text.done", item, index, "text", child.Payload), stream.summaryEvent("response.reasoning_summary_part.done", item, index, "part", object(p.Object{"type": p.StringValue("summary_text"), "text": child.Payload})))
			}
			for index, part := range node.ReasoningContent {
				frames = append(frames, stream.visibleEvent("response.reasoning_text.done", item, index, "text", part.Payload), stream.visibleEvent("response.content_part.done", item, index, "part", object(p.Object{"type": p.StringValue("reasoning_text"), "text": part.Payload})))
			}
			return append(frames, stream.responsesEvent("response.output_item.done", key, item, "item", block)), nil
		}
		message := object(p.Object{"id": item.wireID, "type": p.StringValue("message"), "role": p.StringValue("assistant"), "status": p.StringValue("completed"), "content": array([]p.Value{block})})
		kind, field := "response.output_text.done", "text"
		if node.Kind == p.RefusalNode {
			kind, field = "response.refusal.done", "refusal"
		}
		return []p.Value{stream.responsesEvent(kind, key, item, field, node.Payload), stream.responsesEvent("response.content_part.done", key, item, "part", block), stream.responsesEvent("response.output_item.done", key, item, "item", message)}, nil
	}
	return nil, fmt.Errorf("unsupported stream target")
}

func (stream *streamModule) Finish(ctx context.Context, options p.EvaluationContext) ([]p.Value, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if stream.hasFailed {
		return nil, nil
	}
	if stream.pending == nil {
		return nil, fmt.Errorf("stream ended without a semantic terminal")
	}
	var frames []p.Value
	content := []p.Node{}
	for _, key := range stream.order {
		item := stream.items[key]
		if !item.isFinished {
			end, err := stream.encodeItemEnd(key, item, options)
			if err != nil {
				return nil, err
			}
			frames = append(frames, end...)
		}
		if stream.name == Responses {
			node, err := stream.materialize(item)
			if err != nil {
				return nil, err
			}
			if node.Kind == p.ToolCallNode || node.Kind == p.ReasoningNode {
				node.ID = item.wireID
				node.Status = p.StringValue("completed")
			} else {
				node = p.Node{Kind: p.MessageNode, ID: item.wireID, Status: p.StringValue("completed"), Role: p.StringValue("assistant"), Children: []p.Node{node}}
			}
			content = append(content, node)
		} else {
			content = append(content, item.node)
		}
	}
	response := &p.Response{SchemaVersion: p.SemanticSchemaVersion, ID: stream.id, Model: stream.model, Status: p.StringValue("completed"), Content: content, Usage: stream.usage, Attributes: copyFields(stream.attributes)}
	if stream.pending.Response != nil {
		response.Status = stream.pending.Response.Status
	}
	reason, err := finishReasonOf(response)
	if err != nil {
		return nil, err
	}
	finish, err := encodeFinishReason(stream.name, reason)
	if err != nil && stream.name != Responses {
		return nil, err
	}
	usage, err := stream.module.encodeUsage(stream.usage, options)
	if err != nil {
		return nil, err
	}
	switch stream.name {
	case Chat:
		frames = append(frames, stream.chatChunk(object(p.Object{}), finish, p.Value{}))
		if !usage.IsZero() && (options.ClientOutput == nil || (options.ClientOutput.IncludeUsage != nil && *options.ClientOutput.IncludeUsage)) {
			frames = append(frames, object(p.Object{"id": stream.id, "model": stream.model, "object": p.StringValue("chat.completion.chunk"), "created": stream.attributes["created_at"], "choices": array(nil), "usage": usage}))
		}
	case Anthropic:
		stopSequence := stream.attributes["anthropic_stop_sequence"]
		if stopSequence.IsZero() {
			stopSequence = nullValue()
		}
		frames = append(frames, object(p.Object{"type": p.StringValue("message_delta"), "delta": object(p.Object{"stop_reason": finish, "stop_sequence": stopSequence}), "usage": usage}), object(p.Object{"type": p.StringValue("message_stop")}))
	case Gemini:
		frames = append(frames, stream.geminiChunk(array(nil), finish, usage))
	case Responses:
		value, err := stream.module.encodeResponse(response, options)
		if err != nil {
			return nil, err
		}
		status, _, err := encodeResponsesFinish(response)
		if err != nil {
			return nil, err
		}
		statusName, err := stringValue(status)
		if err != nil {
			return nil, err
		}
		if statusName != "completed" && statusName != "incomplete" {
			return nil, unsupported("/status", "response terminal requires completed or incomplete status")
		}
		frames = append(frames, object(p.Object{"type": p.StringValue("response." + statusName), "response": value}))
	}
	stream.pending = nil
	frames, err = stream.renderMetadata(frames, nil)
	if err != nil {
		return nil, err
	}
	return stream.numberFrames(frames), nil
}

func (stream *streamModule) numberFrames(frames []p.Value) []p.Value {
	if stream.name != Responses {
		return frames
	}
	for index, value := range frames {
		// Frames originate from this codec's closed object constructors.
		fields, _ := value.ReadObject()
		fields["sequence_number"], _ = p.EncodeValue(stream.sequence)
		stream.sequence++
		frames[index] = object(fields)
	}
	return frames
}

func (stream *streamModule) chatChunk(delta, finish, usage p.Value) p.Value {
	index, _ := p.EncodeValue(0)
	return object(p.Object{"id": stream.id, "model": stream.model, "created": stream.attributes["created_at"], "object": p.StringValue("chat.completion.chunk"), "choices": array([]p.Value{object(p.Object{"index": index, "delta": delta, "finish_reason": finish})}), "usage": usage})
}
func (stream *streamModule) anthropicEvent(kind string, item *streamItem, value p.Value, field string) p.Value {
	index, _ := p.EncodeValue(item.index)
	fields := p.Object{"type": p.StringValue(kind), "index": index}
	if field != "" {
		fields[field] = value
	}
	return object(fields)
}
func (stream *streamModule) responsesEvent(kind, key string, item *streamItem, field string, value p.Value) p.Value {
	index, _ := p.EncodeValue(item.index)
	zero, _ := p.EncodeValue(0)
	return object(p.Object{"type": p.StringValue(kind), "response_id": stream.id, "item_id": item.wireID, "output_index": index, "content_index": zero, field: value})
}
func (stream *streamModule) geminiChunk(parts, finish, usage p.Value) p.Value {
	index, _ := p.EncodeValue(0)
	return object(p.Object{"responseId": stream.id, "modelVersion": stream.model, "candidates": array([]p.Value{object(p.Object{"index": index, "content": object(p.Object{"role": p.StringValue("model"), "parts": parts}), "finishReason": finish})}), "usageMetadata": usage})
}

// encodeSignatureUpdates 在快照事件携带签名资源时产出增量帧：仅 Anthropic
// 与 Gemini 可表达推理签名，其余目标显式拒绝。
func (stream *streamModule) encodeSignatureUpdates(event p.Event, item *streamItem) ([]p.Value, error) {
	if event.Type != p.ItemSnapshot || event.Item == nil || len(event.Item.Resources) == 0 || item.node.ReasoningForm != "" {
		return nil, nil
	}
	if (stream.name != Anthropic && stream.name != Gemini) || item.node.Kind != p.ReasoningNode {
		return nil, unsupported("/resources", "target cannot express this reasoning signature update")
	}
	var frames []p.Value
	for _, resource := range event.Item.Resources {
		if resource.Kind != "signature" {
			return nil, unsupported("/resources", "unsupported stream resource")
		}
		if stream.name == Anthropic {
			signature, err := stringValue(resource.ID)
			if err != nil {
				return nil, err
			}
			delta, err := item.signature.Snapshot(signature)
			if err != nil {
				return nil, err
			}
			if delta != "" {
				frames = append(frames, stream.anthropicEvent("content_block_delta", item, object(p.Object{"type": p.StringValue("signature_delta"), "signature": p.StringValue(delta)}), "delta"))
			}
			continue
		}
		isThought, _ := p.EncodeValue(true)
		frames = append(frames, stream.geminiChunk(array([]p.Value{object(p.Object{"text": p.StringValue(""), "thought": isThought, "thoughtSignature": resource.ID})}), p.Value{}, p.Value{}))
	}
	return frames, nil
}
