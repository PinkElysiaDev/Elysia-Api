package relay

func int64Value(value any) int64 {
	number, ok := numberValue(value)
	if !ok {
		return 0
	}
	return int64(number)
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
	// 原始 usage 对象整体留存：同线渲染时未知计数键原样透传（XF5b：不重释、
	// 不丢弃），已知键由类型化字段覆盖。
	usage.Raw = raw
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
