package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/elysia-api/backend/agent"
	"github.com/elysia-api/backend/relay"
	"github.com/elysia-api/backend/storage"
)

// agentStreamCaller 单测：传输层复用 relay 适配器后的端点/鉴权/请求体断言，
// 覆盖四个内置平台 + custom:<协议> 分支 + 非 2xx 不重试。

// capturedUpstreamRequest 记录一次上游请求的关键面。
type capturedUpstreamRequest struct {
	Method string
	Path   string
	Auth   string // Authorization 头
	APIKey string // x-api-key 头
	Body   string
}

type capturingUpstream struct {
	*httptest.Server
	mu      sync.Mutex
	calls   []capturedUpstreamRequest
	handler func(w http.ResponseWriter, body string, call int)
}

func newCapturingUpstream(t *testing.T, handler func(w http.ResponseWriter, body string, call int)) *capturingUpstream {
	t.Helper()
	upstream := &capturingUpstream{handler: handler}
	upstream.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		upstream.mu.Lock()
		upstream.calls = append(upstream.calls, capturedUpstreamRequest{
			Method: r.Method, Path: r.URL.Path,
			Auth: r.Header.Get("Authorization"), APIKey: r.Header.Get("x-api-key"),
			Body: string(body),
		})
		call := len(upstream.calls)
		upstream.mu.Unlock()
		if handler != nil {
			handler(w, string(body), call)
		}
	}))
	t.Cleanup(upstream.Close)
	return upstream
}

func (u *capturingUpstream) requestCount() int {
	u.mu.Lock()
	defer u.mu.Unlock()
	return len(u.calls)
}

func (u *capturingUpstream) last() capturedUpstreamRequest {
	u.mu.Lock()
	defer u.mu.Unlock()
	if len(u.calls) == 0 {
		return capturedUpstreamRequest{}
	}
	return u.calls[len(u.calls)-1]
}

// seedCallerModel 建一个带密钥的源 + 单模型（平台可指定）。
func seedCallerModel(t *testing.T, s *Server, baseURL, platform string) {
	t.Helper()
	source := storage.ModelSource{ID: "cs1", Name: "caller-src", BaseURL: baseURL, APIKey: "sk-caller-key", Platform: platform, Enabled: true}
	if err := s.store.UpsertSource(t.Context(), source); err != nil {
		t.Fatalf("UpsertSource: %v", err)
	}
	models := []storage.Model{{
		ID: "m1", SourceID: "cs1", Name: "fake-model", BaseURL: baseURL,
		Platform: platform, Type: "llm", Enabled: true, Available: true,
	}}
	if err := s.store.ReplaceSourceModels(t.Context(), source, models); err != nil {
		t.Fatalf("ReplaceSourceModels: %v", err)
	}
}

func callerRequest() agent.CallRequest {
	return agent.CallRequest{
		ModelSourceID: "cs1", Model: "fake-model",
		Messages: []relay.MaheshvaraMessage{{Role: "user", Content: []relay.MaheshvaraContentPart{{Type: relay.MaheshvaraContentText, Text: "你好"}}}},
	}
}

// OpenAI chat 平台：适配器拼 /chat/completions + Bearer 鉴权；请求体带
// stream=true 与 stream_options.include_usage（usage 统计依赖）。
func TestAgentCallerOpenAIChatViaAdapter(t *testing.T) {
	s := newAgentIntegrationServer(t)
	upstream := newCapturingUpstream(t, func(w http.ResponseWriter, _ string, _ int) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(openAIChunk("c1", map[string]any{"role": "assistant", "content": "你"}, "", nil)))
		_, _ = w.Write([]byte(openAIChunk("c1", map[string]any{"content": "好"}, "", nil)))
		_, _ = w.Write([]byte(openAIChunk("c1", map[string]any{}, "stop", map[string]any{"prompt_tokens": 3, "completion_tokens": 2, "total_tokens": 5})))
		_, _ = w.Write([]byte(openAIDone()))
	})
	seedCallerModel(t, s, upstream.URL, "openai")

	result, err := newAgentStreamCaller(s).Call(t.Context(), callerRequest(), agent.StreamCallbacks{})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if result.Text != "你好" {
		t.Fatalf("text = %q", result.Text)
	}
	req := upstream.last()
	if req.Path != "/chat/completions" {
		t.Fatalf("path = %q, want /chat/completions", req.Path)
	}
	if req.Auth != "Bearer sk-caller-key" {
		t.Fatalf("authorization = %q", req.Auth)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(req.Body), &body); err != nil {
		t.Fatalf("body not json: %v", err)
	}
	if body["stream"] != true {
		t.Fatalf("stream flag missing: %s", req.Body)
	}
	options, _ := body["stream_options"].(map[string]any)
	if options["include_usage"] != true {
		t.Fatalf("stream_options.include_usage missing: %s", req.Body)
	}
	if result.Usage == nil || result.Usage.TotalTokens != 5 {
		t.Fatalf("usage not captured: %+v", result.Usage)
	}
}

// Anthropic 平台：适配器拼 /v1/messages + x-api-key/anthropic-version；请求体
// 注入 stream=true；Anthropic 形状的 SSE 被聚合。
func TestAgentCallerAnthropicViaAdapter(t *testing.T) {
	s := newAgentIntegrationServer(t)
	upstream := newCapturingUpstream(t, func(w http.ResponseWriter, _ string, _ int) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"m1\",\"role\":\"assistant\",\"usage\":{\"input_tokens\":4}}}\n\n"))
		_, _ = w.Write([]byte("event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n"))
		_, _ = w.Write([]byte("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"完成\"}}\n\n"))
		_, _ = w.Write([]byte("event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n"))
		_, _ = w.Write([]byte("event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":2}}\n\n"))
		_, _ = w.Write([]byte("event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"))
	})
	seedCallerModel(t, s, upstream.URL, "anthropic")

	result, err := newAgentStreamCaller(s).Call(t.Context(), callerRequest(), agent.StreamCallbacks{})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if result.Text != "完成" {
		t.Fatalf("text = %q", result.Text)
	}
	req := upstream.last()
	if req.Path != "/v1/messages" {
		t.Fatalf("path = %q, want /v1/messages", req.Path)
	}
	if req.APIKey != "sk-caller-key" {
		t.Fatalf("x-api-key = %q", req.APIKey)
	}
	if !strings.Contains(req.Body, `"stream":true`) {
		t.Fatalf("stream flag missing: %s", req.Body)
	}
}

// 上游 400（永久错误）：不重试、错误文案带状态码，一次调用即返回。
func TestAgentCallerUpstream400NotRetried(t *testing.T) {
	s := newAgentIntegrationServer(t)
	upstream := newCapturingUpstream(t, func(w http.ResponseWriter, _ string, _ int) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":".messages[1]: Invalid base64 data"}}`))
	})
	seedCallerModel(t, s, upstream.URL, "openai")

	_, err := newAgentStreamCaller(s).Call(t.Context(), callerRequest(), agent.StreamCallbacks{})
	if err == nil || !strings.Contains(err.Error(), "上游模型返回 400") {
		t.Fatalf("err = %v, want 上游模型返回 400", err)
	}
	if count := upstream.requestCount(); count != 1 {
		t.Fatalf("upstream calls = %d, want 1 (400 must not retry)", count)
	}
}

// custom:<协议ID> 平台：走注册协议渲染 + 自定义协议发送 + 注册流解码器，
// 请求路径与请求体由协议定义（预置 chat-completions-api 即 OpenAI chat 形状）。
func TestAgentCallerCustomProtocolPlatform(t *testing.T) {
	s := newAgentIntegrationServer(t)
	s.seedPresetProtocols()
	s.syncCustomProtocols()

	upstream := newCapturingUpstream(t, func(w http.ResponseWriter, _ string, _ int) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(openAIChunk("c9", map[string]any{"role": "assistant", "content": "协议"}, "", nil)))
		_, _ = w.Write([]byte(openAIChunk("c9", map[string]any{}, "stop", nil)))
		_, _ = w.Write([]byte(openAIDone()))
	})
	seedCallerModel(t, s, upstream.URL, "custom:chat-completions-api")

	result, err := newAgentStreamCaller(s).Call(t.Context(), callerRequest(), agent.StreamCallbacks{})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if result.Text != "协议" {
		t.Fatalf("text = %q", result.Text)
	}
	req := upstream.last()
	if req.Path != "/v1/chat/completions" {
		t.Fatalf("path = %q (protocol-defined path expected)", req.Path)
	}
	if req.Auth != "Bearer sk-caller-key" {
		t.Fatalf("authorization = %q", req.Auth)
	}
	if !strings.Contains(req.Body, `"fake-model"`) {
		t.Fatalf("protocol-rendered body missing model name: %s", req.Body)
	}
}

// 未注册的 custom 协议：调用前即失败并给出可读错误。
func TestAgentCallerUnregisteredCustomProtocol(t *testing.T) {
	s := newAgentIntegrationServer(t)
	seedCallerModel(t, s, "http://127.0.0.1:9", "custom:never-registered")
	_, err := newAgentStreamCaller(s).Call(t.Context(), callerRequest(), agent.StreamCallbacks{})
	if err == nil || !strings.Contains(err.Error(), "未注册") {
		t.Fatalf("err = %v, want 未注册", err)
	}
}
