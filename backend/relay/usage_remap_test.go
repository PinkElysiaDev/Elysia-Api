package relay

import "testing"

// codex 严格要求 response.completed.usage 含整数 input_tokens/output_tokens/total_tokens。
// 这些测试锁死 usage 重映射契约，防止再回归到直接透传 prompt_tokens 导致 1 秒断连。

func TestChatUsageToResponsesUsageRemapsChatShape(t *testing.T) {
	raw := map[string]any{
		"prompt_tokens":     float64(12),
		"completion_tokens": float64(8),
		"total_tokens":      float64(20),
	}
	out := chatUsageToResponsesUsage(raw)

	if got := out["input_tokens"]; got != int64(12) {
		t.Fatalf("input_tokens = %v, want 12", got)
	}
	if got := out["output_tokens"]; got != int64(8) {
		t.Fatalf("output_tokens = %v, want 8", got)
	}
	if got := out["total_tokens"]; got != int64(20) {
		t.Fatalf("total_tokens = %v, want 20", got)
	}
	// 不应再出现 chat 命名字段。
	if _, ok := out["prompt_tokens"]; ok {
		t.Fatalf("output must not contain prompt_tokens")
	}
}

func TestChatUsageToResponsesUsageNilReturnsZeroedStruct(t *testing.T) {
	out := chatUsageToResponsesUsage(nil)
	for _, k := range []string{"input_tokens", "output_tokens", "total_tokens"} {
		v, ok := out[k]
		if !ok {
			t.Fatalf("missing required field %q", k)
		}
		if v != int64(0) {
			t.Fatalf("%s = %v, want 0", k, v)
		}
	}
	if _, ok := out["output_tokens_details"]; !ok {
		t.Fatalf("missing output_tokens_details")
	}
}

func TestChatUsageToResponsesUsageComputesTotalWhenMissing(t *testing.T) {
	raw := map[string]any{
		"prompt_tokens":     float64(5),
		"completion_tokens": float64(7),
	}
	out := chatUsageToResponsesUsage(raw)
	if got := out["total_tokens"]; got != int64(12) {
		t.Fatalf("total_tokens = %v, want 12 (computed)", got)
	}
}

func TestChatUsageToResponsesUsageGeminiShape(t *testing.T) {
	raw := map[string]any{
		"promptTokenCount":     float64(3),
		"candidatesTokenCount": float64(4),
		"totalTokenCount":      float64(7),
	}
	out := chatUsageToResponsesUsage(raw)
	if out["input_tokens"] != int64(3) || out["output_tokens"] != int64(4) || out["total_tokens"] != int64(7) {
		t.Fatalf("gemini remap wrong: %+v", out)
	}
}
