// Maheshvara 用量结构与各线制 usage 字段的双向换算。
package relay

import "strings"

func maheshvaraUsageFromOpenAIUsage(usage Usage) *MaheshvaraUsage {
	var promptDetails PromptTokensDetails
	if usage.PromptTokensDetails != nil {
		promptDetails = *usage.PromptTokensDetails
	}
	if usage.InputTokensDetails != nil && (usage.InputTokensDetails.CachedTokens > 0 || usage.InputTokensDetails.TextTokens > 0) {
		promptDetails = *usage.InputTokensDetails
	}
	var completionDetails CompletionTokensDetails
	if usage.CompletionTokensDetails != nil {
		completionDetails = *usage.CompletionTokensDetails
	}
	u := &MaheshvaraUsage{
		InputTokens:              usage.PromptTokens,
		OutputTokens:             usage.CompletionTokens,
		TotalTokens:              usage.TotalTokens,
		CachedInputTokens:        max(usage.CachedTokens, usage.PromptCacheHitTokens),
		ReasoningTokens:          completionDetails.ReasoningTokens,
		AcceptedPredictionTokens: completionDetails.AcceptedPredictionTokens,
		RejectedPredictionTokens: completionDetails.RejectedPredictionTokens,
		TextInputTokens:          promptDetails.TextTokens,
		AudioInputTokens:         promptDetails.AudioTokens,
		ImageInputTokens:         promptDetails.ImageTokens,
		TextOutputTokens:         completionDetails.TextTokens,
		AudioOutputTokens:        completionDetails.AudioTokens,
		ImageOutputTokens:        completionDetails.ImageTokens,
		Raw:                      usage.RawFields,
		Source:                   UsageSourceProviderResponse,
	}
	if u.CachedInputTokens == 0 {
		u.CachedInputTokens = max(promptDetails.CachedTokens, promptDetails.CacheReadTokens)
	}
	if u.CacheCreationInputTokens == 0 {
		u.CacheCreationInputTokens = promptDetails.CachedCreationTokens
	}
	u.TotalTokens = valueOrSum(u.TotalTokens, u.InputTokens, u.OutputTokens)
	return u
}

func maheshvaraUsageFromClaudeUsage(usage ClaudeUsage) *MaheshvaraUsage {
	input := usage.InputTokens + usage.CacheReadInputTokens + usage.CacheCreationInputTokens
	if usage.CacheCreationInputTokens == 0 && usage.CacheCreation != nil {
		// Anthropic 官方响应里 cache_creation.ephemeral_* 是 cache_creation_input_tokens
		// 的明细拆分，两者同时返回且相等；只在总数缺失时才用明细求和，避免双重计入。
		input += usage.CacheCreation.Ephemeral5mInputTokens + usage.CacheCreation.Ephemeral1hInputTokens
	}
	u := &MaheshvaraUsage{
		InputTokens:              input,
		OutputTokens:             usage.OutputTokens,
		TotalTokens:              input + usage.OutputTokens,
		CachedInputTokens:        usage.CacheReadInputTokens,
		CacheCreationInputTokens: usage.CacheCreationInputTokens,
		Source:                   UsageSourceProviderResponse,
	}
	if usage.CacheCreation != nil {
		// 双 TTL 桶明细保真（ephemeral_5m / ephemeral_1h）。
		u.CacheCreation5mTokens = usage.CacheCreation.Ephemeral5mInputTokens
		u.CacheCreation1hTokens = usage.CacheCreation.Ephemeral1hInputTokens
	}
	if usage.ServerToolUse != nil {
		u.WebSearchCallCount = usage.ServerToolUse.WebSearchRequests
	}
	return u
}

func maheshvaraUsageFromGeminiUsage(usage GeminiUsageMeta) *MaheshvaraUsage {
	u := &MaheshvaraUsage{
		InputTokens:       usage.PromptTokenCount + usage.ToolUsePromptTokenCount,
		OutputTokens:      usage.CandidatesTokenCount + usage.ThoughtsTokenCount,
		TotalTokens:       usage.TotalTokenCount,
		CachedInputTokens: usage.CachedContentTokenCount,
		ReasoningTokens:   usage.ThoughtsTokenCount,
		ToolUseTokens:     usage.ToolUsePromptTokenCount,
		Source:            UsageSourceProviderResponse,
	}
	u.TotalTokens = valueOrSum(u.TotalTokens, u.InputTokens, u.OutputTokens)
	for _, detail := range usage.PromptTokensDetails {
		switch strings.ToUpper(detail.Modality) {
		case "TEXT":
			u.TextInputTokens += detail.TokenCount
		case "IMAGE":
			u.ImageInputTokens += detail.TokenCount
		case "AUDIO":
			u.AudioInputTokens += detail.TokenCount
		}
	}
	for _, detail := range usage.CandidatesTokensDetails {
		switch strings.ToUpper(detail.Modality) {
		case "TEXT":
			u.TextOutputTokens += detail.TokenCount
		case "IMAGE":
			u.ImageOutputTokens += detail.TokenCount
		case "AUDIO":
			u.AudioOutputTokens += detail.TokenCount
		}
	}
	return u
}

func maheshvaraUsageFromResponsesUsage(usage *ResponsesUsage) *MaheshvaraUsage {
	if usage == nil {
		return nil
	}
	u := &MaheshvaraUsage{
		InputTokens:  usage.InputTokens,
		OutputTokens: usage.OutputTokens,
		TotalTokens:  usage.TotalTokens,
		Source:       UsageSourceProviderResponse,
	}
	if usage.InputTokensDetails != nil {
		u.CachedInputTokens = usage.InputTokensDetails.CachedTokens
	}
	if usage.OutputTokensDetails != nil {
		u.ReasoningTokens = usage.OutputTokensDetails.ReasoningTokens
	}
	u.TotalTokens = valueOrSum(u.TotalTokens, u.InputTokens, u.OutputTokens)
	return u
}

func openAIUsageFromMaheshvara(u *MaheshvaraUsage) Usage {
	if u == nil {
		return Usage{}
	}
	// details 仅在有值时输出（指针 omitempty）——空对象会覆盖 RawFields
	// 透传的同键子对象。
	var promptDetails *PromptTokensDetails
	if u.CachedInputTokens > 0 || u.TextInputTokens > 0 || u.AudioInputTokens > 0 || u.ImageInputTokens > 0 {
		promptDetails = &PromptTokensDetails{
			CachedTokens: u.CachedInputTokens,
			TextTokens:   u.TextInputTokens,
			AudioTokens:  u.AudioInputTokens,
			ImageTokens:  u.ImageInputTokens,
		}
	}
	var completionDetails *CompletionTokensDetails
	if u.ReasoningTokens > 0 || u.TextOutputTokens > 0 || u.AudioOutputTokens > 0 || u.ImageOutputTokens > 0 || u.AcceptedPredictionTokens > 0 || u.RejectedPredictionTokens > 0 {
		completionDetails = &CompletionTokensDetails{
			ReasoningTokens:          u.ReasoningTokens,
			TextTokens:               u.TextOutputTokens,
			AudioTokens:              u.AudioOutputTokens,
			ImageTokens:              u.ImageOutputTokens,
			AcceptedPredictionTokens: u.AcceptedPredictionTokens,
			RejectedPredictionTokens: u.RejectedPredictionTokens,
		}
	}
	return Usage{
		PromptTokens:            u.InputTokens,
		CompletionTokens:        u.OutputTokens,
		TotalTokens:             valueOrSum(u.TotalTokens, u.InputTokens, u.OutputTokens),
		CachedTokens:            u.CachedInputTokens,
		PromptTokensDetails:     promptDetails,
		CompletionTokensDetails: completionDetails,
		// 上游新增的未知计数键原样透传（原始对象为底，类型化字段覆盖）。
		RawFields: u.Raw,
	}
}

func claudeUsageFromMaheshvara(u *MaheshvaraUsage) ClaudeUsage {
	if u == nil {
		return ClaudeUsage{}
	}
	// Claude 协议中 input_tokens 不含缓存 token（缓存读/写是独立字段），而 maheshvara
	// 的 InputTokens 是含缓存的总数；还原时剔除缓存部分，避免 Claude→maheshvara→Claude
	// 往返后 input_tokens 与 cache 字段重复计入。
	input := u.InputTokens - u.CachedInputTokens - u.CacheCreationInputTokens
	if input < 0 {
		input = u.InputTokens
	}
	usage := ClaudeUsage{
		InputTokens:              input,
		OutputTokens:             u.OutputTokens,
		CacheReadInputTokens:     u.CachedInputTokens,
		CacheCreationInputTokens: u.CacheCreationInputTokens,
	}
	if u.CacheCreation5mTokens > 0 || u.CacheCreation1hTokens > 0 {
		// 双 TTL 桶明细回写（仅在有明细时输出，避免空对象）。
		usage.CacheCreation = &ClaudeCacheCreationUsage{
			Ephemeral5mInputTokens: u.CacheCreation5mTokens,
			Ephemeral1hInputTokens: u.CacheCreation1hTokens,
		}
	}
	if u.WebSearchCallCount > 0 {
		usage.ServerToolUse = &ClaudeServerToolUse{WebSearchRequests: u.WebSearchCallCount}
	}
	return usage
}

func geminiUsageFromMaheshvara(u *MaheshvaraUsage) GeminiUsageMeta {
	if u == nil {
		return GeminiUsageMeta{}
	}
	// maheshvara 的 Input/Output 是含 tool/thought 分量的总数；Gemini 各计数器
	// 是独立分项，还原时剔除已单列的分量，避免往返双计（对齐 Claude 缓存减法）。
	promptTokens := u.InputTokens - u.ToolUseTokens
	if promptTokens < 0 {
		promptTokens = u.InputTokens
	}
	candidateTokens := u.OutputTokens - u.ReasoningTokens
	if candidateTokens < 0 {
		candidateTokens = u.OutputTokens
	}
	meta := GeminiUsageMeta{
		PromptTokenCount:        promptTokens,
		ToolUsePromptTokenCount: u.ToolUseTokens,
		CandidatesTokenCount:    candidateTokens,
		TotalTokenCount:         valueOrSum(u.TotalTokens, u.InputTokens, u.OutputTokens),
		ThoughtsTokenCount:      u.ReasoningTokens,
		CachedContentTokenCount: u.CachedInputTokens,
	}
	// 模态明细回写（有值才输出，保持 usageMetadata 紧凑）。
	appendModality := func(target *[]GeminiTokenDetail, modality string, count int) {
		if count > 0 {
			*target = append(*target, GeminiTokenDetail{Modality: modality, TokenCount: count})
		}
	}
	appendModality(&meta.PromptTokensDetails, "TEXT", u.TextInputTokens)
	appendModality(&meta.PromptTokensDetails, "IMAGE", u.ImageInputTokens)
	appendModality(&meta.PromptTokensDetails, "AUDIO", u.AudioInputTokens)
	appendModality(&meta.CandidatesTokensDetails, "TEXT", u.TextOutputTokens)
	appendModality(&meta.CandidatesTokensDetails, "IMAGE", u.ImageOutputTokens)
	appendModality(&meta.CandidatesTokensDetails, "AUDIO", u.AudioOutputTokens)
	return meta
}

func responsesUsageFromMaheshvara(u *MaheshvaraUsage) *ResponsesUsage {
	if u == nil {
		return nil
	}
	out := &ResponsesUsage{
		InputTokens:  u.InputTokens,
		OutputTokens: u.OutputTokens,
		TotalTokens:  valueOrSum(u.TotalTokens, u.InputTokens, u.OutputTokens),
	}
	if u.CachedInputTokens > 0 {
		out.InputTokensDetails = &ResponsesInputTokensDetails{CachedTokens: u.CachedInputTokens}
	}
	if u.ReasoningTokens > 0 {
		out.OutputTokensDetails = &ResponsesOutputTokensDetails{ReasoningTokens: u.ReasoningTokens}
	}
	return out
}

// nonEmptyJSONArgs 保证 tool call 的 arguments 非空:空串以 "{}" 兜底,
// 避免严格上游(要求 arguments 是合法 JSON 对象)直接拒绝整个请求。
func nonEmptyJSONArgs(args string) string {
	if args == "" {
		return "{}"
	}
	return args
}

func valueOrSum(total, input, output int) int {
	if total > 0 {
		return total
	}
	return input + output
}
