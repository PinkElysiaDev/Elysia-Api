package relay

import (
	"encoding/json"
	"strings"
)

type usageWireField struct {
	counter, path string
	count         int
}

func marshalUsagePresence(typed any, raw map[string]any, usage *MaheshvaraUsage, fields []usageWireField) ([]byte, error) {
	encoded, err := json.Marshal(typed)
	if err != nil {
		return nil, err
	}
	var object map[string]any
	if err := decodeWireJSON(encoded, &object); err != nil {
		return nil, err
	}
	object = mergeUsageObjects(raw, object)
	if usage != nil && usage.present != nil {
		for _, field := range fields {
			parts := strings.Split(field.path, ".")
			parent := object
			for _, key := range parts[:len(parts)-1] {
				child, ok := parent[key].(map[string]any)
				if !ok {
					child = make(map[string]any)
					parent[key] = child
				}
				parent = child
			}
			key := parts[len(parts)-1]
			if usage.HasCounter(field.counter) {
				parent[key] = field.count
			} else {
				delete(parent, key)
			}
		}
		removeEmptyUsageObjects(object)
	}
	return json.Marshal(object)
}

func removeEmptyUsageObjects(object map[string]any) {
	for key, value := range object {
		if child, ok := value.(map[string]any); ok {
			removeEmptyUsageObjects(child)
			if len(child) == 0 {
				delete(object, key)
			}
		}
	}
}

// MarshalJSON preserves observed zero and omitted counters in translated usage.
func (usage ClaudeUsage) MarshalJSON() ([]byte, error) {
	type alias ClaudeUsage
	return marshalUsagePresence(alias(usage), nil, usage.semantic, []usageWireField{
		{"input", "input_tokens", usage.InputTokens}, {"output", "output_tokens", usage.OutputTokens},
		{"cached", "cache_read_input_tokens", usage.CacheReadInputTokens}, {"cache_creation", "cache_creation_input_tokens", usage.CacheCreationInputTokens},
	})
}

// MarshalJSON preserves observed zero and omitted counters in translated usage.
func (usage GeminiUsageMeta) MarshalJSON() ([]byte, error) {
	type alias GeminiUsageMeta
	return marshalUsagePresence(alias(usage), nil, usage.semantic, []usageWireField{
		{"input", "promptTokenCount", usage.PromptTokenCount}, {"output", "candidatesTokenCount", usage.CandidatesTokenCount},
		{"total", "totalTokenCount", usage.TotalTokenCount}, {"cached", "cachedContentTokenCount", usage.CachedContentTokenCount},
		{"reasoning", "thoughtsTokenCount", usage.ThoughtsTokenCount}, {"tool_use", "toolUsePromptTokenCount", usage.ToolUsePromptTokenCount},
	})
}

// MarshalJSON preserves observed zero and omitted counters in translated usage.
func (usage ResponsesUsage) MarshalJSON() ([]byte, error) {
	type alias ResponsesUsage
	fields := []usageWireField{{"input", "input_tokens", usage.InputTokens}, {"output", "output_tokens", usage.OutputTokens}, {"total", "total_tokens", usage.TotalTokens}}
	if usage.semantic != nil {
		fields = append(fields, usageWireField{"cached", "input_tokens_details.cached_tokens", usage.semantic.CachedInputTokens}, usageWireField{"reasoning", "output_tokens_details.reasoning_tokens", usage.semantic.ReasoningTokens})
	}
	return marshalUsagePresence(alias(usage), nil, usage.semantic, fields)
}
