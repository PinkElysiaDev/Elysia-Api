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
	if usage := canonicalUsageFromRawMap(mapValue(raw["usageMetadata"])); usage != nil {
		event := decoder.baseEvent(CanonicalEventUsageDelta, raw)
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
		content := mapValue(candidate["content"])
		parts, _ := content["parts"].([]any)
		for partIndex, partValue := range parts {
			part := mapValue(partValue)
			if part == nil {
				continue
			}
			if signature := firstNonEmptyString(stringValue(part["thoughtSignature"]), stringValue(part["thought_signature"])); signature != "" {
				event := decoder.baseEvent(CanonicalEventReasoningSignatureDelta, raw)
				event.ChoiceIndex = choiceIndex
				event.ContentIndex = partIndex
				event.ReasoningSignatureDelta = signature
				event.ReasoningSignatureProvider = CanonicalSignatureProviderGemini
				events = append(events, event)
			}
			if text := stringValue(part["text"]); text != "" {
				event := decoder.baseEvent(CanonicalEventTextDelta, raw)
				event.ChoiceIndex = choiceIndex
				event.ContentIndex = partIndex
				if boolValue(part["thought"]) {
					event.Type = CanonicalEventReasoningDelta
					event.ReasoningDelta = text
				} else {
					event.Delta = text
				}
				events = append(events, event)
			}
			if functionCall := mapValue(part["functionCall"]); functionCall != nil {
				callID := firstNonEmptyString(stringValue(functionCall["id"]), fmt.Sprintf("call_%d_%d", choiceIndex, partIndex))
				name := stringValue(functionCall["name"])
				added := decoder.baseEvent(CanonicalEventFunctionCallAdded, raw)
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
				done := decoder.baseEvent(CanonicalEventFunctionCallArgumentsDone, raw)
				done.ChoiceIndex = choiceIndex
				done.ContentIndex = partIndex
				done.ToolCallIndex = partIndex
				done.ToolCallID = callID
				done.ToolName = name
				done.ToolArgumentsDone = string(arguments)
				events = append(events, done)
			}
			if functionResponse := mapValue(part["functionResponse"]); functionResponse != nil {
				canonicalPart := CanonicalContentPart{
					Type:       CanonicalContentToolOutput,
					ToolCallID: firstNonEmptyString(stringValue(functionResponse["id"]), stringValue(functionResponse["name"])),
					ToolOutput: contentValueToString(functionResponse["response"]),
					Raw:        functionResponse,
				}
				event := decoder.baseEvent(CanonicalEventContentPartAdded, raw)
				event.ChoiceIndex = choiceIndex
				event.ContentIndex = partIndex
				event.ContentPart = &canonicalPart
				events = append(events, event)
			}
			if canonicalPart := geminiStreamMediaPart(part); canonicalPart != nil {
				event := decoder.baseEvent(CanonicalEventContentPartAdded, raw)
				event.ChoiceIndex = choiceIndex
				event.ContentIndex = partIndex
				event.ContentPart = canonicalPart
				events = append(events, event)
			}
			if executable := mapValue(part["executableCode"]); executable != nil {
				canonicalPart := CanonicalContentPart{Type: "executable_code", Text: stringValue(executable["code"]), Metadata: map[string]any{"language": executable["language"]}, Raw: part}
				event := decoder.baseEvent(CanonicalEventContentPartAdded, raw)
				event.ChoiceIndex = choiceIndex
				event.ContentIndex = partIndex
				event.ContentPart = &canonicalPart
				events = append(events, event)
			}
			if execution := mapValue(part["codeExecutionResult"]); execution != nil {
				canonicalPart := CanonicalContentPart{Type: "code_execution_result", Text: stringValue(execution["output"]), Metadata: map[string]any{"outcome": execution["outcome"]}, Raw: part}
				event := decoder.baseEvent(CanonicalEventContentPartAdded, raw)
				event.ChoiceIndex = choiceIndex
				event.ContentIndex = partIndex
				event.ContentPart = &canonicalPart
				events = append(events, event)
			}
		}
		if finishReason := stringValue(candidate["finishReason"]); finishReason != "" {
			decoder.finishedChoices[choiceIndex] = true
			event := decoder.baseEvent(CanonicalEventResponseCompleted, raw)
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
			event := decoder.baseEvent(CanonicalEventResponseFailed, raw)
			event.Error = &CanonicalError{Message: "Gemini request blocked: " + stringValue(feedback["blockReason"]), Type: "content_filter", Raw: feedback}
			events = append(events, event)
		}
	}
	return events, nil
}

func geminiStreamMediaPart(part map[string]any) *CanonicalContentPart {
	if inline := mapValue(firstNonNilValue(part["inlineData"], part["inline_data"])); inline != nil {
		mediaType := firstNonEmptyString(stringValue(inline["mimeType"]), stringValue(inline["mime_type"]))
		data := stringValue(inline["data"])
		return &CanonicalContentPart{Type: canonicalMediaContentType(mediaType), Data: data, MediaType: mediaType, MimeType: mediaType, Raw: part}
	}
	if file := mapValue(firstNonNilValue(part["fileData"], part["file_data"])); file != nil {
		mediaType := firstNonEmptyString(stringValue(file["mimeType"]), stringValue(file["mime_type"]))
		uri := firstNonEmptyString(stringValue(file["fileUri"]), stringValue(file["file_uri"]))
		return &CanonicalContentPart{Type: canonicalMediaContentType(mediaType), URI: uri, MediaType: mediaType, MimeType: mediaType, Raw: part}
	}
	return nil
}

func canonicalMediaContentType(mediaType string) string {
	switch {
	case strings.HasPrefix(mediaType, "image/"):
		return CanonicalContentImage
	case strings.HasPrefix(mediaType, "audio/"):
		return CanonicalContentAudio
	case strings.HasPrefix(mediaType, "video/"):
		return CanonicalContentVideo
	default:
		return CanonicalContentFile
	}
}
