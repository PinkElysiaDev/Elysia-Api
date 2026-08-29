package relay

import (
	"context"
	"encoding/json"
	"net/http"
)

// chatUsageToResponsesUsage 把上游（Chat Completions / Claude / Gemini）的 usage
// 重映射成 OpenAI Responses API 要求的结构。这是 codex "stream closed before
// response.completed" 1 秒断连的根因修复：codex 严格要求 response.completed.usage
// 一旦存在，就必须含整数 input_tokens / output_tokens / total_tokens；而 Chat
// Completions 上游给的是 prompt_tokens / completion_tokens，字段名不匹配会让
// codex 反序列化 ResponseCompletedUsage 失败 → 整个 ResponseCompleted 解析失败 →
// 立即断连。借鉴 cc-switch 的 chat_usage_to_responses_usage。
//
// 入参 raw 可为 nil（上游未返回 usage）：返回全 0 但结构完整的对象，绝不返回 nil
// 或缺字段，避免 codex 解析失败。
func chatUsageToResponsesUsage(raw any) map[string]any {
	usage, _ := raw.(map[string]any)

	pick := func(keys ...string) (int64, bool) {
		for _, k := range keys {
			if v, ok := usage[k]; ok {
				switch n := v.(type) {
				case float64:
					return int64(n), true
				case int64:
					return n, true
				case int:
					return int64(n), true
				case json.Number:
					if iv, err := n.Int64(); err == nil {
						return iv, true
					}
				}
			}
		}
		return 0, false
	}

	// prompt_tokens → input_tokens（已是 responses 命名也兼容）。
	inputTokens, _ := pick("prompt_tokens", "input_tokens", "promptTokenCount")
	// completion_tokens → output_tokens。
	outputTokens, _ := pick("completion_tokens", "output_tokens", "candidatesTokenCount")
	totalTokens, hasTotal := pick("total_tokens", "totalTokenCount")
	if !hasTotal {
		totalTokens = inputTokens + outputTokens
	}

	// reasoning tokens（若上游提供）放进 output_tokens_details；始终保证该字段存在。
	reasoningTokens := int64(0)
	if details, ok := usage["completion_tokens_details"].(map[string]any); ok {
		if v, ok := details["reasoning_tokens"]; ok {
			switch n := v.(type) {
			case float64:
				reasoningTokens = int64(n)
			case json.Number:
				if iv, err := n.Int64(); err == nil {
					reasoningTokens = iv
				}
			}
		}
	}

	result := map[string]any{
		"input_tokens":  inputTokens,
		"output_tokens": outputTokens,
		"total_tokens":  totalTokens,
		"output_tokens_details": map[string]any{
			"reasoning_tokens": reasoningTokens,
		},
	}

	// 缓存命中：prompt_tokens_details.cached_tokens → input_tokens_details.cached_tokens。
	if details, ok := usage["prompt_tokens_details"].(map[string]any); ok {
		if v, ok := details["cached_tokens"]; ok {
			result["input_tokens_details"] = map[string]any{"cached_tokens": v}
		}
	}

	return result
}

func ForwardResponsesStream(ctx context.Context, resp *http.Response, writer StreamResponseWriter) error {
	return forwardSSELines(ctx, resp, writer, true)
}
