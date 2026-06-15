package relay

import (
	"encoding/json"
	"testing"
)

// TestResponsesPassthroughPreservesRichInput 验证 Responses 透传：codex 发来的
// 复杂 input（含 reasoning + encrypted_content、function_call item）以及未知字段
// 必须原样保留，只有 model 被改写为模型组路由选中的上游模型名。这是修复
// 「Responses 直选透传」零出错的核心契约——任何对 input 的重建都可能让上游严格
// 校验失败、导致 1 秒断连。
func TestResponsesPassthroughPreservesRichInput(t *testing.T) {
	original := `{
		"model": "gpt-5-codex",
		"stream": true,
		"input": [
			{"type": "message", "role": "user", "content": [{"type": "input_text", "text": "hi"}]},
			{"type": "reasoning", "id": "rs_1", "encrypted_content": "ENCRYPTED_BLOB", "summary": []},
			{"type": "function_call", "call_id": "call_1", "name": "shell", "arguments": "{\"cmd\":\"ls\"}"}
		],
		"tools": [{"type": "function", "name": "shell", "parameters": {"type": "object"}}],
		"reasoning": {"effort": "high", "summary": "auto"},
		"store": false,
		"prompt_cache_key": "abc",
		"some_unknown_codex_field": {"nested": [1, 2, 3]}
	}`

	out, err := ResponsesPassthroughBody([]byte(original), "real-upstream-model")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("output not valid json: %v", err)
	}

	// model 必须被改写为上游路由选中的名字。
	if got["model"] != "real-upstream-model" {
		t.Fatalf("expected model rewritten to real-upstream-model, got %v", got["model"])
	}

	// 未知字段必须原样保留（struct 往返会丢，passthrough 不能丢）。
	if _, ok := got["some_unknown_codex_field"]; !ok {
		t.Fatal("unknown field some_unknown_codex_field was dropped — passthrough is lossy")
	}
	if got["prompt_cache_key"] != "abc" {
		t.Fatalf("prompt_cache_key not preserved, got %v", got["prompt_cache_key"])
	}

	// input 数组必须逐项保留，尤其 reasoning 的 encrypted_content 和 function_call。
	input, ok := got["input"].([]any)
	if !ok || len(input) != 3 {
		t.Fatalf("input array not preserved verbatim, got %v", got["input"])
	}
	reasoning, _ := input[1].(map[string]any)
	if reasoning["encrypted_content"] != "ENCRYPTED_BLOB" {
		t.Fatalf("reasoning encrypted_content lost, got %v", reasoning)
	}
	fc, _ := input[2].(map[string]any)
	if fc["type"] != "function_call" || fc["call_id"] != "call_1" {
		t.Fatalf("function_call item lost/altered, got %v", fc)
	}
}

// TestResponsesPassthroughEmptyModelKeepsOriginal 验证 modelName 为空时不改写 model。
func TestResponsesPassthroughEmptyModelKeepsOriginal(t *testing.T) {
	out, err := ResponsesPassthroughBody([]byte(`{"model":"orig","input":[]}`), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var got map[string]any
	_ = json.Unmarshal(out, &got)
	if got["model"] != "orig" {
		t.Fatalf("expected original model preserved when modelName empty, got %v", got["model"])
	}
}
