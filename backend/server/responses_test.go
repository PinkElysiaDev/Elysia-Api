package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/elysia-api/backend/config"
	"github.com/elysia-api/backend/relay"
	"github.com/gin-gonic/gin"
)

// C1 回归：/v1/responses 入口的故障转移。首个候选 500（可重试），第二个 200，
// 客户端应得 200，且两个上游都被尝试。走 transform 路径（chat_completions 上游）。
func TestResponsesFailoverToHealthyModel(t *testing.T) {
	var firstHits, secondHits int32
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&firstHits, 1)
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"error":"upstream boom"}`)
	}))
	defer bad.Close()
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&secondHits, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, okChatCompletionBody(t))
	}))
	defer good.Close()

	group := config.ModelGroupConfig{
		ID: "g1", Name: "grp", Enabled: true, Strategy: "sequential", MaxRetries: 2,
		Models: []config.ModelRef{openAIModel("m-bad", bad.URL), openAIModel("m-good", good.URL)},
	}
	s := newTestServer([]config.ModelGroupConfig{group})

	rec := httptest.NewRecorder()
	c, _ := newResponsesContext(rec, `{"model":"grp","input":"hi"}`)
	s.responses(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 after responses failover, got %d body=%s", rec.Code, rec.Body.String())
	}
	if atomic.LoadInt32(&firstHits) != 1 {
		t.Fatalf("first (bad) upstream should be hit once, got %d", firstHits)
	}
	if atomic.LoadInt32(&secondHits) != 1 {
		t.Fatalf("second (good) upstream should be hit once, got %d", secondHits)
	}
}

// C1 回归：空模型组（无候选）应返回 500「no available models」，而非旧实现里
// 空 baseUrl 掉进 SSRF 校验误报的 403。
func TestResponsesEmptyGroupReturns500(t *testing.T) {
	group := config.ModelGroupConfig{ID: "g1", Name: "grp", Enabled: true, Models: nil}
	s := newTestServer([]config.ModelGroupConfig{group})

	rec := httptest.NewRecorder()
	c, _ := newResponsesContext(rec, `{"model":"grp","input":"hi"}`)
	s.responses(c)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 for empty group, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func newResponsesContext(rec *httptest.ResponseRecorder, body string) (*gin.Context, *httptest.ResponseRecorder) {
	c, _ := gin.CreateTestContext(rec)
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	c.Request = req
	return c, rec
}

func TestSelectResponsesTargetFormatDefaultClaudeTransforms(t *testing.T) {
	model := config.ModelRef{Name: "claude-opus-4-8", Platform: "anthropic"}

	format, mode, err := selectResponsesTargetFormat(model, relay.PlatformAnthropic, config.ResponsesConfig{})

	if err != nil {
		t.Fatalf("expected Claude model to transform by default, got error: %v", err)
	}
	if format != relay.FormatClaude || mode != "transformed_responses" {
		t.Fatalf("expected Claude transform target, got format=%q mode=%q", format, mode)
	}
}

func TestSelectResponsesTargetFormatNativeClaudeErrors(t *testing.T) {
	model := config.ModelRef{Name: "claude-opus-4-8", Platform: "anthropic"}

	_, mode, err := selectResponsesTargetFormat(model, relay.PlatformAnthropic, config.ResponsesConfig{UpstreamMode: "native"})

	if err == nil {
		t.Fatal("expected native Claude model without Responses support to error")
	}
	if mode != "native_responses" {
		t.Fatalf("expected native_responses mode, got %q", mode)
	}
	if !strings.Contains(err.Error(), "does not declare Responses API support") {
		t.Fatalf("expected Responses support error, got %v", err)
	}
}

// 旧的 "openai" apiFormat 现在归一化为 chat_completions，因此走 transform 而非
// native Responses——这是有意的契约变更：只有用户明确选 responses 才透传，避免
// 旧实现里 Responses→Responses 的有损重建导致 codex 1 秒断连。
func TestSelectResponsesTargetFormatLegacyOpenAITransforms(t *testing.T) {
	model := config.ModelRef{Name: "gpt-4.1", Platform: "openai"}

	format, mode, err := selectResponsesTargetFormat(model, relay.PlatformOpenAI, config.ResponsesConfig{})

	if err != nil {
		t.Fatalf("expected legacy openai model to transform, got error: %v", err)
	}
	if format != relay.FormatOpenAIChat || mode != "transformed_responses" {
		t.Fatalf("expected chat transform target, got format=%q mode=%q", format, mode)
	}
}

// 明确选 responses apiFormat → 走 native Responses（透传），不转换。
func TestSelectResponsesTargetFormatExplicitResponsesIsNative(t *testing.T) {
	model := config.ModelRef{Name: "gpt-5-codex", Platform: "responses"}

	format, mode, err := selectResponsesTargetFormat(model, relay.PlatformOpenAI, config.ResponsesConfig{})

	if err != nil {
		t.Fatalf("expected responses apiFormat to use native Responses, got error: %v", err)
	}
	if format != relay.FormatResponses || mode != "native_responses" {
		t.Fatalf("expected native Responses target, got format=%q mode=%q", format, mode)
	}
}

func TestSelectResponsesTargetFormatAutoGeminiTransforms(t *testing.T) {
	model := config.ModelRef{Name: "gemini-2.5-pro", Platform: "gemini"}

	format, mode, err := selectResponsesTargetFormat(model, relay.PlatformGemini, config.ResponsesConfig{UpstreamMode: "auto"})

	if err != nil {
		t.Fatalf("expected Gemini model to transform, got error: %v", err)
	}
	if format != relay.FormatGemini || mode != "transformed_responses" {
		t.Fatalf("expected Gemini transform target, got format=%q mode=%q", format, mode)
	}
}

func TestSelectResponsesTargetFormatUnknownWithChatEndpointTransforms(t *testing.T) {
	chatCompletions := true
	model := config.ModelRef{
		Name:      "custom-chat-model",
		Platform:  "custom",
		Endpoints: &config.EndpointCapabilities{ChatCompletions: &chatCompletions},
	}

	format, mode, err := selectResponsesTargetFormat(model, relay.PlatformUnknown, config.ResponsesConfig{UpstreamMode: "auto"})

	if err != nil {
		t.Fatalf("expected explicit chat endpoint to transform, got error: %v", err)
	}
	if format != relay.FormatOpenAIChat || mode != "transformed_responses" {
		t.Fatalf("expected OpenAI chat transform target, got format=%q mode=%q", format, mode)
	}
}

func TestSelectResponsesTargetFormatExplicitClaudeMessagesFalseErrors(t *testing.T) {
	claudeMessages := false
	model := config.ModelRef{
		Name:      "claude-opus-4-8",
		Platform:  "anthropic",
		Endpoints: &config.EndpointCapabilities{ClaudeMessages: &claudeMessages},
	}

	_, mode, err := selectResponsesTargetFormat(model, relay.PlatformAnthropic, config.ResponsesConfig{UpstreamMode: "auto"})

	if err == nil {
		t.Fatal("expected explicit Claude Messages false to prevent transform")
	}
	if mode != "auto_responses" {
		t.Fatalf("expected auto_responses mode, got %q", mode)
	}
	if !strings.Contains(err.Error(), "does not declare a transformable endpoint") {
		t.Fatalf("expected transformable endpoint error, got %v", err)
	}
}
