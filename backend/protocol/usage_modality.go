package protocol

import (
	"math"
	"strings"
)

// ModalityUsageDetail identifies token breakdowns, never additional totals.
func ModalityUsageDetail(name string) (bucket, modality string) {
	bucket, name, _ = strings.Cut(name, ".")
	switch bucket {
	case "input", "output", "cache", "tool_input":
	default:
		return "", ""
	}
	switch name {
	case "text_tokens", "image_tokens", "audio_tokens", "video_tokens", "document_tokens":
		return bucket, strings.ToUpper(strings.TrimSuffix(name, "_tokens"))
	}
	return "", ""
}

func SupportsModalityUsage(codec, bucket, modality string) bool {
	return codec == "gemini" || (codec == "openai-chat" || codec == "responses") &&
		(bucket == "input" || bucket == "output") && (modality == "TEXT" || modality == "IMAGE" || modality == "AUDIO")
}

func validateModalityUsage(usage *Usage) error {
	remaining := map[string]int64{}
	toolInput, hasToolInput := usage.Details["toolUsePromptTokenCount"]
	for bucket, total := range map[string]*Counter{"input": usage.Input, "output": usage.Output, "cache": usage.CacheRead, "tool_input": nil} {
		remaining[bucket] = math.MaxInt64
		if total != nil {
			remaining[bucket] = total.Count
		}
	}
	if hasToolInput {
		remaining["tool_input"] = toolInput.Count
	}
	for _, name := range sortedKeys(usage.Details) {
		bucket, _ := ModalityUsageDetail(name)
		if bucket == "" {
			continue
		}
		count := usage.Details[name].Count
		if count < 0 || count > remaining[bucket] {
			return streamIssue(InvalidInput, "/usage/details/"+name, "modality breakdown exceeds its total or supported integer range")
		}
		remaining[bucket] -= count
	}
	return nil
}
