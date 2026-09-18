package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/elysia-api/backend/config"
	"github.com/elysia-api/backend/relay"
	"github.com/gin-gonic/gin"
)

// 四线制错误矩阵:未知模型在每条客户端线制上都返回该协议的标准错误体
// (4xx,SDK/Codex 不再自动重试;type/code/status 由 ErrorClass 派生)。
func TestUnknownModelRendersProtocolErrors(t *testing.T) {
	s := newTestServer(nil)
	cases := []struct {
		name    string
		path    string
		body    string
		status  int
		want    []string
		forbid  []string
		handler func(c *gin.Context)
	}{
		{
			name: "openai-chat", path: "/v1/chat/completions",
			body:    `{"model":"gpt-5.6-luna","messages":[{"role":"user","content":"hi"}]}`,
			status:  http.StatusNotFound,
			want:    []string{`"type":"invalid_request_error"`, `"code":"model_not_found"`, `"param":"model"`},
			forbid:  []string{"model group"},
			handler: s.chatCompletions,
		},
		{
			name: "claude-messages", path: "/v1/messages",
			body:    `{"model":"gpt-5.6-luna","max_tokens":10,"messages":[{"role":"user","content":"hi"}]}`,
			status:  http.StatusNotFound,
			want:    []string{`"type":"error"`, `"type":"not_found_error"`},
			handler: s.chatCompletions,
		},
		{
			name: "gemini", path: "/v1beta/models/gpt-5.6-luna:generateContent", // Params 见下方 v1beta 分支
			body:    `{"contents":[{"parts":[{"text":"hi"}]}]}`,
			status:  http.StatusNotFound,
			want:    []string{`"status":"NOT_FOUND"`},
			handler: s.chatCompletions,
		},
		{
			name: "responses", path: "/v1/responses",
			body:    `{"model":"gpt-5.6-luna","input":"hi"}`,
			status:  http.StatusNotFound,
			want:    []string{`"code":"model_not_found"`, `"param":"model"`},
			handler: s.responses,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(tc.body))
			c.Request.Header.Set("Content-Type", "application/json")
			if strings.HasPrefix(tc.path, "/v1beta/") {
				// gin 通配符路由 *action 捕获 /models/MODEL:action,直调 handler 时手动注入。
				c.Params = gin.Params{{Key: "action", Value: strings.TrimPrefix(tc.path, "/v1beta")}}
			}
			tc.handler(c)
			if rec.Code != tc.status {
				t.Fatalf("status = %d, want %d, body=%s", rec.Code, tc.status, rec.Body.String())
			}
			body := rec.Body.String()
			for _, want := range tc.want {
				if !strings.Contains(body, want) {
					t.Fatalf("body missing %s: %s", want, body)
				}
			}
			for _, banned := range tc.forbid {
				if strings.Contains(body, banned) {
					t.Fatalf("body must not leak %q: %s", banned, body)
				}
			}
			if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
				t.Fatalf("pre-stream errors must be JSON, got Content-Type=%s", ct)
			}
		})
	}
}

// stream:true 的前置错误仍是带 JSON Content-Type 的普通响应(非 SSE):
// Codex 等客户端依赖此行为判定请求失败而不是挂在一个空流上。
func TestStreamPreflightErrorStaysJSON(t *testing.T) {
	s := newTestServer(nil)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-5.6-luna","stream":true,"input":"hi"}`))
	c.Request.Header.Set("Content-Type", "application/json")
	s.responses(c)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Fatalf("stream preflight error must be JSON, got %s", ct)
	}
	if !strings.Contains(rec.Body.String(), `"code":"model_not_found"`) {
		t.Fatalf("unexpected body: %s", rec.Body.String())
	}
}

// 401 按线制渲染:OpenAI 线是 invalid_request_error + invalid_api_key。
func TestAuthFailureRendersOpenAIError(t *testing.T) {
	s := newTestServer(nil)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"x"}`))
	s.authMiddleware()(c)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"code":"invalid_api_key"`) || !strings.Contains(body, `"type":"invalid_request_error"`) {
		t.Fatalf("401 must be a typed OpenAI error with invalid_api_key, got: %s", body)
	}
}

// 跨线制上游错误翻译:Claude 上游 429(rate_limit_error)+ OpenAI 客户端
// → 客户端收到 OpenAI 形态的 rate_limit_error,而非 Anthropic 形态泄漏。
func TestCrossProtocolUpstreamErrorTranslation(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"type":"error","error":{"type":"rate_limit_error","message":"Number of requests has exceeded your per-minute rate limit"}}`))
	}))
	defer upstream.Close()

	group := config.ModelGroupConfig{
		ID: "g1", Name: "grp", Enabled: true,
		Models: []config.ModelRef{{ID: "m1", Name: "claude-x", BaseURL: upstream.URL, APIKey: "k", Platform: "anthropic"}},
	}
	s := newTestServer([]config.ModelGroupConfig{group})
	c, rec := chatRequestContext(`{"model":"grp","messages":[{"role":"user","content":"hi"}]}`)
	s.chatCompletions(c)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429, body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"type":"rate_limit_error"`) || !strings.Contains(body, `"message":`) {
		t.Fatalf("client must receive an OpenAI-shaped rate_limit_error, got: %s", body)
	}
	if strings.Contains(body, `"type":"error","error"`) {
		t.Fatalf("Anthropic envelope must not leak to OpenAI clients: %s", body)
	}
}

// 同线制上游错误原样透传(保真):OpenAI 上游错误体逐字节到达客户端。
func TestSameProtocolUpstreamErrorPassthrough(t *testing.T) {
	upstreamBody := `{"error":{"message":"You exceeded your current quota","type":"insufficient_quota","param":null,"code":"insufficient_quota"}}`
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(upstreamBody))
	}))
	defer upstream.Close()

	group := config.ModelGroupConfig{
		ID: "g1", Name: "grp", Enabled: true,
		Models: []config.ModelRef{{ID: "m1", Name: "gpt-x", BaseURL: upstream.URL, APIKey: "k", Platform: "openai"}},
	}
	s := newTestServer([]config.ModelGroupConfig{group})
	c, rec := chatRequestContext(`{"model":"grp","messages":[{"role":"user","content":"hi"}]}`)
	s.chatCompletions(c)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", rec.Code)
	}
	// 同信封(OpenAI 族上游 + OpenAI 客户端)必须逐字节透传,不得改写 type/丢字段。
	if got := rec.Body.String(); got != upstreamBody {
		t.Fatalf("same-envelope upstream error must pass through verbatim: got %s want %s", got, upstreamBody)
	}
}

// 编译期锚点:确保 relay 常量参与此文件(避免 import 漂移)。
var _ = relay.FormatOpenAI
