package relay

// decodeAnthropic 将 Anthropic SSE 事件解码为 Maheshvara 流事件；各族事件的
// 具体解码在各自的分支方法中完成。
func (decoder *MaheshvaraStreamDecoder) decodeAnthropic(raw map[string]any) ([]MaheshvaraStreamEvent, error) {
	switch stringValue(raw["type"]) {
	case "message_start":
		return decoder.decodeAnthropicMessageStart(raw), nil
	case "content_block_start":
		return decoder.decodeAnthropicBlockStart(raw), nil
	case "content_block_delta":
		return decoder.decodeAnthropicBlockDelta(raw), nil
	case "content_block_stop":
		return decoder.decodeAnthropicBlockStop(raw), nil
	case "message_delta":
		return decoder.decodeAnthropicMessageDelta(raw), nil
	case "message_stop":
		if !decoder.terminal {
			decoder.terminal = true
			return []MaheshvaraStreamEvent{decoder.baseEvent(MaheshvaraEventResponseCompleted, raw)}, nil
		}
		return nil, nil
	case "error":
		return decoder.decodeAnthropicError(raw), nil
	case "ping":
		return nil, nil
	default:
		if typeName := stringValue(raw["type"]); typeName != "" {
			return []MaheshvaraStreamEvent{decoder.baseEvent(typeName, raw)}, nil
		}
		return nil, nil
	}
}

func (decoder *MaheshvaraStreamDecoder) decodeAnthropicMessageStart(raw map[string]any) []MaheshvaraStreamEvent {
	message := mapValue(raw["message"])
	decoder.responseID = firstNonEmptyString(stringValue(message["id"]), decoder.responseID)
	decoder.model = firstNonEmptyString(stringValue(message["model"]), decoder.model)
	var events []MaheshvaraStreamEvent
	event := decoder.baseEvent(MaheshvaraEventResponseCreated, raw)
	event.Role = firstNonEmptyString(stringValue(message["role"]), "assistant")
	event.Status = "in_progress"
	events = append(events, event)
	if usage := maheshvaraUsageFromRawMap(mapValue(message["usage"])); usage != nil {
		usageEvent := decoder.baseEvent(MaheshvaraEventUsageDelta, raw)
		usageEvent.Usage = usage
		events = append(events, usageEvent)
	}
	return events
}

func (decoder *MaheshvaraStreamDecoder) decodeAnthropicBlockStart(raw map[string]any) []MaheshvaraStreamEvent {
	index := intValue(raw["index"])
	blockValue := mapValue(raw["content_block"])
	block := &maheshvaraAnthropicBlock{
		typeName: stringValue(blockValue["type"]),
		id:       firstNonEmptyString(stringValue(blockValue["id"]), stringValue(raw["content_block_id"])),
		name:     stringValue(blockValue["name"]),
	}
	decoder.anthropicBlocks[index] = block
	var events []MaheshvaraStreamEvent
	switch block.typeName {
	case "tool_use", "server_tool_use":
		event := decoder.baseEvent(MaheshvaraEventFunctionCallAdded, raw)
		event.ContentIndex = index
		event.ToolCallIndex = index
		event.ToolCallID = block.id
		event.ToolName = block.name
		events = append(events, event)
	case "thinking":
		part := MaheshvaraContentPart{Type: MaheshvaraContentReasoning, Thought: true, SignatureProvider: MaheshvaraSignatureProviderAnthropic, Raw: blockValue}
		event := decoder.baseEvent(MaheshvaraEventContentPartAdded, raw)
		event.ContentIndex = index
		event.ContentPart = &part
		events = append(events, event)
	case "redacted_thinking":
		if envelope, ok := decodeMaheshvaraReasoningEnvelope(stringValue(blockValue["data"])); ok {
			part := MaheshvaraContentPart{Type: MaheshvaraContentReasoning, Thought: true, ReasoningText: envelope.Text, Text: envelope.Text, SignatureProvider: MaheshvaraSignatureProviderMaheshvara, EncryptedContent: envelope.EncryptedContent, EncryptedProvider: envelope.Provider, EncryptedModel: envelope.Model, ReasoningSummary: envelope.Summary, Raw: blockValue}
			event := decoder.baseEvent(MaheshvaraEventContentPartAdded, raw)
			event.ContentIndex = index
			event.ContentPart = &part
			events = append(events, event)
		}
	case "text":
		part := MaheshvaraContentPart{Type: MaheshvaraContentText, Raw: blockValue}
		event := decoder.baseEvent(MaheshvaraEventContentPartAdded, raw)
		event.ContentIndex = index
		event.ContentPart = &part
		events = append(events, event)
	default:
		part := MaheshvaraContentPart{Type: block.typeName, Raw: blockValue}
		event := decoder.baseEvent(MaheshvaraEventContentPartAdded, raw)
		event.ContentIndex = index
		event.ContentPart = &part
		events = append(events, event)
	}
	return events
}

func (decoder *MaheshvaraStreamDecoder) decodeAnthropicBlockDelta(raw map[string]any) []MaheshvaraStreamEvent {
	index := intValue(raw["index"])
	block := decoder.anthropicBlocks[index]
	delta := mapValue(raw["delta"])
	var events []MaheshvaraStreamEvent
	switch stringValue(delta["type"]) {
	case "text_delta":
		if text := stringValue(delta["text"]); text != "" {
			event := decoder.baseEvent(MaheshvaraEventTextDelta, raw)
			event.ContentIndex = index
			event.Delta = text
			events = append(events, event)
		}
	case "thinking_delta":
		if text := stringValue(delta["thinking"]); text != "" {
			event := decoder.baseEvent(MaheshvaraEventReasoningDelta, raw)
			event.ContentIndex = index
			event.ReasoningDelta = text
			events = append(events, event)
		}
	case "signature_delta":
		if signature := stringValue(delta["signature"]); signature != "" {
			event := decoder.baseEvent(MaheshvaraEventReasoningSignatureDelta, raw)
			event.ContentIndex = index
			event.ReasoningSignatureDelta = signature
			event.ReasoningSignatureProvider = MaheshvaraSignatureProviderAnthropic
			events = append(events, event)
		}
	case "input_json_delta":
		arguments := stringValue(delta["partial_json"])
		if block != nil {
			block.arguments.WriteString(arguments)
		}
		if arguments != "" {
			event := decoder.baseEvent(MaheshvaraEventFunctionCallArgumentsDelta, raw)
			event.ContentIndex = index
			event.ToolCallIndex = index
			if block != nil {
				event.ToolCallID = block.id
				event.ToolName = block.name
			}
			event.ToolArgumentsDelta = arguments
			events = append(events, event)
		}
	case "citations_delta":
		citation := mapValue(delta["citation"])
		if citation != nil {
			// 引用标注不生成独立 part（空文本 part 在部分渲染器会被当作
			// 畸形块原样回放）；挂到事件 Annotations 载体，由 Claude 渲染器
			// 在对应文本块收尾前发合法的 citations_delta。
			event := decoder.baseEvent(MaheshvaraEventAnnotationDelta, raw)
			event.ContentIndex = index
			event.Annotations = []map[string]any{citation}
			events = append(events, event)
		}
	}
	return events
}

func (decoder *MaheshvaraStreamDecoder) decodeAnthropicBlockStop(raw map[string]any) []MaheshvaraStreamEvent {
	index := intValue(raw["index"])
	var events []MaheshvaraStreamEvent
	if block := decoder.anthropicBlocks[index]; block != nil && (block.typeName == "tool_use" || block.typeName == "server_tool_use") {
		event := decoder.baseEvent(MaheshvaraEventFunctionCallArgumentsDone, raw)
		event.ContentIndex = index
		event.ToolCallIndex = index
		event.ToolCallID = block.id
		event.ToolName = block.name
		event.ToolArgumentsDone = firstNonEmptyString(block.arguments.String(), "{}")
		events = append(events, event)
	}
	event := decoder.baseEvent(MaheshvaraEventOutputItemDone, raw)
	event.ContentIndex = index
	events = append(events, event)
	return events
}

func (decoder *MaheshvaraStreamDecoder) decodeAnthropicMessageDelta(raw map[string]any) []MaheshvaraStreamEvent {
	var events []MaheshvaraStreamEvent
	if usage := maheshvaraUsageFromRawMap(mapValue(raw["usage"])); usage != nil {
		usageEvent := decoder.baseEvent(MaheshvaraEventUsageDelta, raw)
		usageEvent.Usage = usage
		events = append(events, usageEvent)
	}
	delta := mapValue(raw["delta"])
	if finishReason := stringValue(delta["stop_reason"]); finishReason != "" {
		decoder.sawFinishReason = true
		decoder.terminal = true
		event := decoder.baseEvent(MaheshvaraEventResponseCompleted, raw)
		event.FinishReason = finishReason
		event.StopSequence = stringValue(delta["stop_sequence"])
		events = append(events, event)
	}
	return events
}

func (decoder *MaheshvaraStreamDecoder) decodeAnthropicError(raw map[string]any) []MaheshvaraStreamEvent {
	decoder.terminal = true
	errorValue := mapValue(raw["error"])
	event := decoder.baseEvent(MaheshvaraEventResponseFailed, raw)
	errType := stringValue(errorValue["type"])
	event.Error = &MaheshvaraError{Message: firstNonEmptyString(stringValue(errorValue["message"]), "Anthropic stream error"), Type: errType, Class: classFromAnthropicType(errType), Raw: errorValue}
	return []MaheshvaraStreamEvent{event}
}
