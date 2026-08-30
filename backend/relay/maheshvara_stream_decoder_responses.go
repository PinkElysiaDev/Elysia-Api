package relay

import (
	"encoding/json"
	"fmt"
	"strings"
)

func (decoder *MaheshvaraStreamDecoder) decodeResponses(raw map[string]any) ([]MaheshvaraStreamEvent, error) {
	typeName := stringValue(raw["type"])
	responseValue := mapValue(raw["response"])
	decoder.responseID = firstNonEmptyString(stringValue(raw["response_id"]), stringValue(responseValue["id"]), decoder.responseID)
	decoder.model = firstNonEmptyString(stringValue(raw["model"]), stringValue(responseValue["model"]), decoder.model)
	event := decoder.baseEvent(typeName, raw)
	event.ItemID = stringValue(raw["item_id"])
	event.OutputIndex = intValue(raw["output_index"])
	event.ContentIndex = intValue(raw["content_index"])
	event.Sequence = int64Value(raw["sequence_number"])
	event.Status = stringValue(responseValue["status"])

	switch typeName {
	case MaheshvaraEventResponseCreated, MaheshvaraEventResponseInProgress:
		if responseValue != nil {
			if response, err := responsesMapToMaheshvara(responseValue); err == nil {
				event.Response = response
			}
		}
		return []MaheshvaraStreamEvent{event}, nil
	case MaheshvaraEventOutputItemAdded, MaheshvaraEventOutputItemDone:
		item, err := responsesOutputMapToMaheshvara(mapValue(raw["item"]))
		if err != nil {
			return nil, err
		}
		event.OutputItem = item
		if item != nil && item.Type == MaheshvaraOutputFunctionCall {
			event.ToolCallID = item.CallID
			event.ToolName = item.Name
			event.ToolArgumentsDone = string(item.Arguments)
			event.ToolCallIndex = event.OutputIndex
		}
		return []MaheshvaraStreamEvent{event}, nil
	case MaheshvaraEventContentPartAdded, MaheshvaraEventContentPartDone:
		part, err := responsesContentMapToMaheshvara(mapValue(raw["part"]))
		if err != nil {
			return nil, err
		}
		event.ContentPart = part
		return []MaheshvaraStreamEvent{event}, nil
	case MaheshvaraEventTextDelta:
		event.Delta = stringValue(raw["delta"])
		return []MaheshvaraStreamEvent{event}, nil
	case MaheshvaraEventTextDone:
		event.TextDone = firstNonEmptyString(stringValue(raw["text"]), stringValue(raw["content"]), stringValue(raw["delta"]))
		return []MaheshvaraStreamEvent{event}, nil
	case MaheshvaraEventRefusalDelta:
		event.RefusalDelta = stringValue(raw["delta"])
		return []MaheshvaraStreamEvent{event}, nil
	case MaheshvaraEventRefusalDone:
		event.RefusalDone = firstNonEmptyString(stringValue(raw["refusal"]), stringValue(raw["text"]), stringValue(raw["delta"]))
		return []MaheshvaraStreamEvent{event}, nil
	case MaheshvaraEventReasoningDelta, MaheshvaraEventReasoningSummaryDelta, "response.reasoning_text.delta":
		event.ReasoningDelta = stringValue(raw["delta"])
		return []MaheshvaraStreamEvent{event}, nil
	case MaheshvaraEventReasoningDone, MaheshvaraEventReasoningSummaryDone, "response.reasoning_text.done":
		event.ReasoningDone = firstNonEmptyString(stringValue(raw["text"]), stringValue(raw["delta"]), stringValue(raw["content"]))
		return []MaheshvaraStreamEvent{event}, nil
	case MaheshvaraEventReasoningSignatureDelta, "response.reasoning_signature.done":
		// 此前落入 default 且按 "reasoning" 前缀被误当推理文本拼接。
		signature := firstNonEmptyString(stringValue(raw["delta"]), stringValue(raw["signature"]), stringValue(raw["text"]))
		if signature != "" {
			event.Type = MaheshvaraEventReasoningSignatureDelta
			event.ReasoningSignatureDelta = signature
			event.ReasoningSignatureProvider = firstNonEmptyString(stringValue(raw["provider"]), MaheshvaraSignatureProviderOpenAI)
			return []MaheshvaraStreamEvent{event}, nil
		}
		return nil, nil
	case MaheshvaraEventFunctionCallArgumentsDelta:
		event.ToolCallID = stringValue(raw["call_id"])
		event.ToolName = stringValue(raw["name"])
		event.ToolArgumentsDelta = stringValue(raw["delta"])
		event.ToolCallIndex = event.OutputIndex
		return []MaheshvaraStreamEvent{event}, nil
	case MaheshvaraEventFunctionCallArgumentsDone:
		event.ToolCallID = stringValue(raw["call_id"])
		event.ToolName = stringValue(raw["name"])
		event.ToolArgumentsDone = firstNonEmptyString(stringValue(raw["arguments"]), stringValue(raw["delta"]))
		event.ToolCallIndex = event.OutputIndex
		return []MaheshvaraStreamEvent{event}, nil
	case MaheshvaraEventUsageDelta:
		event.Usage = maheshvaraUsageFromRawMap(mapValue(raw["usage"]))
		return []MaheshvaraStreamEvent{event}, nil
	case MaheshvaraEventResponseCompleted:
		decoder.terminal = true
		decoder.sawFinishReason = true
		if responseValue != nil {
			response, err := responsesMapToMaheshvara(responseValue)
			if err != nil {
				return nil, err
			}
			event.Response = response
			event.FinishReason = response.StopReason
			if response.Usage != nil {
				usageEvent := decoder.baseEvent(MaheshvaraEventUsageDelta, raw)
				usageEvent.Usage = response.Usage
				return []MaheshvaraStreamEvent{usageEvent, event}, nil
			}
		}
		return []MaheshvaraStreamEvent{event}, nil
	case MaheshvaraEventResponseFailed, "response.incomplete", "error":
		decoder.terminal = true
		errorValue := mapValue(raw["error"])
		if errorValue == nil {
			errorValue = mapValue(responseValue["error"])
		}
		event.Type = MaheshvaraEventResponseFailed
		event.Error = &MaheshvaraError{Message: firstNonEmptyString(stringValue(errorValue["message"]), "OpenAI Responses stream failed"), Type: stringValue(errorValue["type"]), Code: stringValue(errorValue["code"]), Param: stringValue(errorValue["param"]), Raw: errorValue}
		return []MaheshvaraStreamEvent{event}, nil
	default:
		if strings.Contains(typeName, "function_call") {
			event.ToolCallID = stringValue(raw["call_id"])
			event.ToolName = stringValue(raw["name"])
			event.ToolArgumentsDelta = stringValue(raw["delta"])
			event.ToolArgumentsDone = stringValue(raw["arguments"])
		}
		if strings.Contains(typeName, "reasoning") {
			event.ReasoningDelta = stringValue(raw["delta"])
		}
		if typeName == "" {
			return nil, nil
		}
		return []MaheshvaraStreamEvent{event}, nil
	}
}

func responsesMapToMaheshvara(raw map[string]any) (*MaheshvaraResponse, error) {
	if raw == nil {
		return nil, nil
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("encode Responses stream response: %w", err)
	}
	var response OpenAIResponsesResponse
	if err := json.Unmarshal(encoded, &response); err != nil {
		return nil, fmt.Errorf("decode Responses stream response: %w", err)
	}
	return OpenAIResponsesResponseToMaheshvara(&response)
}

func responsesOutputMapToMaheshvara(raw map[string]any) (*MaheshvaraOutputItem, error) {
	if raw == nil {
		return nil, nil
	}
	response, err := responsesMapToMaheshvara(map[string]any{"id": "stream", "object": "response", "status": "in_progress", "created_at": 0, "model": "", "output": []any{raw}})
	if err != nil || response == nil || len(response.Output) == 0 {
		return nil, err
	}
	item := response.Output[0]
	return &item, nil
}

func responsesContentMapToMaheshvara(raw map[string]any) (*MaheshvaraContentPart, error) {
	if raw == nil {
		return nil, nil
	}
	item, err := responsesOutputMapToMaheshvara(map[string]any{"id": "stream_part", "type": MaheshvaraOutputMessage, "status": "in_progress", "role": "assistant", "content": []any{raw}})
	if err != nil || item == nil || len(item.Content) == 0 {
		return nil, err
	}
	part := item.Content[0]
	return &part, nil
}
