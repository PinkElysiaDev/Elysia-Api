package relay

func int64Value(value any) int64 {
	number, ok := numberValue(value)
	if !ok {
		return 0
	}
	return int64(number)
}

func maheshvaraUsageFromRawMap(raw map[string]any) *MaheshvaraUsage {
	if len(raw) == 0 {
		return nil
	}
	usage := &MaheshvaraUsage{
		InputTokens:       intValue(firstNonValue(raw, usageAliasTables.input...)),
		OutputTokens:      intValue(firstNonValue(raw, usageAliasTables.output...)),
		TotalTokens:       intValue(firstNonValue(raw, usageAliasTables.total...)),
		CachedInputTokens: intValue(firstNonValue(raw, usageAliasTables.cached...)),
		ReasoningTokens:   intValue(firstNonValue(raw, usageAliasTables.reason...)),
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
