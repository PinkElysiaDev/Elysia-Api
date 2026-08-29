package relay

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// 批次一回归：推理链路闭环——Responses 签名事件、信封 v2 门控往返、
// adaptive thinking、effort none 省略、chat reasoning_details 双向。

// response.reasoning_signature.delta 必须映射为签名事件（此前被 default
// 分支误当推理文本拼接），并渲染回 Responses 下游。
func TestResponsesStreamSignatureDeltaRouting(t *testing.T) {
	body := strings.Join([]string{
		`data: {"type":"response.created","response":{"id":"r","output":[]}}`,
		``,
		`data: {"type":"response.output_text.delta","item_id":"msg_1","output_index":1,"content_index":0,"delta":"答案"}`,
		``,
		`data: {"type":"response.reasoning_signature.delta","item_id":"rs_1","output_index":0,"delta":"sig-fragment"}`,
		``,
		`data: {"type":"response.completed","response":{"id":"r","output":[],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}`,
		``,
		`data: [DONE]`,
		``,
	}, "\n")
	writer := &captureStreamWriter{}
	if err := TransformStreamViaMaheshvara(context.Background(), sseResponse(body), FormatResponses, FormatResponses, writer, "m"); err != nil {
		t.Fatalf("transform: %v", err)
	}
	out := writer.String()
	if !strings.Contains(out, "response.reasoning_signature.delta") || !strings.Contains(out, "sig-fragment") {
		t.Fatalf("signature delta not routed/rendered:\n%s", out)
	}
}

// 终态 reasoning item 的 encrypted_content 必须渲染回 Responses 下游（跨协议
// Codex 续轮闭环）。
func TestResponsesRenderEncryptedContentOnTerminalItem(t *testing.T) {
	reasoningItem := `{"id":"rs_1","type":"reasoning","status":"completed","summary":[{"type":"summary_text","text":"思考"}],"encrypted_content":"enc-data"}`
	body := strings.Join([]string{
		`data: {"type":"response.created","response":{"id":"r","output":[]}}`,
		``,
		`data: {"type":"response.output_item.done","output_index":0,"item":` + reasoningItem + `}`,
		``,
		`data: {"type":"response.completed","response":{"id":"r","output":[` + reasoningItem + `],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}`,
		``,
		`data: [DONE]`,
		``,
	}, "\n")
	writer := &captureStreamWriter{}
	if err := TransformStreamViaMaheshvara(context.Background(), sseResponse(body), FormatResponses, FormatResponses, writer, "m"); err != nil {
		t.Fatalf("transform: %v", err)
	}
	out := writer.String()
	if !strings.Contains(out, "enc-data") {
		t.Fatalf("encrypted_content missing on rendered reasoning item:\n%s", out)
	}
	if !strings.Contains(out, "思考") {
		t.Fatalf("reasoning summary text missing:\n%s", out)
	}
}

// 信封 v2 往返 + provider 门控：openai 密文经 Claude 客户端往返后回放给
// Responses 上游；gemini 签发的密文不回放给 Responses（只保留摘要）。
func TestEnvelopeV2RoundTripAndProviderGate(t *testing.T) {
	response := &MaheshvaraResponse{
		ID: "r", Model: "gpt-x", Status: "completed",
		Output: []MaheshvaraOutputItem{{
			ID: "rs", Type: CanonicalOutputReasoning, Status: "completed",
			Content: []MaheshvaraContentPart{{
				Type: CanonicalContentReasoning, ReasoningText: "理由",
				EncryptedContent: "openai-ciphertext", EncryptedProvider: CanonicalSignatureProviderOpenAI,
			}},
		}},
	}
	claudeResp, err := MaheshvaraToAnthropicResponse(response)
	if err != nil {
		t.Fatalf("render claude: %v", err)
	}
	if len(claudeResp.Content) != 1 || !strings.HasPrefix(claudeResp.Content[0].Signature, "maheshvara-reasoning-v2:") {
		t.Fatalf("openai ciphertext should ride as v2 envelope: %+v", claudeResp.Content)
	}
	// 客户端下一轮把 thinking 块原样带回。
	claudeReq := `{"model":"grp","max_tokens":16,"messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"理由","signature":` + jsonQuote(claudeResp.Content[0].Signature) + `}]}]}`
	req, err := AnthropicToMaheshvara([]byte(claudeReq))
	if err != nil {
		t.Fatalf("decode back: %v", err)
	}
	part := findReasoningPart(t, req)
	if part.EncryptedContent != "openai-ciphertext" || part.EncryptedProvider != CanonicalSignatureProviderOpenAI {
		t.Fatalf("envelope did not round-trip: %+v", part)
	}
	// 回放给 Responses 上游：input 中出现带 encrypted_content 的 reasoning item。
	target, err := MaheshvaraToOpenAIResponses(req, nil)
	if err != nil {
		t.Fatalf("render responses: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(target, &payload); err != nil {
		t.Fatalf("unmarshal target: %v", err)
	}
	input := payload["input"].([]any)
	foundEncrypted := false
	for _, raw := range input {
		item, _ := raw.(map[string]any)
		if item["type"] == "reasoning" && item["encrypted_content"] == "openai-ciphertext" {
			foundEncrypted = true
		}
	}
	if !foundEncrypted {
		t.Fatalf("reasoning item with encrypted_content missing: %s", target)
	}
	if !containsStringValue(payload["include"], "reasoning.encrypted_content") {
		t.Fatalf("include should request encrypted reasoning: %s", target)
	}

	// 门控：gemini 签发的密文不回放给 Responses 上游。
	for i := range req.Messages {
		for j := range req.Messages[i].Content {
			if req.Messages[i].Content[j].Type == CanonicalContentReasoning {
				req.Messages[i].Content[j].EncryptedProvider = CanonicalSignatureProviderGemini
			}
		}
	}
	target, err = MaheshvaraToOpenAIResponses(req, nil)
	if err != nil {
		t.Fatalf("render gated responses: %v", err)
	}
	if strings.Contains(string(target), "openai-ciphertext") {
		t.Fatalf("gemini-minted ciphertext must not be replayed to an openai upstream: %s", target)
	}
}

// Claude adaptive thinking：{"type":"adaptive"} + output_config.effort 解码为
// canonical Adaptive，编码回 Claude 请求保持 adaptive 形态。
func TestClaudeAdaptiveThinking(t *testing.T) {
	req, err := AnthropicToMaheshvara([]byte(`{"model":"grp","max_tokens":16,"thinking":{"type":"adaptive"},"output_config":{"effort":"high"},"messages":[{"role":"user","content":"hi"}]}`))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if req.Thinking == nil || !req.Thinking.Adaptive || !req.Thinking.Enabled || req.Thinking.Effort != "high" {
		t.Fatalf("adaptive thinking not decoded: %+v", req.Thinking)
	}
	target, err := MaheshvaraToAnthropic(req)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(target, &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	thinking, _ := payload["thinking"].(map[string]any)
	if thinking["type"] != "adaptive" {
		t.Fatalf("adaptive thinking not rendered: %s", target)
	}
	outputConfig, _ := payload["output_config"].(map[string]any)
	if outputConfig["effort"] != "high" {
		t.Fatalf("output_config.effort not rendered: %s", target)
	}
}

// effort:"none" 发给 Responses 上游时必须整个省略字段（上游会把 none 静默
// 当成 low 档执行）。
func TestResponsesEffortNoneOmitted(t *testing.T) {
	target, err := MaheshvaraToOpenAIResponses(&MaheshvaraRequest{
		Model: "m", Messages: []MaheshvaraMessage{{Role: "user", Content: []MaheshvaraContentPart{{Type: CanonicalContentText, Text: "hi"}}}},
		Reasoning: &CanonicalReasoning{Effort: "none", Raw: map[string]any{"effort": json.RawMessage(`"none"`)}},
	}, nil)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if strings.Contains(string(target), "effort") {
		t.Fatalf("effort none must be omitted entirely: %s", target)
	}
}

// chat reasoning_details 逐条解析与重建；reasoning_opaque 走密文。
func TestChatReasoningDetailsRoundTrip(t *testing.T) {
	req, err := OpenAIChatToMaheshvara([]byte(`{"model":"grp","messages":[{"role":"assistant","content":"答案","reasoning_details":[{"type":"reasoning.text","text":"第一步"},{"type":"reasoning.encrypted","data":"opaque-data"}]}]}`))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	msg := req.Messages[0]
	texts := 0
	encrypted := ""
	for _, part := range msg.Content {
		if part.Type == CanonicalContentReasoning {
			if part.EncryptedContent != "" {
				encrypted = part.EncryptedContent
			} else {
				texts++
			}
		}
	}
	if texts != 1 || encrypted != "opaque-data" {
		t.Fatalf("reasoning_details not parsed per-entry: %+v", msg.Content)
	}
	target, err := MaheshvaraToOpenAIChat(req)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(string(target), "\"reasoning_details\"") {
		t.Fatalf("reasoning_details not rebuilt: %s", target)
	}
	if !strings.Contains(string(target), "opaque-data") {
		t.Fatalf("encrypted reasoning detail not replayed: %s", target)
	}
}

func findReasoningPart(t *testing.T, req *MaheshvaraRequest) MaheshvaraContentPart {
	t.Helper()
	for _, msg := range req.Messages {
		for _, part := range msg.Content {
			if part.Type == CanonicalContentReasoning {
				return part
			}
		}
	}
	t.Fatalf("no reasoning part found: %+v", req.Messages)
	return MaheshvaraContentPart{}
}

func containsStringValue(value any, target string) bool {
	switch typed := value.(type) {
	case []any:
		for _, item := range typed {
			if s, ok := item.(string); ok && s == target {
				return true
			}
		}
	case []string:
		for _, s := range typed {
			if s == target {
				return true
			}
		}
	}
	return false
}

func jsonQuote(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}
