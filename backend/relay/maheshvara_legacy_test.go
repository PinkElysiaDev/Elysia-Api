package relay

import (
	"encoding/json"
	"strings"
	"testing"
)

// 批次六回归：遗留兼容——legacy function calling 双向、system_fingerprint。

// 遗留 function calling 完整生命周期：顶层 functions → 旧形态回发；
// assistant function_call + role:"function" 结果消息按旧形态还原。
func TestLegacyFunctionCallingRoundTrip(t *testing.T) {
	req, err := OpenAIChatToMaheshvara([]byte(`{"model":"grp",` +
		`"functions":[{"name":"get_weather","description":"查天气","parameters":{"type":"object","properties":{"city":{"type":"string"}}}}],` +
		`"messages":[{"role":"user","content":"北京天气"},` +
		`{"role":"assistant","content":null,"function_call":{"name":"get_weather","arguments":"{\"city\":\"北京\"}"}},` +
		`{"role":"function","name":"get_weather","content":"晴"}]}`))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(req.Tools) != 1 || !isLegacyFunctionTool(req.Tools[0]) || req.Tools[0].Name != "get_weather" {
		t.Fatalf("legacy functions not decoded: %+v", req.Tools)
	}
	target, err := MaheshvaraToOpenAIChat(req)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(target, &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	functions, _ := payload["functions"].([]any)
	if len(functions) != 1 {
		t.Fatalf("functions not replayed in legacy shape: %s", target)
	}
	encoded := string(target)
	if !strings.Contains(encoded, `"function_call":{`) || !strings.Contains(encoded, `"name":"get_weather"`) {
		t.Fatalf("assistant function_call not replayed: %s", target)
	}
	if !strings.Contains(encoded, `"role":"function"`) || !strings.Contains(encoded, `"name":"get_weather"`) {
		t.Fatalf("function result message not replayed in legacy shape: %s", target)
	}
	if strings.Contains(encoded, `"tool_calls"`) || strings.Contains(encoded, `"role":"tool"`) {
		t.Fatalf("legacy lifecycle must not degrade into modern tool_calls form: %s", target)
	}

	// 跨协议目标：遗留工具按现代工具正常转换（Claude 目标可用）。
	claudeTarget, err := MaheshvaraToAnthropic(req)
	if err != nil {
		t.Fatalf("render claude: %v", err)
	}
	if !strings.Contains(string(claudeTarget), "get_weather") {
		t.Fatalf("legacy tool must convert for cross-protocol targets: %s", claudeTarget)
	}
}

// system_fingerprint 往返（此前是声明即死的字段）。
func TestSystemFingerprintRoundTrip(t *testing.T) {
	var resp OpenAIResponse
	body := `{"id":"c","object":"chat.completion","created":1,"model":"m","system_fingerprint":"fp-123",` +
		`"choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],` +
		`"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	canonical, err := OpenAIChatResponseToMaheshvara(&resp)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if canonical.SystemFingerprint != "fp-123" {
		t.Fatalf("system_fingerprint not parsed: %+v", canonical.SystemFingerprint)
	}
	rendered, err := MaheshvaraToOpenAIChatResponse(canonical)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if rendered.SystemFingerprint != "fp-123" {
		t.Fatalf("system_fingerprint not written back: %+v", rendered.SystemFingerprint)
	}
}
