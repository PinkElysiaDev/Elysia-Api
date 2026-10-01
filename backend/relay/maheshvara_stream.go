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
	usage := customUsageAtWithAliases(map[string]any{"usage": raw}, "usage", nil)
	usage.Source = "provider_stream"
	// 原始 usage 对象整体留存：同线渲染时未知计数键原样透传（XF5b：不重释、
	// 不丢弃），已知键由类型化字段覆盖。
	usage.Raw = raw
	return usage
}
