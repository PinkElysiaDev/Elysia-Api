package server

import (
	"encoding/json"
	"strings"

	"github.com/elysia-api/backend/config"
	"github.com/elysia-api/backend/relay"
)

func usageTokenUsageFromMaheshvara(u *relay.MaheshvaraUsage) usageTokenUsage {
	if u == nil {
		return usageTokenUsage{}
	}
	usage := usageTokenUsage{}
	if u.Estimated && (u.Source == "" || strings.Contains(u.Source, "estimate")) {
		if u.EstimatedTotalTokens > 0 {
			usage.EstimatedTokens = u.EstimatedTotalTokens
		}
		usage.Estimated = true
		return usage
	}
	if u.InputTokens > 0 {
		usage.InputTokens = intPtr(u.InputTokens)
	}
	if u.OutputTokens > 0 {
		usage.OutputTokens = intPtr(u.OutputTokens)
	}
	total := u.TotalTokens
	if total == 0 && (u.InputTokens > 0 || u.OutputTokens > 0) {
		total = u.InputTokens + u.OutputTokens
	}
	if total > 0 {
		usage.TotalTokens = intPtr(total)
	}
	if u.CachedInputTokens > 0 {
		usage.CacheHitTokens = intPtr(u.CachedInputTokens)
	}
	if u.EstimatedTotalTokens > 0 {
		usage.EstimatedTokens = u.EstimatedTotalTokens
	}
	usage.Estimated = u.Estimated
	return usage
}

func usageDetailFromMaheshvara(u *relay.MaheshvaraUsage) usageDetail {
	if u == nil {
		return usageDetail{}
	}
	detail := usageDetail{Estimated: u.Estimated}
	if u.Estimated && (u.Source == "" || strings.Contains(u.Source, "estimate")) {
		return detail
	}
	if u.InputTokens > 0 {
		detail.InputTokens = intPtr(u.InputTokens)
	}
	if u.OutputTokens > 0 {
		detail.OutputTokens = intPtr(u.OutputTokens)
	}
	total := u.TotalTokens
	if total == 0 && (u.InputTokens > 0 || u.OutputTokens > 0) {
		total = u.InputTokens + u.OutputTokens
	}
	if total > 0 {
		detail.TotalTokens = intPtr(total)
	}
	if u.CachedInputTokens > 0 {
		detail.CachedInputTokens = intPtr(u.CachedInputTokens)
	}
	if u.CacheCreationInputTokens > 0 {
		detail.CacheCreationInputTokens = intPtr(u.CacheCreationInputTokens)
	}
	if u.ReasoningTokens > 0 {
		detail.ReasoningTokens = intPtr(u.ReasoningTokens)
	}
	if u.TextInputTokens > 0 {
		detail.TextInputTokens = intPtr(u.TextInputTokens)
	}
	if u.TextOutputTokens > 0 {
		detail.TextOutputTokens = intPtr(u.TextOutputTokens)
	}
	if u.ImageInputTokens > 0 {
		detail.ImageInputTokens = intPtr(u.ImageInputTokens)
	}
	if u.ImageOutputTokens > 0 {
		detail.ImageOutputTokens = intPtr(u.ImageOutputTokens)
	}
	if u.AudioInputTokens > 0 {
		detail.AudioInputTokens = intPtr(u.AudioInputTokens)
	}
	if u.AudioOutputTokens > 0 {
		detail.AudioOutputTokens = intPtr(u.AudioOutputTokens)
	}
	if u.ToolUseTokens > 0 {
		detail.ToolUseTokens = intPtr(u.ToolUseTokens)
	}
	return detail
}

func builtinToolUsageFromMaheshvara(u *relay.MaheshvaraUsage) builtinToolUsage {
	if u == nil {
		return builtinToolUsage{}
	}
	return builtinToolUsage{
		WebSearchCalls:       u.WebSearchCallCount,
		FileSearchCalls:      u.FileSearchCallCount,
		ImageGenerationCalls: u.ImageGenerationCallCount,
		CodeInterpreterCalls: u.CodeInterpreterCallCount,
		ComputerUseCalls:     u.ComputerUseCallCount,
	}
}

func updateRecordUsageFromMaheshvara(record *usageRecord, usage *relay.MaheshvaraUsage) {
	if record == nil || usage == nil {
		return
	}
	record.Usage = mergeUsage(record.Usage, usageTokenUsageFromMaheshvara(usage))
	record.UsageDetail = usageDetailFromMaheshvara(usage)
	record.BuiltinToolUsage = builtinToolUsageFromMaheshvara(usage)
	if usage.Source != "" {
		record.UsageSource = usage.Source
	}
}

func estimateMaheshvaraRequestUsage(req *relay.MaheshvaraRequest, cfg config.UsageConfig) *relay.MaheshvaraUsage {
	if req == nil {
		return &relay.MaheshvaraUsage{Estimated: true, Source: "maheshvara_estimate"}
	}

	charsPerToken := cfg.CharsPerToken
	if charsPerToken <= 0 {
		charsPerToken = DefaultCharsPerToken
	}

	textChars := len([]rune(req.Instructions))
	imageTokens := 0
	fileTokens := 0

	addParts := func(parts []relay.MaheshvaraContentPart) {
		for _, part := range parts {
			switch part.Type {
			case relay.MaheshvaraContentText:
				textChars += len([]rune(part.Text))
			case relay.MaheshvaraContentReasoning:
				textChars += len([]rune(part.ReasoningText))
			case relay.MaheshvaraContentImage:
				imageTokens += cfg.ImageInputTokenEstimate
			case relay.MaheshvaraContentFile:
				if part.FileData != "" {
					fileTokens += (len(part.FileData)/1024 + 1) * cfg.FileInputTokenEstimatePerKB
				} else {
					fileTokens += cfg.FileInputTokenEstimatePerKB
				}
			}
		}
	}

	for _, msg := range req.Messages {
		addParts(msg.Content)
		for _, call := range msg.ToolCalls {
			textChars += len(call.Name) + len(call.Arguments)
		}
	}
	for _, item := range req.InputItems {
		addParts(item.Content)
		textChars += len([]rune(item.Output))
	}
	for _, tool := range req.Tools {
		if raw, err := json.Marshal(tool); err == nil {
			textChars += len(raw)
		}
	}
	if req.ResponseFormat != nil {
		if raw, err := json.Marshal(req.ResponseFormat); err == nil {
			textChars += len(raw)
		}
	}

	textTokens := (textChars + charsPerToken - 1) / charsPerToken
	inputTokens := textTokens + imageTokens + fileTokens
	outputTokens := req.MaxOutputTokens
	if outputTokens <= 0 {
		outputTokens = cfg.DefaultOutputTokenEstimate
	}

	return &relay.MaheshvaraUsage{
		InputTokens:              inputTokens,
		OutputTokens:             outputTokens,
		TotalTokens:              inputTokens + outputTokens,
		TextInputTokens:          textTokens,
		ImageInputTokens:         imageTokens,
		EstimatedInputTokens:     inputTokens,
		EstimatedOutputTokens:    outputTokens,
		EstimatedTotalTokens:     inputTokens + outputTokens,
		Estimated:                true,
		Source:                   "maheshvara_estimate",
		FileSearchCallCount:      countRequestBuiltinTool(req, relay.MaheshvaraToolFileSearch),
		WebSearchCallCount:       countRequestBuiltinTool(req, relay.MaheshvaraToolWebSearchPreview),
		ImageGenerationCallCount: countRequestBuiltinTool(req, relay.MaheshvaraToolImageGeneration),
	}
}

func countRequestBuiltinTool(req *relay.MaheshvaraRequest, toolType string) int {
	count := 0
	for _, tool := range req.Tools {
		if strings.EqualFold(tool.Type, toolType) {
			count++
		}
	}
	return count
}
