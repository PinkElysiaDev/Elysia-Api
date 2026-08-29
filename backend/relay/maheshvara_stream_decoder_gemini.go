package relay

import (
	"encoding/json"
	"fmt"
	"strings"
)

func (decoder *MaheshvaraStreamDecoder) decodeGemini(raw map[string]any) ([]MaheshvaraStreamEvent, error) {
	decoder.responseID = firstNonEmptyString(stringValue(raw["responseId"]), decoder.responseID)
	decoder.model = firstNonEmptyString(stringValue(raw["modelVersion"]), decoder.model)
	var events []MaheshvaraStreamEvent
	if usage := maheshvaraUsageFromRawMap(mapValue(raw["usageMetadata"])); usage != nil {
		event := decoder.baseEvent(MaheshvaraEventUsageDelta, raw)
		event.Usage = usage
		events = append(events, event)
	}

	candidates, _ := raw["candidates"].([]any)
	for _, candidateValue := range candidates {
		candidate := mapValue(candidateValue)
		if candidate == nil {
			continue
		}
		choiceIndex := intValue(candidate["index"])
		decoder.seenChoices[choiceIndex] = true
		// 搜索/据实来源标注随 candidate 到达：挂到同 chunk 首个文本事件上，
		// 渲染层据此回写 candidate.groundingMetadata。
		grounding := mapValue(candidate["groundingMetadata"])
		groundingEmitted := false
		content := mapValue(candidate["content"])
		parts, _ := content["parts"].([]any)
		for partIndex, partValue := range parts {
			part := mapValue(partValue)
			if part == nil {
				continue
			}
			if signature := firstNonEmptyString(stringValue(part["thoughtSignature"]), stringValue(part["thought_signature"])); signature != "" {
				event := decoder.baseEvent(MaheshvaraEventReasoningSignatureDelta, raw)
				event.ChoiceIndex = choiceIndex
				event.ContentIndex = partIndex
				event.ReasoningSignatureDelta = signature
				event.ReasoningSignatureProvider = MaheshvaraSignatureProviderGemini
				events = append(events, event)
			}
			if text := stringValue(part["text"]); text != "" {
				event := decoder.baseEvent(MaheshvaraEventTextDelta, raw)
				event.ChoiceIndex = choiceIndex
				event.ContentIndex = partIndex
				if boolValue(part["thought"]) {
					event.Type = MaheshvaraEventReasoningDelta
					event.ReasoningDelta = text
				} else {
					event.Delta = text
					if !groundingEmitted && grounding != nil {
						groundingEmitted = true
						event.Annotations = []map[string]any{{MaheshvaraAnnotationGeminiGrounding: grounding}}
					}
				}
				events = append(events, event)
			}
			if functionCall := mapValue(part["functionCall"]); functionCall != nil {
				// 合成 id 用解码器级单调计数器：partIndex 是 chunk 内序号（每块
				// 从 0 重新计），跨 chunk 的两个无 id 调用会撞成 call_0_0。
				callID := firstNonEmptyString(stringValue(functionCall["id"]), fmt.Sprintf("call_syn_%d", decoder.nextSyntheticCallID))
				decoder.nextSyntheticCallID++
				name := stringValue(functionCall["name"])
				added := decoder.baseEvent(MaheshvaraEventFunctionCallAdded, raw)
				added.ChoiceIndex = choiceIndex
				added.ContentIndex = partIndex
				added.ToolCallIndex = partIndex
				added.ToolCallID = callID
				added.ToolName = name
				events = append(events, added)
				arguments, err := json.Marshal(firstNonNilValue(functionCall["args"], map[string]any{}))
				if err != nil {
					return nil, fmt.Errorf("encode Gemini function call arguments: %w", err)
				}
				done := decoder.baseEvent(MaheshvaraEventFunctionCallArgumentsDone, raw)
				done.ChoiceIndex = choiceIndex
				done.ContentIndex = partIndex
				done.ToolCallIndex = partIndex
				done.ToolCallID = callID
				done.ToolName = name
				done.ToolArgumentsDone = string(arguments)
				events = append(events, done)
			}
			if functionResponse := mapValue(part["functionResponse"]); functionResponse != nil {
				maheshvaraPart := MaheshvaraContentPart{
					Type:       MaheshvaraContentToolOutput,
					ToolCallID: firstNonEmptyString(stringValue(functionResponse["id"]), stringValue(functionResponse["name"])),
					ToolOutput: contentValueToString(functionResponse["response"]),
					Raw:        functionResponse,
				}
				event := decoder.baseEvent(MaheshvaraEventContentPartAdded, raw)
				event.ChoiceIndex = choiceIndex
				event.ContentIndex = partIndex
				event.ContentPart = &maheshvaraPart
				events = append(events, event)
			}
			if maheshvaraPart := geminiStreamMediaPart(part); maheshvaraPart != nil {
				event := decoder.baseEvent(MaheshvaraEventContentPartAdded, raw)
				event.ChoiceIndex = choiceIndex
				event.ContentIndex = partIndex
				event.ContentPart = maheshvaraPart
				events = append(events, event)
			}
			if executable := mapValue(part["executableCode"]); executable != nil {
				maheshvaraPart := MaheshvaraContentPart{Type: "executable_code", Text: stringValue(executable["code"]), Metadata: map[string]any{"language": executable["language"]}, Raw: part}
				event := decoder.baseEvent(MaheshvaraEventContentPartAdded, raw)
				event.ChoiceIndex = choiceIndex
				event.ContentIndex = partIndex
				event.ContentPart = &maheshvaraPart
				events = append(events, event)
			}
			if execution := mapValue(part["codeExecutionResult"]); execution != nil {
				maheshvaraPart := MaheshvaraContentPart{Type: "code_execution_result", Text: stringValue(execution["output"]), Metadata: map[string]any{"outcome": execution["outcome"]}, Raw: part}
				event := decoder.baseEvent(MaheshvaraEventContentPartAdded, raw)
				event.ChoiceIndex = choiceIndex
				event.ContentIndex = partIndex
				event.ContentPart = &maheshvaraPart
				events = append(events, event)
			}
		}
		if finishReason := stringValue(candidate["finishReason"]); finishReason != "" {
			decoder.sawFinishReason = true
			decoder.finishedChoices[choiceIndex] = true
			event := decoder.baseEvent(MaheshvaraEventResponseCompleted, raw)
			event.ChoiceIndex = choiceIndex
			event.FinishReason = finishReason
			events = append(events, event)
		}
	}
	if len(decoder.seenChoices) > 0 && allMaheshvaraChoicesFinished(decoder.seenChoices, decoder.finishedChoices) {
		decoder.terminal = true
	}
	if len(candidates) == 0 {
		if feedback := mapValue(raw["promptFeedback"]); feedback != nil && stringValue(feedback["blockReason"]) != "" {
			decoder.terminal = true
			event := decoder.baseEvent(MaheshvaraEventResponseFailed, raw)
			event.Error = &MaheshvaraError{Message: "Gemini request blocked: " + stringValue(feedback["blockReason"]), Type: "content_filter", Raw: feedback}
			events = append(events, event)
		}
	}
	return events, nil
}

func geminiStreamMediaPart(part map[string]any) *MaheshvaraContentPart {
	if inline := mapValue(firstNonNilValue(part["inlineData"], part["inline_data"])); inline != nil {
		mediaType := firstNonEmptyString(stringValue(inline["mimeType"]), stringValue(inline["mime_type"]))
		data := stringValue(inline["data"])
		return &MaheshvaraContentPart{Type: maheshvaraMediaContentType(mediaType), Data: data, MediaType: mediaType, MimeType: mediaType, Raw: part}
	}
	if file := mapValue(firstNonNilValue(part["fileData"], part["file_data"])); file != nil {
		mediaType := firstNonEmptyString(stringValue(file["mimeType"]), stringValue(file["mime_type"]))
		uri := firstNonEmptyString(stringValue(file["fileUri"]), stringValue(file["file_uri"]))
		return &MaheshvaraContentPart{Type: maheshvaraMediaContentType(mediaType), URI: uri, MediaType: mediaType, MimeType: mediaType, Raw: part}
	}
	return nil
}

func maheshvaraMediaContentType(mediaType string) string {
	switch {
	case strings.HasPrefix(mediaType, "image/"):
		return MaheshvaraContentImage
	case strings.HasPrefix(mediaType, "audio/"):
		return MaheshvaraContentAudio
	case strings.HasPrefix(mediaType, "video/"):
		return MaheshvaraContentVideo
	default:
		return MaheshvaraContentFile
	}
}
