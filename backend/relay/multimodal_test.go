package relay

import (
	"encoding/json"
	"strings"
	"testing"
)

// R4 回归：base64 图片经 canonical 模型转换到 Claude / Gemini 时不丢失。
// 旧实现 parseClaudeMessages 只存 Raw、各 emitter 的 switch 无 image case，
// 任何经 canonical 路由的 vision 请求都会丢图。

func TestCanonicalImageOpenAIToClaude(t *testing.T) {
	// OpenAI 客户端：data: URI 内联 base64 图片。
	body := []byte(`{"model":"m","messages":[{"role":"user","content":[{"type":"text","text":"look"},{"type":"image_url","image_url":{"url":"data:image/png;base64,iVBORw0KGgo="}}]}]}`)
	req, err := OpenAIChatRequestToCanonical(body)
	if err != nil {
		t.Fatalf("to canonical: %v", err)
	}
	out, err := CanonicalToClaudeRequest(req)
	if err != nil {
		t.Fatalf("to claude: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(out, &parsed); err != nil {
		t.Fatalf("unmarshal claude: %v", err)
	}
	s := string(out)
	for _, want := range []string{`"type":"image"`, `"type":"base64"`, `"media_type":"image/png"`, `"data":"iVBORw0KGgo="`} {
		if !strings.Contains(s, want) {
			t.Fatalf("claude request should contain %q, got:\n%s", want, s)
		}
	}
}

func TestCanonicalImageOpenAIToGemini(t *testing.T) {
	body := []byte(`{"model":"m","messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"data:image/jpeg;base64,QQ=="}}]}]}`)
	req, err := OpenAIChatRequestToCanonical(body)
	if err != nil {
		t.Fatalf("to canonical: %v", err)
	}
	out, err := CanonicalToGeminiRequest(req)
	if err != nil {
		t.Fatalf("to gemini: %v", err)
	}
	s := string(out)
	for _, want := range []string{`"inlineData"`, `"mimeType":"image/jpeg"`, `"data":"QQ=="`} {
		if !strings.Contains(s, want) {
			t.Fatalf("gemini request should contain %q, got:\n%s", want, s)
		}
	}
}

func TestCanonicalImageClaudeToOpenAI(t *testing.T) {
	// Claude 客户端：base64 image block → 经 canonical → OpenAI data: URI。
	body := []byte(`{"model":"m","messages":[{"role":"user","content":[{"type":"image","source":{"type":"base64","media_type":"image/webp","data":"AAAA"}}]}]}`)
	req, err := ClaudeRequestToCanonical(body)
	if err != nil {
		t.Fatalf("to canonical: %v", err)
	}
	out, err := CanonicalToOpenAIChatRequest(req)
	if err != nil {
		t.Fatalf("to openai: %v", err)
	}
	s := string(out)
	for _, want := range []string{`"image_url"`, `data:image/webp;base64,AAAA`} {
		if !strings.Contains(s, want) {
			t.Fatalf("openai request should contain %q, got:\n%s", want, s)
		}
	}
}
