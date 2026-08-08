package relay

func (decoder *MaheshvaraStreamDecoder) decodeAnthropic(raw map[string]any) ([]MaheshvaraStreamEvent, error) {
	typeName := stringValue(raw["type"])
	var events []MaheshvaraStreamEvent
	switch typeName {
	case "message_start":
		message := mapValue(raw["message"])
		decoder.responseID = firstNonEmptyString(stringValue(message["id"]), decoder.responseID)
		decoder.model = firstNonEmptyString(stringValue(message["model"]), decoder.model)
		event := decoder.baseEvent(CanonicalEventResponseCreated, raw)
		event.Role = firstNonEmptyString(stringValue(message["role"]), "assistant")
		event.Status = "in_progress"
		events = append(events, event)
		if usage := canonicalUsageFromRawMap(mapValue(message["usage"])); usage != nil {
			usageEvent := decoder.baseEvent(CanonicalEventUsageDelta, raw)
			usageEvent.Usage = usage
			events = append(events, usageEvent)
		}
	case "content_block_start":
		index := intValue(raw["index"])
		blockValue := mapValue(raw["content_block"])
		block := &maheshvaraAnthropicBlock{
			typeName: stringValue(blockValue["type"]),
			id:       firstNonEmptyString(stringValue(blockValue["id"]), stringValue(raw["content_block_id"])),
			name:     stringValue(blockValue["name"]),
		}
		decoder.anthropicBlocks[index] = block
		switch block.typeName {
		case "tool_use", "server_tool_use":
			event := decoder.baseEvent(CanonicalEventFunctionCallAdded, raw)
			event.ContentIndex = index
			event.ToolCallIndex = index
			event.ToolCallID = block.id
			event.ToolName = block.name
			events = append(events, event)
		case "thinking":
			part := CanonicalContentPart{Type: CanonicalContentReasoning, Thought: true, SignatureProvider: CanonicalSignatureProviderAnthropic, Raw: blockValue}
			event := decoder.baseEvent(CanonicalEventContentPartAdded, raw)
			event.ContentIndex = index
			event.ContentPart = &part
			events = append(events, event)
		case "redacted_thinking":
			if envelope, ok := decodeMaheshvaraReasoningEnvelope(stringValue(blockValue["data"])); ok {
				part := CanonicalContentPart{Type: CanonicalContentReasoning, Thought: true, ReasoningText: envelope.Text, Text: envelope.Text, SignatureProvider: CanonicalSignatureProviderMaheshvara, EncryptedContent: envelope.EncryptedContent, ReasoningSummary: envelope.Summary, Raw: blockValue}
				event := decoder.baseEvent(CanonicalEventContentPartAdded, raw)
				event.ContentIndex = index
				event.ContentPart = &part
				events = append(events, event)
			}
		case "text":
			part := CanonicalContentPart{Type: CanonicalContentText, Raw: blockValue}
			event := decoder.baseEvent(CanonicalEventContentPartAdded, raw)
			event.ContentIndex = index
			event.ContentPart = &part
			events = append(events, event)
		default:
			part := CanonicalContentPart{Type: block.typeName, Raw: blockValue}
			event := decoder.baseEvent(CanonicalEventContentPartAdded, raw)
			event.ContentIndex = index
			event.ContentPart = &part
			events = append(events, event)
		}
	case "content_block_delta":
		index := intValue(raw["index"])
		block := decoder.anthropicBlocks[index]
		delta := mapValue(raw["delta"])
		switch stringValue(delta["type"]) {
		case "text_delta":
			if text := stringValue(delta["text"]); text != "" {
				event := decoder.baseEvent(CanonicalEventTextDelta, raw)
				event.ContentIndex = index
				event.Delta = text
				events = append(events, event)
			}
		case "thinking_delta":
			if text := stringValue(delta["thinking"]); text != "" {
				event := decoder.baseEvent(CanonicalEventReasoningDelta, raw)
				event.ContentIndex = index
				event.ReasoningDelta = text
				events = append(events, event)
			}
		case "signature_delta":
			if signature := stringValue(delta["signature"]); signature != "" {
				event := decoder.baseEvent(CanonicalEventReasoningSignatureDelta, raw)
				event.ContentIndex = index
				event.ReasoningSignatureDelta = signature
				event.ReasoningSignatureProvider = CanonicalSignatureProviderAnthropic
				events = append(events, event)
			}
		case "input_json_delta":
			arguments := stringValue(delta["partial_json"])
			if block != nil {
				block.arguments.WriteString(arguments)
			}
			if arguments != "" {
				event := decoder.baseEvent(CanonicalEventFunctionCallArgumentsDelta, raw)
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
				part := CanonicalContentPart{Type: CanonicalContentText, Annotations: []map[string]any{citation}, Raw: delta}
				event := decoder.baseEvent(CanonicalEventContentPartAdded, raw)
				event.ContentIndex = index
				event.ContentPart = &part
				events = append(events, event)
			}
		}
	case "content_block_stop":
		index := intValue(raw["index"])
		if block := decoder.anthropicBlocks[index]; block != nil && (block.typeName == "tool_use" || block.typeName == "server_tool_use") {
			event := decoder.baseEvent(CanonicalEventFunctionCallArgumentsDone, raw)
			event.ContentIndex = index
			event.ToolCallIndex = index
			event.ToolCallID = block.id
			event.ToolName = block.name
			event.ToolArgumentsDone = firstNonEmptyString(block.arguments.String(), "{}")
			events = append(events, event)
		}
		event := decoder.baseEvent(CanonicalEventOutputItemDone, raw)
		event.ContentIndex = index
		events = append(events, event)
	case "message_delta":
		if usage := canonicalUsageFromRawMap(mapValue(raw["usage"])); usage != nil {
			usageEvent := decoder.baseEvent(CanonicalEventUsageDelta, raw)
			usageEvent.Usage = usage
			events = append(events, usageEvent)
		}
		delta := mapValue(raw["delta"])
		if finishReason := stringValue(delta["stop_reason"]); finishReason != "" {
			decoder.terminal = true
			event := decoder.baseEvent(CanonicalEventResponseCompleted, raw)
			event.FinishReason = finishReason
			event.StopSequence = stringValue(delta["stop_sequence"])
			events = append(events, event)
		}
	case "message_stop":
		if !decoder.terminal {
			decoder.terminal = true
			events = append(events, decoder.baseEvent(CanonicalEventResponseCompleted, raw))
		}
	case "error":
		decoder.terminal = true
		errorValue := mapValue(raw["error"])
		event := decoder.baseEvent(CanonicalEventResponseFailed, raw)
		event.Error = &CanonicalError{Message: firstNonEmptyString(stringValue(errorValue["message"]), "Anthropic stream error"), Type: stringValue(errorValue["type"]), Raw: errorValue}
		events = append(events, event)
	case "ping":
		return nil, nil
	default:
		if typeName != "" {
			events = append(events, decoder.baseEvent(typeName, raw))
		}
	}
	return events, nil
}
