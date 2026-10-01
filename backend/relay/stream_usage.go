package relay

import "github.com/elysia-api/backend/protocol"

// MergeUsageSnapshots merges provider snapshots for accounting as well as
// stream rendering, preserving explicit zeros and input component provenance.
func MergeUsageSnapshots(current, update *MaheshvaraUsage) *MaheshvaraUsage {
	return mergeMaheshvaraStreamUsage(current, update)
}

// usageCounters is the transitional projection of the canonical usage model.
// Keeping the projection together prevents partial stream merges from dropping
// a less common modality, cache TTL or hosted-tool counter.
func usageCounters(usage *MaheshvaraUsage) map[string]*int {
	return map[string]*int{
		"input": &usage.InputTokens, "output": &usage.OutputTokens, "total": &usage.TotalTokens,
		"cached": &usage.CachedInputTokens, "cache_creation": &usage.CacheCreationInputTokens,
		"cache_5m": &usage.CacheCreation5mTokens, "cache_1h": &usage.CacheCreation1hTokens,
		"reasoning": &usage.ReasoningTokens, "tool_prompt": &usage.ToolPromptTokens,
		"tool_use": &usage.ToolUseTokens, "accepted": &usage.AcceptedPredictionTokens,
		"rejected":   &usage.RejectedPredictionTokens,
		"text_input": &usage.TextInputTokens, "text_output": &usage.TextOutputTokens,
		"image_input": &usage.ImageInputTokens, "image_output": &usage.ImageOutputTokens,
		"audio_input": &usage.AudioInputTokens, "audio_output": &usage.AudioOutputTokens,
		"web_search": &usage.WebSearchCallCount, "file_search": &usage.FileSearchCallCount,
		"image_generation": &usage.ImageGenerationCallCount, "code_interpreter": &usage.CodeInterpreterCallCount,
		"computer_use": &usage.ComputerUseCallCount,
	}
}

// HasCounter distinguishes a reported zero from a missing counter at the
// legacy accounting boundary. Programmatically constructed legacy values can
// only express presence through nonzero counters until the v2 cutover.
func (usage *MaheshvaraUsage) HasCounter(name string) bool {
	if usage == nil {
		return false
	}
	if usage.present != nil {
		return usage.present[name]
	}
	count := usageCounters(usage)[name]
	return count != nil && *count != 0
}

func semanticUsage(usage *MaheshvaraUsage) *protocol.Usage {
	if usage == nil {
		return nil
	}
	result := &protocol.Usage{Details: make(map[string]protocol.Counter)}
	for name, value := range usageCounters(usage) {
		if (usage.present != nil && !usage.present[name]) || (usage.present == nil && *value == 0) {
			continue
		}
		counter := &protocol.Counter{Count: int64(*value), Origin: protocol.ObservedCount}
		switch name {
		case "input":
			result.Input = counter
		case "output":
			result.Output = counter
		case "cached":
			result.CacheRead = counter
		case "cache_creation":
			if usage.cacheCreationInferred {
				counter.Origin = protocol.InferredCount
			}
			result.CacheCreation = counter
		case "total":
			if usage.TotalTokensInferred {
				counter.Origin = protocol.InferredCount
			}
			result.Total = counter
		default:
			result.Details[name] = *counter
		}
	}
	return result
}

func applySemanticUsage(usage *MaheshvaraUsage, canonical *protocol.Usage) {
	usage.present = make(map[string]bool)
	counters := usageCounters(usage)
	apply := func(name string, counter *protocol.Counter) {
		if counter != nil {
			*counters[name] = int(counter.Count)
			usage.present[name] = true
		}
	}
	apply("input", canonical.Input)
	apply("output", canonical.Output)
	apply("total", canonical.Total)
	apply("cached", canonical.CacheRead)
	apply("cache_creation", canonical.CacheCreation)
	for name, counter := range canonical.Details {
		apply(name, &counter)
	}
	usage.TotalTokensInferred = canonical.Total != nil && canonical.Total.Origin == protocol.InferredCount
	usage.cacheCreationInferred = canonical.CacheCreation != nil && canonical.CacheCreation.Origin == protocol.InferredCount
}

func mergeMaheshvaraStreamUsage(current, update *MaheshvaraUsage) *MaheshvaraUsage {
	if update == nil {
		return current
	}
	merged := &MaheshvaraUsage{}
	if current != nil {
		*merged = *current
	}
	applySemanticUsage(merged, protocol.MergeUsage(semanticUsage(current), semanticUsage(update)))
	if merged.cacheCreationInferred {
		merged.CacheCreationInputTokens = merged.CacheCreation5mTokens + merged.CacheCreation1hTokens
	}
	if update.rawInputTokens != nil {
		input := *update.rawInputTokens
		merged.rawInputTokens = &input
	}
	merged.cacheInputExclusive = merged.cacheInputExclusive || update.cacheInputExclusive
	merged.toolInputExclusive = merged.toolInputExclusive || update.toolInputExclusive
	merged.reasoningOutputExclusive = merged.reasoningOutputExclusive || update.reasoningOutputExclusive
	if update.rawOutputTokens != nil {
		output := *update.rawOutputTokens
		merged.rawOutputTokens = &output
	}
	if merged.cacheInputExclusive && merged.rawInputTokens != nil {
		merged.InputTokens = *merged.rawInputTokens + merged.CachedInputTokens + merged.CacheCreationInputTokens
		merged.present["input"] = true
	}
	if merged.toolInputExclusive && merged.rawInputTokens != nil {
		merged.InputTokens = *merged.rawInputTokens + merged.ToolUseTokens
		merged.present["input"] = true
	}
	if merged.reasoningOutputExclusive && merged.rawOutputTokens != nil {
		merged.OutputTokens = *merged.rawOutputTokens + merged.ReasoningTokens
		merged.present["output"] = true
	}
	if merged.TotalTokensInferred {
		merged.TotalTokens = merged.InputTokens + merged.OutputTokens
	}
	if update.Source != "" {
		merged.Source = update.Source
	}
	if update.Provider != "" {
		merged.Provider = update.Provider
	}
	merged.Raw = mergeUsageObjects(merged.Raw, update.Raw)
	return merged
}

func mergeUsageObjects(current, update map[string]any) map[string]any {
	if current == nil && update == nil {
		return nil
	}
	merged := make(map[string]any, len(current)+len(update))
	for key, value := range current {
		merged[key] = value
	}
	for key, value := range update {
		if object, ok := value.(map[string]any); ok {
			merged[key] = mergeUsageObjects(mapValue(merged[key]), object)
		} else {
			merged[key] = value
		}
	}
	return merged
}
