package server

import (
	"encoding/base64"
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
// stream=true 与 stream_options.include_usage；usage 按规范以 finish_reason
// 之后的独立尾帧到达（W1-4 回归：终态即 return 会丢掉整帧用量）。
func TestAgentCallerOpenAIChatViaAdapter(t *testing.T) {
	s := newAgentIntegrationServer(t)
	upstream := newCapturingUpstream(t, func(w http.ResponseWriter, _ string, _ int) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(openAIChunk("c1", map[string]any{"role": "assistant", "content": "你"}, "", nil)))
		_, _ = w.Write([]byte(openAIChunk("c1", map[string]any{"content": "好"}, "", nil)))
		_, _ = w.Write([]byte(openAIChunk("c1", map[string]any{}, "stop", nil)))
		// 规范的 usage-only 尾帧（choices 为空）在 finish 之后、[DONE] 之前。
		_, _ = w.Write([]byte(`data: {"id":"c1","choices":[],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}` + "\n\n"))
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

	// 调用日志：成功调用一条，② 后端转发的请求体可查。
	_, logs, err := s.store.QueryUsageLogs(t.Context(), storage.UsageQuery{KeyName: AgentUsageKeyName, Limit: 5})
	if err != nil {
		t.Fatalf("query usage: %v", err)
	}
	if len(logs) != 1 {
		t.Fatalf("usage records = %d, want 1", len(logs))
	}
	log := logs[0]
	if log.RelayMode != agentRelayMode || log.ModelName != "fake-model" {
		t.Fatalf("log meta wrong: %+v", log)
	}
	if log.TotalTokens != 5 {
		t.Fatalf("log tokens = %d", log.TotalTokens)
	}
	detail, _, err := s.store.GetUsageRecordJSON(t.Context(), log.RequestID)
	if err != nil {
		t.Fatalf("usage detail: %v", err)
	}
	if !strings.Contains(string(detail), `"outgoingBody"`) || !strings.Contains(string(detail), "fake-model") {
		t.Fatalf("outgoing body missing from usage detail: %.200s", detail)
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

	// 失败调用同样入账：statusCode=400、error 非空，③ 上游回传带错误体。
	_, logs, qErr := s.store.QueryUsageLogs(t.Context(), storage.UsageQuery{KeyName: AgentUsageKeyName, Limit: 5})
	if qErr != nil {
		t.Fatalf("query usage: %v", qErr)
	}
	if len(logs) != 1 {
		t.Fatalf("usage records = %d, want 1 (failed calls must be logged)", len(logs))
	}
	if logs[0].StatusCode != 400 || logs[0].Error == "" {
		t.Fatalf("failed log wrong: status=%d error=%q", logs[0].StatusCode, logs[0].Error)
	}
	detail, _, dErr := s.store.GetUsageRecordJSON(t.Context(), logs[0].RequestID)
	if dErr != nil {
		t.Fatalf("usage detail: %v", dErr)
	}
	if !strings.Contains(string(detail), "Invalid base64 data") || !strings.Contains(string(detail), `"outgoingBody"`) {
		t.Fatalf("failed log missing bodies: %.200s", detail)
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

	// 调用日志 ② 后端转发：custom 协议分支记录渲染产物的请求体。
	_, logs, lErr := s.store.QueryUsageLogs(t.Context(), storage.UsageQuery{KeyName: AgentUsageKeyName, Limit: 5})
	if lErr != nil || len(logs) != 1 {
		t.Fatalf("usage records = %d (err %v), want 1", len(logs), lErr)
	}
	detail, _, dErr := s.store.GetUsageRecordJSON(t.Context(), logs[0].RequestID)
	if dErr != nil {
		t.Fatalf("usage detail: %v", dErr)
	}
	if !strings.Contains(string(detail), `"outgoingBody":{"content":"{`) || !strings.Contains(string(detail), `fake-model`) {
		t.Fatalf("custom-protocol outgoing body not captured: %.200s", detail)
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

// 附件 data URL 渲染：ImageBase64/FileData 必须是 base64 文本（可再解码、
// 无 data: 前缀），不得是解码后的二进制——回归 ".messages[1]: Invalid base64
// data"（上游按 base64 校验 source.data / inlineData.data 直接 400）。
func TestAgentAttachmentBase64IsTextNotBinary(t *testing.T) {
	s := newAgentIntegrationServer(t)
	renderer := newAgentUserContentRenderer(s)
	// 1x1 PNG 的 base64。
	const pngPayload = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg=="
	dataURL := "data:image/png;base64," + pngPayload
	content := &agent.UserContent{Text: "看图", Documents: []agent.Document{{Name: "shot.png", Mime: "image/png", DataURL: dataURL}}}

	parts, err := renderer.RenderUserContent(agent.SessionMeta{}, content)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if len(parts) != 2 { // 文本块 + 图片块
		t.Fatalf("parts = %d", len(parts))
	}
	var image *relay.MaheshvaraContentPart
	for i := range parts {
		if parts[i].Type == relay.MaheshvaraContentImage {
			image = &parts[i]
		}
	}
	if image == nil {
		t.Fatalf("image part missing")
	}
	if image.ImageBase64 != pngPayload {
		t.Fatalf("image base64 corrupted: %.60s", image.ImageBase64)
	}
	if _, decodeErr := base64.StdEncoding.DecodeString(image.ImageBase64); decodeErr != nil {
		t.Fatalf("image base64 not decodable: %v", decodeErr)
	}

	// 各平台出口透传后仍是合法 base64。
	request := &relay.MaheshvaraRequest{Model: "fake-model", Messages: []relay.MaheshvaraMessage{
		{Role: "user", Content: []relay.MaheshvaraContentPart{*image}},
	}}
	anthropicBody, err := relay.MaheshvaraToAnthropic(request)
	if err != nil {
		t.Fatalf("anthropic convert: %v", err)
	}
	var claudeMsg struct {
		Messages []struct {
			Content []struct {
				Source struct {
					Data string `json:"data"`
				} `json:"source"`
			} `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(anthropicBody, &claudeMsg); err != nil {
		t.Fatalf("anthropic body: %v", err)
	}
	if len(claudeMsg.Messages) == 0 || len(claudeMsg.Messages[0].Content) == 0 {
		t.Fatalf("anthropic content missing: %s", anthropicBody)
	}
	sourceData := claudeMsg.Messages[0].Content[0].Source.Data
	if strings.HasPrefix(sourceData, "data:") {
		t.Fatalf("anthropic source.data keeps data: prefix")
	}
	if _, decodeErr := base64.StdEncoding.DecodeString(sourceData); decodeErr != nil {
		t.Fatalf("anthropic source.data not valid base64 (this is the reported 400): %v", decodeErr)
	}

	geminiBody, err := relay.MaheshvaraToGemini(request)
	if err != nil {
		t.Fatalf("gemini convert: %v", err)
	}
	var geminiReq struct {
		Contents []struct {
			Parts []struct {
				InlineData struct {
					Data string `json:"data"`
				} `json:"inlineData"`
			} `json:"parts"`
		} `json:"contents"`
	}
	if err := json.Unmarshal(geminiBody, &geminiReq); err != nil {
		t.Fatalf("gemini body: %v", err)
	}
	if len(geminiReq.Contents) == 0 || len(geminiReq.Contents[0].Parts) == 0 {
		t.Fatalf("gemini parts missing: %s", geminiBody)
	}
	if _, decodeErr := base64.StdEncoding.DecodeString(geminiReq.Contents[0].Parts[0].InlineData.Data); decodeErr != nil {
		t.Fatalf("gemini inlineData.data not valid base64: %v", decodeErr)
	}

	chatBody, err := relay.MaheshvaraToOpenAIChat(request)
	if err != nil {
		t.Fatalf("chat convert: %v", err)
	}
	if !strings.Contains(string(chatBody), "data:image/png;base64,"+pngPayload) {
		t.Fatalf("openai image_url should keep the full data URL: %.120s", chatBody)
	}
}

// 回归（W1-5）：Responses 平台不得注入 stream_options——该参数是 Chat 线
// 专属，严格上游会对未知顶层参数直接 400（且不重试）。
func TestAgentCallerResponsesNoStreamOptions(t *testing.T) {
	s := newAgentIntegrationServer(t)
	upstream := newCapturingUpstream(t, func(w http.ResponseWriter, _ string, _ int) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"完成\"}\n\n"))
		_, _ = w.Write([]byte("event: response.completed\ndata: {\"type\":\"response.completed\"}\n\n"))
	})
	seedCallerModel(t, s, upstream.URL, "responses")

	result, err := newAgentStreamCaller(s).Call(t.Context(), callerRequest(), agent.StreamCallbacks{})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if result.Text != "完成" {
		t.Fatalf("text = %q", result.Text)
	}
	req := upstream.last()
	if req.Path != "/responses" {
		t.Fatalf("path = %q, want /responses", req.Path)
	}
	if strings.Contains(req.Body, "stream_options") {
		t.Fatalf("responses body must not carry stream_options: %s", req.Body)
	}
	if !strings.Contains(req.Body, `"stream":true`) {
		t.Fatalf("stream flag missing: %s", req.Body)
	}
}

// 回归（W1-6）：custom 协议终态后的排水窗内，重复文本帧不得再计入结果
// （旧实现对 terminalBeforeBatch 视而不见，会把"协议协议"这类重复发给用户）。
func TestAgentCallerCustomProtocolPostTerminalTextIgnored(t *testing.T) {
	s := newAgentIntegrationServer(t)
	s.seedPresetProtocols()
	s.syncCustomProtocols()

	upstream := newCapturingUpstream(t, func(w http.ResponseWriter, _ string, _ int) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(openAIChunk("c9", map[string]any{"role": "assistant", "content": "协议"}, "", nil)))
		_, _ = w.Write([]byte(openAIChunk("c9", map[string]any{}, "stop", nil)))
		// 终态后的迟到文本帧 + DONE（排水窗内到达）。
		_, _ = w.Write([]byte(openAIChunk("c9", map[string]any{"content": "协议"}, "", nil)))
		_, _ = w.Write([]byte(openAIDone()))
	})
	seedCallerModel(t, s, upstream.URL, "custom:chat-completions-api")

	result, err := newAgentStreamCaller(s).Call(t.Context(), callerRequest(), agent.StreamCallbacks{})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if result.Text != "协议" {
		t.Fatalf("post-terminal text leaked into result: %q", result.Text)
	}
}
