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

	// 批准 → 续跑（工具真实执行：vendor 上游收到请求）→ 终稿。
	// SSE 结果正文经截断，不逐字断言；以事件面与状态为准。
	c, rec = agentContextWithID(http.MethodPost, "/api/admin/agent/sessions/"+sessionID+"/approve", sessionID, `{"approved":true}`)
	s.adminApproveAgentAction(c)
	if rec.Code != http.StatusOK {
		t.Fatalf("approve: %d %s", rec.Code, rec.Body.String())
	}
	events = parseSSEEvents(t, rec.Body.String())
	if !hasAgentEvent(events, "tool_result") || !hasAgentEvent(events, "turn_done") {
		t.Fatalf("resume missing events: %s", rec.Body.String())
	}
	c, rec = agentContextWithID(http.MethodGet, "/api/admin/agent/sessions/"+sessionID, sessionID, "")
	s.adminGetAgentSession(c)
	session = decodeAdminData(t, rec)["session"].(map[string]any)
	if session["status"] != "idle" {
		t.Fatalf("status after approve = %v", session["status"])
	}
}

// 审批卡出口：待批命令打码（敏感 flag 不得经 Reason 泄露），同批多个
// bash 调用的门控命令全部点名（用户所见即所批）。
func TestAgentApprovalReasonMaskingAndBatchScope(t *testing.T) {
	s := newAgentIntegrationServer(t)
	fake := newFakeAgentModelServer(t, [][]string{
		{openAIChunk("c1", toolCallDelta(0, "call_1", "bash", `{"command":"elysia source create --name x --base-url https://u.io --api-key sk-live-789"}`), "", nil),
			openAIChunk("c1", toolCallDelta(1, "call_2", "bash", `{"command":"elysia key delete --name prod-key"}`), "", nil),
			openAIChunk("c1", map[string]any{}, "tool_calls", nil),
			openAIDone()},
	})
	seedAgentModel(t, s, fake.URL)

	c, rec := adminProtocolContext(http.MethodPost, "/api/admin/agent/sessions", `{"mode":"create"}`)
	s.adminCreateAgentSession(c)
	sessionID := decodeAdminData(t, rec)["id"].(string)
	c, _ = agentContextWithID(http.MethodPatch, "/api/admin/agent/sessions/"+sessionID, sessionID, `{"settings":{"modelSourceId":"s1","modelName":"fake-model"}}`)
	s.adminUpdateAgentSession(c)

	c, rec = agentContextWithID(http.MethodPost, "/api/admin/agent/sessions/"+sessionID+"/messages", sessionID, `{"content":"配好并清掉旧Key"}`)
	s.adminSendAgentMessage(c)
	events := parseSSEEvents(t, rec.Body.String())
	found := false
	for _, event := range events {
		if event.Type != "approval_required" {
			continue
		}
		found = true
		body := string(event.Data)
		if strings.Contains(body, "sk-live-789") {
			t.Fatalf("approval reason leaked secret: %s", body)
		}
		if !strings.Contains(body, "--api-key ***") {
			t.Fatalf("approval reason missing mask: %s", body)
		}
		// 同批第二个 bash 调用的门控命令也在批准面内，必须点名。
		if !strings.Contains(body, "key delete --name prod-key") {
			t.Fatalf("approval reason missing later gated command: %s", body)
		}
	}
	if !found {
		t.Fatalf("missing approval_required: %s", rec.Body.String())
	}
}

// 旧版本 PendingAction（工具已下线）批准时明确报过期；拒绝仍可收尾。
func TestAgentStalePendingApprovalRejected(t *testing.T) {
	s := newAgentIntegrationServer(t)
	fake := newFakeAgentModelServer(t, [][]string{
		{openAIChunk("c1", map[string]any{"role": "assistant", "content": "旧审批已拒绝"}, "", nil),
			openAIChunk("c1", map[string]any{}, "stop", nil),
			openAIDone()},
	})
	seedAgentModel(t, s, fake.URL)
	created, _ := s.store.CreateAgentSession(t.Context(), storage.AgentSessionUpsert{Mode: "create"})
	sessionID := created.ID
	waiting := agent.StatusWaitingApproval
	pending := &agent.PendingAction{
		Calls:  []relay.MaheshvaraToolCall{{ID: "call_1", Type: "function", Name: "update_model_source", Arguments: json.RawMessage(`{}`)}},
		Reason: "legacy pending",
	}
	if err := s.store.UpdateSessionState(t.Context(), sessionID, agent.SessionStateUpdate{Status: &waiting, PendingAction: pending}); err != nil {
		t.Fatalf("seed pending: %v", err)
	}

	// 批准 → 409 stale_pending（而非静默 unknown_tool 执行失败）。
	c, rec := agentContextWithID(http.MethodPost, "/api/admin/agent/sessions/"+sessionID+"/approve", sessionID, `{"approved":true}`)
	s.adminApproveAgentAction(c)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "stale_pending") {
		t.Fatalf("stale approve = %d %s", rec.Code, rec.Body.String())
	}

	// 拒绝 → 正常收尾（合成拒绝结果并续跑模型）。
	c, rec = agentContextWithID(http.MethodPost, "/api/admin/agent/sessions/"+sessionID+"/approve", sessionID, `{"approved":false}`)
	s.adminApproveAgentAction(c)
	if rec.Code != http.StatusOK {
		t.Fatalf("deny stale pending = %d %s", rec.Code, rec.Body.String())
	}
	if !hasAgentEvent(parseSSEEvents(t, rec.Body.String()), "turn_done") {
		t.Fatalf("deny must finish turn: %s", rec.Body.String())
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
	for _, effort := range []string{"high", "xhigh"} {
		t.Run(effort, func(t *testing.T) {
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
				fmt.Sprintf(`{"settings":{"modelSourceId":"s1","modelName":"fake-model","thinkingEnabled":true,"thinkingEffort":%q}}`, effort))
			s.adminUpdateAgentSession(c)

			c, rec = agentContextWithID(http.MethodPost, "/api/admin/agent/sessions/"+sessionID+"/messages", sessionID, `{"content":"hi"}`)
			s.adminSendAgentMessage(c)
			if rec.Code != http.StatusOK {
				t.Fatalf("send: %d %s", rec.Code, rec.Body.String())
			}
			// OpenAI chat 线的思考映射为 reasoning_effort 原样档位。
			want := fmt.Sprintf(`"reasoning_effort":%q`, effort)
			if len(fake.bodies) == 0 || !strings.Contains(fake.bodies[0], want) {
				t.Fatalf("reasoning_effort not mapped: want %s in %v", want, fake.bodies)
			}
			// 系统提示词与工具定义进入请求体（bash 是模型可见的操作入口；旧工具
			// 名不得再出现在提示词指令里）。
			if !strings.Contains(fake.bodies[0], `"bash"`) {
				t.Fatalf("tools not sent: %v", fake.bodies)
			}
			if strings.Contains(fake.bodies[0], "update_protocol_draft") {
				t.Fatalf("prompt still references removed tool: %v", fake.bodies)
			}
		})
	}
}

// 白名单：xhigh 合法、未知档位 400。
func TestAgentThinkingEffortWhitelist(t *testing.T) {
	s := newAgentIntegrationServer(t)
	created, err := s.store.CreateAgentSession(t.Context(), storage.AgentSessionUpsert{Mode: "create"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	sessionID := created.ID

	c, rec := agentContextWithID(http.MethodPatch, "/api/admin/agent/sessions/"+sessionID, sessionID,
		`{"settings":{"thinkingEffort":"ultra"}}`)
	s.adminUpdateAgentSession(c)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "invalid_effort") {
		t.Fatalf("unknown effort = %d %s", rec.Code, rec.Body.String())
	}

	c, rec = agentContextWithID(http.MethodPatch, "/api/admin/agent/sessions/"+sessionID, sessionID,
		`{"settings":{"thinkingEffort":"xhigh"}}`)
	s.adminUpdateAgentSession(c)
	if rec.Code != http.StatusOK {
		t.Fatalf("xhigh patch = %d %s", rec.Code, rec.Body.String())
	}
	session, _ := s.store.GetSession(t.Context(), sessionID)
	if session.Settings.ThinkingEffort != "xhigh" {
		t.Fatalf("effort = %q", session.Settings.ThinkingEffort)
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

// bash 全链运维 e2e：空清单补救文案 → 批处理建组（失败回退策略）+ 建指定
// 明文的 Key（审批暂停）→ 批准续跑 → 验证组与 Key 落库。锁住提示词重构
// 后模型可见的运行时行为（空结果指引、明文照建、策略语义、审批面）。
func TestAgentCLIOpsChainE2E(t *testing.T) {
	s := newAgentIntegrationServer(t)
	ctx := t.Context()
	// 预置两个源：s0 空（测空清单补救文案）、s1 带手动模型 m1。
	if err := s.store.UpsertSource(ctx, storage.ModelSource{ID: "s0", Name: "空源", BaseURL: "https://s0.example", Platform: "openai", Enabled: true}); err != nil {
		t.Fatalf("seed s0: %v", err)
	}
	if err := s.store.UpsertSource(ctx, storage.ModelSource{ID: "s1", Name: "主力", BaseURL: "https://s1.example", Platform: "openai", Enabled: true,
		ManualModels: []storage.Model{{SourceID: "s1", Name: "m1"}}}); err != nil {
		t.Fatalf("seed s1: %v", err)
	}

	fake := newFakeAgentModelServer(t, [][]string{
		{ // 第 1 轮：查空源模型清单
			openAIChunk("c1", toolCallDelta(0, "call_1", "bash", `{"command":"elysia model ls --source s0"}`), "", nil),
			openAIChunk("c1", map[string]any{}, "tool_calls", nil),
			openAIDone()},
		{ // 第 2 轮：批处理建组（失败回退）+ 建指定明文 Key → 触发审批暂停
			openAIChunk("c2", toolCallDelta(0, "call_2", "bash", `{"command":"elysia group create --name test123 --models s1:m1 --strategy sequential ; elysia key create --name k1 --secret 123 --allowed-groups test123"}`), "", nil),
			openAIChunk("c2", map[string]any{}, "tool_calls", nil),
			openAIDone()},
		{ // 第 3 轮：验证
			openAIChunk("c3", toolCallDelta(0, "call_3", "bash", `{"command":"elysia group ls | grep test123 && elysia key ls"}`), "", nil),
			openAIChunk("c3", map[string]any{}, "tool_calls", nil),
			openAIDone()},
		{ // 终稿
			openAIChunk("c4", map[string]any{"role": "assistant", "content": "组与 Key 已建好"}, "", nil),
			openAIChunk("c4", map[string]any{}, "stop", nil),
			openAIDone()},
	})
	seedAgentModel(t, s, fake.URL)

	c, rec := adminProtocolContext(http.MethodPost, "/api/admin/agent/sessions", `{"mode":"create"}`)
	s.adminCreateAgentSession(c)
	sessionID := decodeAdminData(t, rec)["id"].(string)
	c, _ = agentContextWithID(http.MethodPatch, "/api/admin/agent/sessions/"+sessionID, sessionID,
		`{"settings":{"modelSourceId":"s1","modelName":"fake-model"}}`)
	s.adminUpdateAgentSession(c)

	c, rec = agentContextWithID(http.MethodPost, "/api/admin/agent/sessions/"+sessionID+"/messages", sessionID, `{"content":"建组建Key"}`)
	s.adminSendAgentMessage(c)
	events := parseSSEEvents(t, rec.Body.String())
	// 第 1 轮结果：空清单补救文案进对话。
	foundEmptyHint := false
	for _, event := range events {
		if event.Type == "tool_result" && strings.Contains(string(event.Data), "本地缓存清单为空") {
			foundEmptyHint = true
		}
	}
	if !foundEmptyHint {
		t.Fatalf("empty-catalog hint missing: %s", rec.Body.String())
	}
	if !hasAgentEvent(events, "approval_required") {
		t.Fatalf("batch must pause for approval: %s", rec.Body.String())
	}
	// 批内逐命令进度：tool_progress 事件带 Text（第 N/M 条 + 脱敏命令），
	// 前端据此实时显示执行到哪条命令。
	progressTexts := []string{}
	for _, event := range events {
		if event.Type == "tool_progress" {
			progressTexts = append(progressTexts, string(event.Data))
		}
	}
	joined := strings.Join(progressTexts, "|")
	if !strings.Contains(joined, `"text":"正在执行（1/1）：elysia model ls --source s0"`) {
		t.Fatalf("per-command progress missing: %s", joined)
	}

	// 批准 → 批处理执行 → 验证轮 → 终稿。
	c, rec = agentContextWithID(http.MethodPost, "/api/admin/agent/sessions/"+sessionID+"/approve", sessionID, `{"approved":true}`)
	s.adminApproveAgentAction(c)
	if rec.Code != http.StatusOK {
		t.Fatalf("approve: %d %s", rec.Code, rec.Body.String())
	}
	events = parseSSEEvents(t, rec.Body.String())
	if !hasAgentEvent(events, "turn_done") {
		t.Fatalf("turn must finish: %s", rec.Body.String())
	}

	// 组：名称/策略/成员。
	groups, _ := s.store.ListGroups(ctx)
	var group *storage.ModelGroup
	for i := range groups {
		if groups[i].Name == "test123" {
			group = &groups[i]
		}
	}
	if group == nil || group.Strategy != "sequential" || len(group.Models) != 1 || group.Models[0] != "s1:m1" {
		t.Fatalf("group wrong: %+v", group)
	}
	// Key：明文按给定值创建、组限定正确。
	token, ok, err := s.store.FindAPITokenByName(ctx, "k1")
	if err != nil || !ok {
		t.Fatalf("key k1 missing: %v %v", ok, err)
	}
	if token.Token != "123" {
		t.Fatalf("plaintext must be as-given, got %q", token.Token)
	}
	if len(token.AllowedGroups) != 1 || token.AllowedGroups[0] != "test123" {
		t.Fatalf("allowedGroups = %v", token.AllowedGroups)
	}
}

// 计划模式全链：只读批放行 → 混合批被拒且点名被拒命令与只读连带提示 →
// 按提示拆分重发只读批成功 → update_plan 定稿暂停 → 用户确认关闭计划模式
// → 写入批走 save 审批 → 批准建成。锁住模型在计划模式下的自救闭环。
func TestAgentPlanModeReadOnlySelfRescueE2E(t *testing.T) {
	s := newAgentIntegrationServer(t)
	fake := newFakeAgentModelServer(t, [][]string{
		{ // 第 1 轮：纯只读批 → 计划模式下放行
			openAIChunk("c1", toolCallDelta(0, "call_1", "bash", `{"command":"elysia source ls"}`), "", nil),
			openAIChunk("c1", map[string]any{}, "tool_calls", nil),
			openAIDone()},
		{ // 第 2 轮：混合批（只读 + 门控 refresh）→ 整批拒绝，文案点名
			openAIChunk("c2", toolCallDelta(0, "call_2", "bash", `{"command":"elysia model ls --source s1 ; elysia source refresh --source s1"}`), "", nil),
			openAIChunk("c2", map[string]any{}, "tool_calls", nil),
			openAIDone()},
		{ // 第 3 轮：按提示拆分，重发纯只读 → 成功
			openAIChunk("c3", toolCallDelta(0, "call_3", "bash", `{"command":"elysia model ls --source s1"}`), "", nil),
			openAIChunk("c3", map[string]any{}, "tool_calls", nil),
			openAIDone()},
		{ // 第 4 轮：方案定稿 → plan 型暂停
			openAIChunk("c4", toolCallDelta(0, "call_4", "update_plan", `{"analysis":"源与模型清单已确认","plan":[{"title":"建组 plan-e2e","status":"pending"}],"ready_for_approval":true}`), "", nil),
			openAIChunk("c4", map[string]any{}, "tool_calls", nil),
			openAIDone()},
		{ // 第 5 轮（方案确认后）：写入 → save 审批暂停
			openAIChunk("c5", toolCallDelta(0, "call_5", "bash", `{"command":"elysia group create --name plan-e2e --models s1:fake-model"}`), "", nil),
			openAIChunk("c5", map[string]any{}, "tool_calls", nil),
			openAIDone()},
		{ // 终稿
			openAIChunk("c6", map[string]any{"role": "assistant", "content": "已建成"}, "", nil),
			openAIChunk("c6", map[string]any{}, "stop", nil),
			openAIDone()},
	})
	seedAgentModel(t, s, fake.URL)

	c, rec := adminProtocolContext(http.MethodPost, "/api/admin/agent/sessions", `{"mode":"create"}`)
	s.adminCreateAgentSession(c)
	sessionID := decodeAdminData(t, rec)["id"].(string)
	// 开计划模式 + 配模型
	c, _ = agentContextWithID(http.MethodPatch, "/api/admin/agent/sessions/"+sessionID, sessionID,
		`{"settings":{"modelSourceId":"s1","modelName":"fake-model","planMode":true}}`)
	s.adminUpdateAgentSession(c)

	// 第 1 轮：只读放行；第 2 轮：混合批拒绝 → 第 4 轮方案暂停（两次流）。
	c, rec = agentContextWithID(http.MethodPost, "/api/admin/agent/sessions/"+sessionID+"/messages", sessionID, `{"content":"帮我建组"}`)
	s.adminSendAgentMessage(c)
	events := parseSSEEvents(t, rec.Body.String())

	results := ""
	for _, event := range events {
		if event.Type == "tool_result" {
			results += string(event.Data)
		}
	}
	if !strings.Contains(results, "执行 1 条命令") {
		t.Fatalf("read-only batch must run in plan mode: %s", results)
	}
	if !strings.Contains(results, "source refresh") || !strings.Contains(results, "只读命令也被连带跳过") {
		t.Fatalf("mixed batch denial must name the blocked command and the read-only side effect: %s", results)
	}
	if !strings.Contains(results, "fake-model") {
		t.Fatalf("resplit read-only batch must return models: %s", results)
	}
	// plan 型暂停
	pausedForPlan := false
	for _, event := range events {
		if event.Type == "approval_required" && strings.Contains(string(event.Data), `"kind":"plan"`) {
			pausedForPlan = true
		}
	}
	if !pausedForPlan {
		t.Fatalf("ready_for_approval must pause with plan pending: %s", rec.Body.String())
	}

	// 确认方案 → 关闭计划模式 → 第 5 轮写入触发 save 审批。
	c, rec = agentContextWithID(http.MethodPost, "/api/admin/agent/sessions/"+sessionID+"/approve", sessionID, `{"approved":true}`)
	s.adminApproveAgentAction(c)
	events = parseSSEEvents(t, rec.Body.String())
	if !hasAgentEvent(events, "approval_required") {
		t.Fatalf("write after plan approval must pause on save: %s", rec.Body.String())
	}
	session, _ := s.store.GetSession(t.Context(), sessionID)
	if session.Settings.PlanMode {
		t.Fatal("plan mode must be off after plan approval")
	}

	// 批准写入 → 组建成。
	c, rec = agentContextWithID(http.MethodPost, "/api/admin/agent/sessions/"+sessionID+"/approve", sessionID, `{"approved":true}`)
	s.adminApproveAgentAction(c)
	events = parseSSEEvents(t, rec.Body.String())
	if !hasAgentEvent(events, "turn_done") {
		t.Fatalf("turn must finish: %s", rec.Body.String())
	}
	groups, _ := s.store.ListGroups(t.Context())
	found := false
	for _, group := range groups {
		if group.Name == "plan-e2e" {
			found = true
		}
	}
	if !found {
		t.Fatalf("group plan-e2e not created: %+v", groups)
	}
}
