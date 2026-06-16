package relay

import (
	"encoding/json"
	"strings"
	"testing"
)

// R9 回归：temperature=0 是「确定性输出」的合法值，不能被当作未设置而丢弃。
// 旧实现把 temperature/top_p 解码为 float64 并用 `> 0` 守卫，导致显式的 0 被吞。

func TestClaudeTemperatureZeroPreserved(t *testing.T) {
	body := []byte(`{"model":"m","max_tokens":10,"temperature":0,"top_p":0,"messages":[{"role":"user","content":"hi"}]}`)
	req, err := ClaudeToUnified(body)
	if err != nil {
		t.Fatalf("ClaudeToUnified: %v", err)
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
	req, err := ClaudeToUnified(body)
	if err != nil {
		t.Fatalf("ClaudeToUnified: %v", err)
	}
	if req.Temperature != nil {
		t.Fatalf("unset temperature should stay nil, got %v", *req.Temperature)
	}
}

func TestGeminiTemperatureZeroPreserved(t *testing.T) {
	body := []byte(`{"model":"m","generationConfig":{"temperature":0,"topP":0},"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`)
	req, err := GeminiToUnified(body)
	if err != nil {
		t.Fatalf("GeminiToUnified: %v", err)
	}
	if req.Temperature == nil || *req.Temperature != 0 {
		t.Fatalf("temperature=0 should be preserved, got %v", req.Temperature)
	}
	if req.TopP == nil || *req.TopP != 0 {
		t.Fatalf("top_p=0 should be preserved, got %v", req.TopP)
	}
}

// R13 回归：被安全策略拦截的响应应映射为 content_filter，而非塌缩为 stop，
// 让调用方能区分「正常结束」与「被拦截/拒答」。

func TestGeminiSafetyMapsToContentFilter(t *testing.T) {
	for _, reason := range []string{"SAFETY", "RECITATION"} {
		if got := geminiFinishReasonToOpenAI(reason); got != "content_filter" {
			t.Fatalf("Gemini %q should map to content_filter, got %q", reason, got)
		}
	}
}

func TestClaudeRefusalMapsToContentFilter(t *testing.T) {
	if got := claudeStopReasonToOpenAI("refusal"); got != "content_filter" {
		t.Fatalf("Claude refusal should map to content_filter, got %q", got)
	}
}

// R8 回归：Gemini 同一条消息内，文本部件不能抹掉先出现的 executableCode；
// functionCall/functionResponse 必须被转换而非整段丢弃。

func TestGeminiPartsAccumulateWithText(t *testing.T) {
	body := []byte(`{"model":"m","contents":[{"role":"model","parts":[{"executableCode":{"language":"python","code":"print(1)"}},{"text":"done"}]}]}`)
	req, err := GeminiToUnified(body)
	if err != nil {
		t.Fatalf("GeminiToUnified: %v", err)
	}
	if len(req.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(req.Messages))
	}
	raw, _ := json.Marshal(req.Messages[0].Content)
	s := string(raw)
	// 文本与代码部件都应保留（文本不再清空 contentParts）。
	if !strings.Contains(s, "print(1)") || !strings.Contains(s, "done") {
		t.Fatalf("both code and text parts should survive, got:\n%s", s)
	}
}

func TestGeminiFunctionCallConverted(t *testing.T) {
	body := []byte(`{"model":"m","contents":[{"role":"model","parts":[{"functionCall":{"name":"get_weather","args":{"city":"SF"}}}]}]}`)
	req, err := GeminiToUnified(body)
	if err != nil {
		t.Fatalf("GeminiToUnified: %v", err)
	}
	raw, _ := json.Marshal(req.Messages[0].Content)
	s := string(raw)
	if !strings.Contains(s, "function_call") || !strings.Contains(s, "get_weather") {
		t.Fatalf("functionCall should be converted, got:\n%s", s)
	}
}
