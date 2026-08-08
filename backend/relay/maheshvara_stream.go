package relay

import (
	"encoding/json"
	"fmt"
	"strings"
)

func OpenAIChatStreamChunkToMaheshvara(body []byte) (*MaheshvaraStreamEvent, error) {
	raw, err := decodeStreamJSON(body)
	if err != nil {
		return nil, fmt.Errorf("parse OpenAI Chat stream chunk: %w", err)
	}
	event := &MaheshvaraStreamEvent{Raw: rawMap(raw)}
	event.ResponseID = stringValue(raw["id"])
	event.CreatedAt = int64Value(raw["created"])
	if usage := canonicalUsageFromRawMap(mapValue(raw["usage"])); usage != nil {
		event.Usage = usage
		event.Type = CanonicalEventUsageDelta
	}
	choices, _ := raw["choices"].([]any)
	if len(choices) == 0 {
		if event.Type == "" {
			event.Type = CanonicalEventResponseInProgress
		}
		return event, nil
	}
	choice, _ := choices[0].(map[string]any)
	event.ChoiceIndex = intValue(choice["index"])
	event.FinishReason = stringValue(choice["finish_reason"])
	delta, _ := choice["delta"].(map[string]any)
	event.Role = stringValue(delta["role"])
	if text := stringValue(delta["content"]); text != "" {
		event.Type = CanonicalEventTextDelta
		event.Delta = text
	}
	if reasoning := stringValue(delta["reasoning_content"]); reasoning != "" {
		event.Type = CanonicalEventReasoningDelta
		event.ReasoningDelta = reasoning
	}
	if toolCalls, ok := delta["tool_calls"].([]any); ok {
		for _, item := range toolCalls {
			toolCall, _ := item.(map[string]any)
			function, _ := toolCall["function"].(map[string]any)
			event.Type = CanonicalEventFunctionCallArgumentsDelta
			event.ToolCallID = stringValue(toolCall["id"])
			event.ToolName = stringValue(function["name"])
			event.ToolArgumentsDelta += stringValue(function["arguments"])
		}
	}
	if event.Type == "" && event.FinishReason != "" {
		event.Type = CanonicalEventResponseCompleted
	}
	return event, nil
}

func AnthropicStreamEventToMaheshvara(body []byte) (*MaheshvaraStreamEvent, error) {
	raw, err := decodeStreamJSON(body)
	if err != nil {
		return nil, fmt.Errorf("parse Anthropic stream event: %w", err)
	}
	event := &MaheshvaraStreamEvent{Raw: rawMap(raw)}
	typeName := stringValue(raw["type"])
	switch typeName {
	case "message_start":
		event.Type = CanonicalEventResponseCreated
		message, _ := raw["message"].(map[string]any)
		event.ResponseID = stringValue(message["id"])
	case "content_block_start":
		event.Type = CanonicalEventContentPartAdded
		event.ItemID = stringValue(raw["content_block_id"])
	case "content_block_delta":
		delta, _ := raw["delta"].(map[string]any)
		switch stringValue(delta["type"]) {
		case "text_delta":
			event.Type = CanonicalEventTextDelta
			event.Delta = stringValue(delta["text"])
		case "thinking_delta":
			event.Type = CanonicalEventReasoningDelta
			event.ReasoningDelta = stringValue(delta["thinking"])
		case "signature_delta":
			event.Type = CanonicalEventReasoningSignatureDelta
			event.ReasoningSignatureDelta = stringValue(delta["signature"])
			event.ReasoningSignatureProvider = CanonicalSignatureProviderAnthropic
		case "input_json_delta":
			event.Type = CanonicalEventFunctionCallArgumentsDelta
			event.ToolArgumentsDelta = stringValue(delta["partial_json"])
		}
	case "content_block_stop":
		event.Type = CanonicalEventContentPartAdded
	case "message_delta":
		delta, _ := raw["delta"].(map[string]any)
		event.Type = CanonicalEventResponseCompleted
		event.FinishReason = stringValue(delta["stop_reason"])
		if usage := canonicalUsageFromRawMap(mapValue(raw["usage"])); usage != nil {
			event.Usage = usage
		}
	case "message_stop":
		event.Type = CanonicalEventResponseCompleted
	default:
		event.Type = typeName
	}
	return event, nil
}

func GeminiStreamChunkToMaheshvara(body []byte) (*MaheshvaraStreamEvent, error) {
	raw, err := decodeStreamJSON(body)
	if err != nil {
		return nil, fmt.Errorf("parse Gemini stream chunk: %w", err)
	}
	event := &MaheshvaraStreamEvent{Raw: rawMap(raw)}
	if usage := canonicalUsageFromRawMap(mapValue(raw["usageMetadata"])); usage != nil {
		event.Usage = usage
		event.Type = CanonicalEventUsageDelta
	}
	candidates, _ := raw["candidates"].([]any)
	if len(candidates) == 0 {
		if event.Type == "" {
			event.Type = CanonicalEventResponseInProgress
		}
		return event, nil
	}
	candidate, _ := candidates[0].(map[string]any)
	event.FinishReason = stringValue(candidate["finishReason"])
	content, _ := candidate["content"].(map[string]any)
	parts, _ := content["parts"].([]any)
	for _, item := range parts {
		part, _ := item.(map[string]any)
		if signature := firstNonEmptyString(stringValue(part["thoughtSignature"]), stringValue(part["thought_signature"])); signature != "" {
			event.ReasoningSignatureDelta = signature
			event.ReasoningSignatureProvider = CanonicalSignatureProviderGemini
		}
		if text := stringValue(part["text"]); text != "" {
			if boolValue(part["thought"]) {
				event.Type = CanonicalEventReasoningDelta
				event.ReasoningDelta = text
			} else {
				event.Type = CanonicalEventTextDelta
				event.Delta = text
			}
		}
		if functionCall, ok := part["functionCall"].(map[string]any); ok {
			event.Type = CanonicalEventFunctionCallAdded
			event.ToolName = stringValue(functionCall["name"])
			event.ToolArgumentsDone = stringValue(jsonString(functionCall["args"]))
		}
	}
	if event.Type == "" && event.FinishReason != "" {
		event.Type = CanonicalEventResponseCompleted
	}
	return event, nil
}

func OpenAIResponsesStreamEventToMaheshvara(body []byte) (*MaheshvaraStreamEvent, error) {
	raw, err := decodeStreamJSON(body)
	if err != nil {
		return nil, fmt.Errorf("parse OpenAI Responses stream event: %w", err)
	}
	event := &MaheshvaraStreamEvent{
		Type:         stringValue(raw["type"]),
		ResponseID:   stringValue(raw["response_id"]),
		ItemID:       stringValue(raw["item_id"]),
		OutputIndex:  intValue(raw["output_index"]),
		ContentIndex: intValue(raw["content_index"]),
		Delta:        stringValue(raw["delta"]),
		Raw:          rawMap(raw),
	}
	if event.Type == CanonicalEventUsageDelta {
		event.Usage = canonicalUsageFromRawMap(mapValue(raw["usage"]))
	}
	if strings.Contains(event.Type, "reasoning") {
		event.ReasoningDelta = event.Delta
	}
	if strings.Contains(event.Type, "function_call") {
		event.ToolCallID = stringValue(raw["call_id"])
		event.ToolName = stringValue(raw["name"])
		event.ToolArgumentsDelta = stringValue(raw["delta"])
		event.ToolArgumentsDone = stringValue(raw["arguments"])
	}
	return event, nil
}

func MaheshvaraStreamEventToOpenAIChatChunk(event *MaheshvaraStreamEvent) ([]byte, error) {
	if event == nil {
		return nil, fmt.Errorf("nil Maheshvara stream event")
	}
	delta := map[string]any{}
	if event.Role != "" {
		delta["role"] = event.Role
	}
	if event.Delta != "" {
		delta["content"] = event.Delta
	}
	if event.ReasoningDelta != "" {
		delta["reasoning_content"] = event.ReasoningDelta
	}
	toolArguments := event.ToolArgumentsDelta
	if toolArguments == "" {
		toolArguments = event.ToolArgumentsDone
	}
	if toolArguments != "" || event.ToolName != "" {
		delta["tool_calls"] = []map[string]any{{"index": event.ChoiceIndex, "id": event.ToolCallID, "type": "function", "function": map[string]any{"name": event.ToolName, "arguments": toolArguments}}}
	}
	chunk := map[string]any{"id": event.ResponseID, "object": "chat.completion.chunk", "choices": []map[string]any{{"index": event.ChoiceIndex, "delta": delta, "finish_reason": nil}}}
	if event.FinishReason != "" {
		chunk["choices"] = []map[string]any{{"index": event.ChoiceIndex, "delta": delta, "finish_reason": canonicalStopToOpenAI(event.FinishReason)}}
	}
	if event.Usage != nil {
		chunk["usage"] = openAIUsageFromCanonical(event.Usage)
	}
	return json.Marshal(chunk)
}

func decodeStreamJSON(body []byte) (map[string]any, error) {
	text := strings.TrimSpace(string(body))
	if strings.HasPrefix(text, "data:") {
		text = strings.TrimSpace(strings.TrimPrefix(text, "data:"))
	}
	if text == "[DONE]" || text == "" {
		return map[string]any{"type": CanonicalEventResponseCompleted}, nil
	}
	var raw map[string]any
	if err := json.Unmarshal([]byte(text), &raw); err != nil {
		return nil, err
	}
	return raw, nil
}

func rawMap(value map[string]any) map[string]any {
	return value
}

func int64Value(value any) int64 {
	number, ok := numberValue(value)
	if !ok {
		return 0
	}
	return int64(number)
}

func jsonString(value any) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func canonicalUsageFromRawMap(raw map[string]any) *CanonicalUsage {
	if len(raw) == 0 {
		return nil
	}
	usage := &CanonicalUsage{
		InputTokens:       intValue(firstNonValue(raw, "input_tokens", "inputTokens", "prompt_tokens", "promptTokenCount")),
		OutputTokens:      intValue(firstNonValue(raw, "output_tokens", "outputTokens", "completion_tokens", "candidatesTokenCount")),
		TotalTokens:       intValue(firstNonValue(raw, "total_tokens", "totalTokens", "totalTokenCount")),
		CachedInputTokens: intValue(firstNonValue(raw, "cached_tokens", "cachedInputTokens", "cachedContentTokenCount")),
		ReasoningTokens:   intValue(firstNonValue(raw, "reasoning_tokens", "reasoningTokens", "thoughtsTokenCount")),
		Source:            "provider_stream",
	}
	if usage.TotalTokens == 0 {
		usage.TotalTokens = usage.InputTokens + usage.OutputTokens
	}
	return usage
}

func firstNonValue(values map[string]any, keys ...string) any {
	for _, key := range keys {
		if values[key] != nil {
			return values[key]
		}
	}
	return nil
}
