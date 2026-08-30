package relay

import (
	"testing"
)

// R9 回归：temperature=0 是「确定性输出」的合法值，不能被当作未设置而丢弃。
// 经 Maheshvara maheshvara 管线验证同一语义（旧 Unified 管线已随死代码移除）。

func TestClaudeTemperatureZeroPreserved(t *testing.T) {
	body := []byte(`{"model":"m","max_tokens":10,"temperature":0,"top_p":0,"messages":[{"role":"user","content":"hi"}]}`)
	req, err := AnthropicToMaheshvara(body)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if req.Temperature == nil || *req.Temperature != 0 {
		t.Fatalf("temperature=0 should be preserved, got %v", req.Temperature)
	}
	if req.TopP == nil || *req.TopP != 0 {
		t.Fatalf("top_p=0 should be preserved, got %v", req.TopP)
	}
}

func TestClaudeTemperatureUnsetStaysNil(t *testing.T) {
	body := []byte(`{"model":"m","max_tokens":10,"messages":[{"role":"user","content":"hi"}]}`)
	req, err := AnthropicToMaheshvara(body)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if req.Temperature != nil {
		t.Fatalf("unset temperature should stay nil, got %v", *req.Temperature)
	}
}

func TestGeminiTemperatureZeroPreserved(t *testing.T) {
	body := []byte(`{"model":"m","generationConfig":{"temperature":0,"topP":0},"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`)
	req, err := GeminiToMaheshvara(body, "")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if req.Temperature == nil || *req.Temperature != 0 {
		t.Fatalf("temperature=0 should be preserved, got %v", req.Temperature)
	}
	if req.TopP == nil || *req.TopP != 0 {
		t.Fatalf("top_p=0 should be preserved, got %v", req.TopP)
	}
}

func TestGeminiSafetyMapsToContentFilter(t *testing.T) {
	const body = `{"model":"m","contents":[{"role":"user","parts":[{"text":"hi"}]}],"generationConfig":{"stopSequences":["x"]}}`
	if _, err := GeminiToMaheshvara([]byte(body), ""); err != nil {
		t.Fatalf("parse: %v", err)
	}
	// SAFETY→content_filter 的映射在响应方向：maheshvaraStopToOpenAI("SAFETY")。
	if got := maheshvaraStopToOpenAI("SAFETY"); got != "content_filter" {
		t.Fatalf("SAFETY must map to content_filter, got %q", got)
	}
}

func TestClaudeRefusalMapsToContentFilter(t *testing.T) {
	if got := maheshvaraStopToOpenAI("refusal"); got != "content_filter" {
		t.Fatalf("refusal must map to content_filter, got %q", got)
	}
}

func TestGeminiPartsAccumulateWithText(t *testing.T) {
	body := []byte(`{"model":"m","contents":[{"role":"user","parts":[{"text":"a"},{"text":"b"}]}]}`)
	req, err := GeminiToMaheshvara(body, "")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(req.Messages) != 1 || len(req.Messages[0].Content) != 2 {
		t.Fatalf("expected 2 text parts, got %+v", req.Messages)
	}
}

func TestGeminiFunctionCallConverted(t *testing.T) {
	body := []byte(`{"model":"m","contents":[{"role":"user","parts":[{"text":"q"}]},{"role":"model","parts":[{"functionCall":{"name":"f","args":{"a":1}}}]}]}`)
	req, err := GeminiToMaheshvara(body, "")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	found := false
	for _, msg := range req.Messages {
		if len(msg.ToolCalls) > 0 && msg.ToolCalls[0].Name == "f" {
			found = true
		}
	}
	if !found {
		t.Fatalf("functionCall part not converted to a tool call: %+v", req.Messages)
	}
}
