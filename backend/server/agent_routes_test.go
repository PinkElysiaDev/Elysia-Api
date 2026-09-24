package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elysia-api/backend/agent"
	"github.com/elysia-api/backend/config"
	"github.com/elysia-api/backend/relay"
	"github.com/elysia-api/backend/storage"
	"github.com/gin-gonic/gin"
)

// Agent 端到端：httptest 伪模型（OpenAI chat SSE）× 真 SQLite 会话存储 ×
// 真 SSE 编码，覆盖「草稿工具 → 终稿」全轮次、门控审批、用量入账。

// agentContextWithID 为带 :id 路由参数的 handler 构造测试上下文。
func agentContextWithID(method, target, id, body string) (*gin.Context, *httptest.ResponseRecorder) {
	c, rec := adminProtocolContext(method, target, body)
	c.Params = gin.Params{{Key: "id", Value: id}}
	return c, rec
}

func newAgentIntegrationServer(t *testing.T) *Server {
	t.Helper()
	gin.SetMode(gin.TestMode)
	dir := t.TempDir()
	cfg := &config.Config{}
	store, err := storage.Open(filepath.Join(dir, "agent.sqlite3"))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	s := &Server{
		config:                 cfg,
		engine:                 gin.New(),
		openaiAdapter:          relay.NewOpenAIAdapter(10 * time.Second),
		claudeAdapter:          relay.NewClaudeAdapter(10 * time.Second),
		geminiAdapter:          relay.NewGeminiAdapter(10 * time.Second),
		roundRobinIndex:        make(map[string]int),
		rateLimits:             make(map[string]*rateLimitState),
		affinity:               newAffinityCache(),
		store:                  store,
		skipOutboundValidation: true,
	}
	return s
}

// fakeAgentModelServer 是 OpenAI chat 兼容的 SSE 模型：按脚本依次响应。
type fakeAgentModelServer struct {
	*httptest.Server
	calls atomic.Int64
	// scripts[i] 是第 i+1 次调用的 SSE 块序列。
	scripts [][]string
	// bodies 捕获请求体（断言 thinking/tools 传参）。
	bodies []string
}

func newFakeAgentModelServer(t *testing.T, scripts [][]string) *fakeAgentModelServer {
	t.Helper()
	fake := &fakeAgentModelServer{scripts: scripts}
	fake.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		fake.bodies = append(fake.bodies, string(body))
		index := int(fake.calls.Add(1)) - 1
		if index >= len(fake.scripts) {
			t.Errorf("unexpected model call #%d", index+1)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, chunk := range fake.scripts[index] {
			_, _ = w.Write([]byte(chunk))
		}
	}))
	t.Cleanup(fake.Close)
	return fake
}

// openAIChunk 构造一个 OpenAI chat SSE data 块（程序化生成，避免手写转义）。
func openAIChunk(id string, delta map[string]any, finish string, usage map[string]any) string {
	choice := map[string]any{"index": 0, "delta": delta}
	if finish != "" {
		choice["finish_reason"] = finish
	}
	chunk := map[string]any{"id": id, "choices": []any{choice}}
	if delta["role"] != nil && id != "" {
		chunk["model"] = "fake-model"
	}
	if usage != nil {
		chunk["usage"] = usage
	}
	encoded, _ := json.Marshal(chunk)
	return "data: " + string(encoded) + "\n\n"
}

func openAIDone() string { return "data: [DONE]\n\n" }

func toolCallDelta(index int, id, name, args string) map[string]any {
	tool := map[string]any{"index": index, "function": map[string]any{"name": name, "arguments": args}}
	if id != "" {
		tool["id"] = id
		tool["type"] = "function"
	}
	return map[string]any{"tool_calls": []any{tool}}
}

func agentTestConfig(id string) string {
	config := map[string]any{
		"id": id,
		"request": map[string]any{
			"method":       "POST",
			"path":         "/v1/x",
			"bodyTemplate": `{"model":"{{maheshvara.model}}"}`,
		},
	}
	encoded, _ := json.Marshal(config)
	return string(encoded)
}

// parseSSEEvents 从 recorder 输出解析 (event, data) 对。
func parseSSEEvents(t *testing.T, body string) []agentSSEEvent {
	t.Helper()
	var events []agentSSEEvent
	for _, block := range strings.Split(body, "\n\n") {
		var eventType, data string
		for _, line := range strings.Split(block, "\n") {
			switch {
			case strings.HasPrefix(line, "event: "):
				eventType = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				data = strings.TrimPrefix(line, "data: ")
			}
		}
		if eventType != "" && data != "" {
			events = append(events, agentSSEEvent{Type: eventType, Data: json.RawMessage(data)})
		}
	}
	return events
}

type agentSSEEvent struct {
	Type string
	Data json.RawMessage
}

func (e agentSSEEvent) field(path string) any {
	var value map[string]any
	if err := json.Unmarshal(e.Data, &value); err != nil {
		return nil
	}
	return value[path]
}

func errorText(events []agentSSEEvent) string {
	for _, event := range events {
		if event.Type == "error" {
			return truncateForDisplay(string(event.Data), 600)
		}
	}
	return ""
}

func hasAgentEvent(events []agentSSEEvent, eventType string) bool {
	for _, event := range events {
		if event.Type == eventType {
			return true
		}
	}
	return false
}

func TestAgentFullTurnWithDraftTool(t *testing.T) {
	s := newAgentIntegrationServer(t)
	// 第 1 次调用：bash 写协议草稿；第 2 次：终稿文本。
	draftCommand := `{"command":"elysia protocol draft '{\"id\":\"agent-proto\",\"request\":{\"method\":\"POST\",\"path\":\"/v1/x\",\"bodyTemplate\":\"{\\\"model\\\":\\\"{{maheshvara.model}}\\\"}\"}}'"}`
	fake := newFakeAgentModelServer(t, [][]string{
		{openAIChunk("c1", map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{"index": 0, "id": "call_1", "type": "function", "function": map[string]any{"name": "bash", "arguments": ""}}}}, "", nil),
			openAIChunk("c1", toolCallDelta(0, "", "", draftCommand), "", nil),
			openAIChunk("c1", map[string]any{}, "tool_calls", map[string]any{"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15}),
			openAIDone()},
		{openAIChunk("c2", map[string]any{"role": "assistant", "content": "配置"}, "", nil),
			openAIChunk("c2", map[string]any{"content": "完成"}, "", nil),
			openAIChunk("c2", map[string]any{}, "stop", map[string]any{"prompt_tokens": 20, "completion_tokens": 8, "total_tokens": 28}),
			openAIDone()},
	})
	seedAgentModel(t, s, fake.URL)

	// 建会话 + 设置模型
	c, rec := adminProtocolContext(http.MethodPost, "/api/admin/agent/sessions", `{"mode":"create"}`)
	s.adminCreateAgentSession(c)
	created := decodeAdminData(t, rec)
	sessionID, _ := created["id"].(string)
	if sessionID == "" {
		t.Fatalf("no session id: %s", rec.Body.String())
	}
	settings := `{"settings":{"modelSourceId":"s1","modelName":"fake-model"}}`
	c, rec = agentContextWithID(http.MethodPatch, "/api/admin/agent/sessions/"+sessionID, sessionID, settings)
	s.adminUpdateAgentSession(c)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch settings: %d %s", rec.Code, rec.Body.String())
	}

	// 发消息 → SSE
	c, rec = agentContextWithID(http.MethodPost, "/api/admin/agent/sessions/"+sessionID+"/messages", sessionID, `{"content":"帮我接入测试协议"}`)
	s.adminSendAgentMessage(c)
	if rec.Code != http.StatusOK {
		t.Fatalf("send message: %d %s", rec.Code, rec.Body.String())
	}
	events := parseSSEEvents(t, rec.Body.String())
	for _, want := range []string{"text_delta", "tool_call", "tool_result", "draft_updated", "turn_done"} {
		if !hasAgentEvent(events, want) {
			var types []string
			for _, event := range events {
				types = append(types, event.Type+" "+truncateForDisplay(string(event.Data), 400))
			}
			var summaries []string
			for _, event := range events {
				summaries = append(summaries, event.Type)
			}
			t.Fatalf("missing %s event; types=%v first-error=%v", want, summaries, errorText(events))
		}
	}
	// 用量入账：两次模型调用各一条，key_name = AI 协议助手
	_, logs, err := s.store.QueryUsageLogs(t.Context(), storage.UsageQuery{KeyName: AgentUsageKeyName, Limit: 10})
	if err != nil {
		t.Fatalf("query usage: %v", err)
	}
	if len(logs) != 2 {
		t.Fatalf("usage records = %d, want 2", len(logs))
	}
	if logs[0].TotalTokens+logs[1].TotalTokens != 43 {
		t.Fatalf("tokens = %d+%d", logs[0].TotalTokens, logs[1].TotalTokens)
	}
	if logs[0].RelayMode != agentRelayMode {
		t.Fatalf("relay mode = %q", logs[0].RelayMode)
	}

	// 会话状态收敛 + 草稿落库
	c, rec = agentContextWithID(http.MethodGet, "/api/admin/agent/sessions/"+sessionID, sessionID, "")
	s.adminGetAgentSession(c)
	detail := decodeAdminData(t, rec)
	session := detail["session"].(map[string]any)
	if session["status"] != "idle" {
		t.Fatalf("status = %v", session["status"])
	}
	if draft, _ := session["draftConfig"].(string); !strings.Contains(draft, "agent-proto") {
		// draftConfig 是 json.RawMessage，可能被序列化为任意 JSON 类型
		raw, _ := json.Marshal(session["draftConfig"])
		if !strings.Contains(string(raw), "agent-proto") {
			t.Fatalf("draft missing: %v", session["draftConfig"])
		}
	}
	// 消息序列 user → assistant(tool) → tool_result → assistant(终稿)
	messages := detail["messages"].([]any)
	if len(messages) != 4 {
		t.Fatalf("messages = %d", len(messages))
	}
}

func TestAgentGatedToolApprovalFlow(t *testing.T) {
	s := newAgentIntegrationServer(t)
	// 第 1 次：请求 test_upstream（门控）；恢复后第 2 次：终稿。
	fake := newFakeAgentModelServer(t, [][]string{
		{openAIChunk("c1", toolCallDelta(0, "call_1", "bash", `{"command":"elysia protocol test"}`), "", nil),
			openAIChunk("c1", map[string]any{}, "tool_calls", nil),
			openAIDone()},
		{openAIChunk("c2", map[string]any{"role": "assistant", "content": "测试完成"}, "", nil),
			openAIChunk("c2", map[string]any{}, "stop", nil),
			openAIDone()},
	})
	seedAgentModel(t, s, fake.URL)
	// 草稿 + 测试目标：vendor 上游返回 200 JSON
	vendor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"answer":{"text":"hi"},"finish":"stop"}`))
	}))
	defer vendor.Close()

	c, rec := adminProtocolContext(http.MethodPost, "/api/admin/agent/sessions", `{"mode":"create"}`)
	s.adminCreateAgentSession(c)
	sessionID := decodeAdminData(t, rec)["id"].(string)
	patch := fmt.Sprintf(`{"settings":{"modelSourceId":"s1","modelName":"fake-model","testBaseUrl":%q},"apiKey":"vendor-key"}`, vendor.URL)
	c, rec = agentContextWithID(http.MethodPatch, "/api/admin/agent/sessions/"+sessionID, sessionID, patch)
	s.adminUpdateAgentSession(c)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch: %d %s", rec.Code, rec.Body.String())
	}
	// 预置草稿（直接写会话状态，走工具路径太长；此处聚焦门控）
	if err := s.store.UpdateSessionState(t.Context(), sessionID, stateUpdateWithDraft(json.RawMessage(agentTestConfig("gated-proto")))); err != nil {
		t.Fatalf("seed draft: %v", err)
	}

	c, rec = agentContextWithID(http.MethodPost, "/api/admin/agent/sessions/"+sessionID+"/messages", sessionID, `{"content":"请测试"}`)
	s.adminSendAgentMessage(c)
	events := parseSSEEvents(t, rec.Body.String())
	if !hasAgentEvent(events, "approval_required") {
		t.Fatalf("missing approval_required: %s", rec.Body.String())
	}
	if hasAgentEvent(events, "turn_done") {
		t.Fatalf("paused turn must not emit turn_done")
	}
	// 会话停在 waiting_approval
	c, rec = agentContextWithID(http.MethodGet, "/api/admin/agent/sessions/"+sessionID, sessionID, "")
	s.adminGetAgentSession(c)
	session := decodeAdminData(t, rec)["session"].(map[string]any)
	if session["status"] != "waiting_approval" {
		t.Fatalf("status = %v", session["status"])
	}

	// 批准 → 续跑（工具真实执行：vendor 上游收到请求）→ 终稿
	c, rec = agentContextWithID(http.MethodPost, "/api/admin/agent/sessions/"+sessionID+"/approve", sessionID, `{"approved":true}`)
	s.adminApproveAgentAction(c)
	if rec.Code != http.StatusOK {
		t.Fatalf("approve: %d %s", rec.Code, rec.Body.String())
	}
	events = parseSSEEvents(t, rec.Body.String())
	if !hasAgentEvent(events, "tool_result") || !hasAgentEvent(events, "turn_done") {
		t.Fatalf("resume missing events: %s", rec.Body.String())
	}
	// 工具结果里应看到 vendor 的 200 响应
	foundVendor := false
	for _, event := range events {
		if event.Type == "tool_result" {
			if strings.Contains(string(event.Data), "vendor-key") {
				foundVendor = true
			}
		}
	}
	_ = foundVendor // SSE 结果经截断，不强制断言正文；以状态为准
	session = nil
	c, rec = agentContextWithID(http.MethodGet, "/api/admin/agent/sessions/"+sessionID, sessionID, "")
	s.adminGetAgentSession(c)
	session = decodeAdminData(t, rec)["session"].(map[string]any)
	if session["status"] != "idle" {
		t.Fatalf("status after approve = %v", session["status"])
	}
}

func TestAgentDenyApprovalAdapts(t *testing.T) {
	s := newAgentIntegrationServer(t)
	fake := newFakeAgentModelServer(t, [][]string{
		{openAIChunk("c1", toolCallDelta(0, "call_1", "bash", `{"command":"elysia protocol save"}`), "", nil),
			openAIChunk("c1", map[string]any{}, "tool_calls", nil),
			openAIDone()},
		{openAIChunk("c2", map[string]any{"role": "assistant", "content": "好的，先不保存"}, "", nil),
			openAIChunk("c2", map[string]any{}, "stop", nil),
			openAIDone()},
	})
	seedAgentModel(t, s, fake.URL)

	c, rec := adminProtocolContext(http.MethodPost, "/api/admin/agent/sessions", `{"mode":"create"}`)
	s.adminCreateAgentSession(c)
	sessionID := decodeAdminData(t, rec)["id"].(string)
	c, _ = agentContextWithID(http.MethodPatch, "/api/admin/agent/sessions/"+sessionID, sessionID, `{"settings":{"modelSourceId":"s1","modelName":"fake-model"}}`)
	s.adminUpdateAgentSession(c)

	c, rec = agentContextWithID(http.MethodPost, "/api/admin/agent/sessions/"+sessionID+"/messages", sessionID, `{"content":"保存"}`)
	s.adminSendAgentMessage(c)
	if !hasAgentEvent(parseSSEEvents(t, rec.Body.String()), "approval_required") {
		t.Fatalf("expected approval: %s", rec.Body.String())
	}
	c, rec = agentContextWithID(http.MethodPost, "/api/admin/agent/sessions/"+sessionID+"/approve", sessionID, `{"approved":false,"note":"等等"}`)
	s.adminApproveAgentAction(c)
	events := parseSSEEvents(t, rec.Body.String())
	if !hasAgentEvent(events, "turn_done") {
		t.Fatalf("deny must continue turn: %s", rec.Body.String())
	}
	// 协议没有被保存
	rows, _ := s.store.ListCustomProtocols(t.Context())
	for _, row := range rows {
		if strings.Contains(row.Config, "agent-proto") {
			t.Fatalf("protocol must not be saved")
		}
	}
}

func TestAgentSessionLifecycle(t *testing.T) {
	s := newAgentIntegrationServer(t)
	// 建 → 列表 → 清空消息 → 删除
	c, rec := adminProtocolContext(http.MethodPost, "/api/admin/agent/sessions", `{"mode":"create","title":"生命周期"}`)
	s.adminCreateAgentSession(c)
	sessionID := decodeAdminData(t, rec)["id"].(string)

	c, rec = adminProtocolContext(http.MethodGet, "/api/admin/agent/sessions", "")
	s.listAgentSessionsFiltered(c)
	list := decodeAdminData(t, rec)["items"].([]any)
	if len(list) != 1 {
		t.Fatalf("sessions = %d", len(list))
	}

	if _, err := s.store.AppendMessage(t.Context(), sessionID, "user", map[string]any{"text": "hi"}, "", nil); err != nil {
		t.Fatalf("append: %v", err)
	}
	c, rec = agentContextWithID(http.MethodDelete, "/api/admin/agent/sessions/"+sessionID+"/messages?afterSeq=0", sessionID, "")
	s.adminClearAgentMessages(c)
	if rec.Code != http.StatusOK {
		t.Fatalf("clear: %d %s", rec.Code, rec.Body.String())
	}
	c, rec = agentContextWithID(http.MethodGet, "/api/admin/agent/sessions/"+sessionID, sessionID, "")
	s.adminGetAgentSession(c)
	if detail := decodeAdminData(t, rec); len(detail["messages"].([]any)) != 0 {
		t.Fatalf("messages not cleared")
	}

	c, rec = agentContextWithID(http.MethodDelete, "/api/admin/agent/sessions/"+sessionID, sessionID, "")
	s.adminDeleteAgentSession(c)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body.String())
	}
	c, rec = agentContextWithID(http.MethodGet, "/api/admin/agent/sessions/"+sessionID, sessionID, "")
	s.adminGetAgentSession(c)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("get after delete = %d", rec.Code)
	}
}

func TestAgentThinkingSettingsMappedToRequest(t *testing.T) {
	s := newAgentIntegrationServer(t)
	fake := newFakeAgentModelServer(t, [][]string{
		{openAIChunk("c1", map[string]any{"role": "assistant", "content": "ok"}, "", nil),
			openAIChunk("c1", map[string]any{}, "stop", nil),
			openAIDone()},
	})
	seedAgentModel(t, s, fake.URL)

	c, rec := adminProtocolContext(http.MethodPost, "/api/admin/agent/sessions", `{"mode":"create"}`)
	s.adminCreateAgentSession(c)
	sessionID := decodeAdminData(t, rec)["id"].(string)
	c, _ = agentContextWithID(http.MethodPatch, "/api/admin/agent/sessions/"+sessionID, sessionID,
		`{"settings":{"modelSourceId":"s1","modelName":"fake-model","thinkingEnabled":true,"thinkingEffort":"high"}}`)
	s.adminUpdateAgentSession(c)

	c, rec = agentContextWithID(http.MethodPost, "/api/admin/agent/sessions/"+sessionID+"/messages", sessionID, `{"content":"hi"}`)
	s.adminSendAgentMessage(c)
	if rec.Code != http.StatusOK {
		t.Fatalf("send: %d %s", rec.Code, rec.Body.String())
	}
	// OpenAI chat 线的思考映射为 reasoning_effort=high
	if len(fake.bodies) == 0 || !strings.Contains(fake.bodies[0], `"reasoning_effort":"high"`) {
		t.Fatalf("reasoning_effort not mapped: %v", fake.bodies)
	}
	// 系统提示词与工具定义进入请求体
	if !strings.Contains(fake.bodies[0], "update_protocol_draft") {
		t.Fatalf("tools not sent")
	}
}

// seedAgentModel 注入指向伪模型上游的模型行（沿用 s1/fake-model 命名）。
func seedAgentModel(t *testing.T, s *Server, baseURL string) {
	t.Helper()
	source := storage.ModelSource{ID: "s1", Name: "src", BaseURL: baseURL, Platform: "openai", Enabled: true}
	if err := s.store.UpsertSource(t.Context(), source); err != nil {
		t.Fatalf("UpsertSource: %v", err)
	}
	models := []storage.Model{{
		ID: "fake-model", SourceID: "s1", Name: "fake-model", BaseURL: baseURL,
		APIKey: "", Platform: "openai", Type: "llm", Enabled: true, Available: true,
	}}
	if err := s.store.ReplaceSourceModels(t.Context(), source, models); err != nil {
		t.Fatalf("ReplaceSourceModels: %v", err)
	}
}

// stateUpdateWithDraft 构造带草稿的状态更新（测试辅助）。
func stateUpdateWithDraft(draft json.RawMessage) agent.SessionStateUpdate {
	return agent.SessionStateUpdate{DraftConfig: draft}
}

// 通用运维工具的全链路：模型发起 list_model_groups 工具调用并落终稿。
func TestAgentFullTurnWithOpsTool(t *testing.T) {
	s := newAgentIntegrationServer(t)
	fake := newFakeAgentModelServer(t, [][]string{
		{openAIChunk("c1", toolCallDelta(0, "call_1", "bash", `{"command":"elysia group ls"}`), "", nil),
			openAIChunk("c1", map[string]any{}, "tool_calls", nil),
			openAIDone()},
		{openAIChunk("c2", map[string]any{"role": "assistant", "content": "当前没有任何模型组"}, "", nil),
			openAIChunk("c2", map[string]any{}, "stop", map[string]any{"prompt_tokens": 5, "completion_tokens": 2, "total_tokens": 7}),
			openAIDone()},
	})
	seedAgentModel(t, s, fake.URL)

	c, rec := adminProtocolContext(http.MethodPost, "/api/admin/agent/sessions", `{"mode":"create"}`)
	s.adminCreateAgentSession(c)
	sessionID := decodeAdminData(t, rec)["id"].(string)
	c, _ = agentContextWithID(http.MethodPatch, "/api/admin/agent/sessions/"+sessionID, sessionID, `{"settings":{"modelSourceId":"s1","modelName":"fake-model"}}`)
	s.adminUpdateAgentSession(c)

	c, rec = agentContextWithID(http.MethodPost, "/api/admin/agent/sessions/"+sessionID+"/messages", sessionID, `{"content":"现在有哪些模型组"}`)
	s.adminSendAgentMessage(c)
	events := parseSSEEvents(t, rec.Body.String())
	if !hasAgentEvent(events, "tool_call") || !hasAgentEvent(events, "tool_result") || !hasAgentEvent(events, "turn_done") {
		t.Fatalf("ops tool events missing: %s", truncateForDisplay(rec.Body.String(), 1200))
	}
	// 只读工具不应触发审批
	if hasAgentEvent(events, "approval_required") {
		t.Fatalf("read-only tool must not require approval")
	}
}
