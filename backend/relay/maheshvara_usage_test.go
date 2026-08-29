package relay

import (
	"encoding/json"
	"strings"
	"testing"
)

// 批次五回归：Usage 保真——Claude 双 TTL 桶、模态明细双向、未知计数键透传。

// Claude 缓存写入的 ephemeral_5m/1h 双桶经 canonical 往返不丢。
func TestClaudeCacheCreationTiersRoundTrip(t *testing.T) {
	var resp ClaudeResponse
	body := `{"id":"m","model":"c","stop_reason":"end_turn","role":"assistant","content":[{"type":"text","text":"ok"}],` +
		`"usage":{"input_tokens":10,"output_tokens":5,"cache_read_input_tokens":7,"cache_creation_input_tokens":20,` +
		`"cache_creation":{"ephemeral_5m_input_tokens":15,"ephemeral_1h_input_tokens":5}}}`
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	canonical, err := AnthropicResponseToMaheshvara(&resp)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if canonical.Usage.CacheCreation5mTokens != 15 || canonical.Usage.CacheCreation1hTokens != 5 {
		t.Fatalf("cache creation tiers lost: %+v", canonical.Usage)
	}
	rendered, err := MaheshvaraToAnthropicResponse(canonical)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if rendered.Usage.CacheCreation == nil ||
		rendered.Usage.CacheCreation.Ephemeral5mInputTokens != 15 ||
		rendered.Usage.CacheCreation.Ephemeral1hInputTokens != 5 {
		t.Fatalf("cache creation tiers not written back: %+v", rendered.Usage)
	}
}

// Chat 模态明细（text/audio/image tokens）双向；未知计数键原样透传。
func TestChatModalityDetailsAndUnknownUsageKeys(t *testing.T) {
	var resp OpenAIResponse
	body := `{"id":"c","object":"chat.completion","created":1,"model":"m",` +
		`"choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],` +
		`"usage":{"prompt_tokens":100,"completion_tokens":50,"total_tokens":150,` +
		`"prompt_tokens_details":{"cached_tokens":40,"text_tokens":80,"audio_tokens":10,"image_tokens":10},` +
		`"completion_tokens_details":{"reasoning_tokens":20,"text_tokens":30},` +
		`"vendor_custom_counter":777}}`
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	canonical, err := OpenAIChatResponseToMaheshvara(&resp)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	u := canonical.Usage
	if u.TextInputTokens != 80 || u.AudioInputTokens != 10 || u.ImageInputTokens != 10 ||
		u.TextOutputTokens != 30 || u.CachedInputTokens != 40 {
		t.Fatalf("modality details lost: %+v", u)
	}
	if u.Raw["vendor_custom_counter"] == nil {
		t.Fatalf("unknown usage key not retained: %+v", u.Raw)
	}
	rendered, err := MaheshvaraToOpenAIChatResponse(canonical)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	encoded, err := json.Marshal(rendered.Usage)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, required := range []string{`"text_tokens":80`, `"audio_tokens":10`, `"cached_tokens":40`, `"reasoning_tokens":20`, `"vendor_custom_counter":777`} {
		if !strings.Contains(string(encoded), required) {
			t.Fatalf("usage detail %s missing on chat target: %s", required, encoded)
		}
	}
}

// Gemini 模态明细经 canonical 到 Chat 目标下发。
func TestGeminiModalityDetailsToChat(t *testing.T) {
	resp := &GeminiResponse{
		ResponseID: "g", ModelVersion: "gem",
		Candidates: []GeminiCandidate{{Content: GeminiContent{Role: "model", Parts: []GeminiPart{{Text: "ok"}}}, FinishReason: "STOP"}},
		UsageMetadata: GeminiUsageMeta{
			PromptTokenCount: 100, CandidatesTokenCount: 50, TotalTokenCount: 150,
			PromptTokensDetails:     []GeminiTokenDetail{{Modality: "TEXT", TokenCount: 80}, {Modality: "IMAGE", TokenCount: 20}},
			CandidatesTokensDetails: []GeminiTokenDetail{{Modality: "TEXT", TokenCount: 50}},
		},
	}
	canonical, err := GeminiResponseToMaheshvara(resp)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	rendered, err := MaheshvaraToOpenAIChatResponse(canonical)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	encoded, err := json.Marshal(rendered.Usage)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(encoded), `"text_tokens":80`) || !strings.Contains(string(encoded), `"image_tokens":20`) {
		t.Fatalf("gemini modality details not delivered to chat target: %s", encoded)
	}
}
