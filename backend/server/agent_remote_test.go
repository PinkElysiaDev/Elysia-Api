package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elysia-api/backend/agent"
	"github.com/elysia-api/backend/storage"

	"github.com/gin-gonic/gin"
)

// 远程面测试：API Key 作用域鉴权、MCP（legacy/modern 双世代）、A2A
//（v0.3/v1.0 双线、审批恢复）、REST 列表过滤分页。

// ---- 上下文构造辅助 ----

func remoteContext(method, target, body string, headers map[string]string) (*gin.Context, *httptest.ResponseRecorder) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	c.Request = req
	return c, rec
}

func mcpHeaders(extra map[string]string) map[string]string {
	headers := map[string]string{"Accept": "application/json, text/event-stream"}
	for key, value := range extra {
		headers[key] = value
	}
	return headers
}

func mcpCall(t *testing.T, s *Server, body string, extraHeaders map[string]string) (int, map[string]any, string) {
	t.Helper()
	c, rec := remoteContext(http.MethodPost, "/mcp", body, mcpHeaders(extraHeaders))
	s.handleMCP(c)
	var decoded map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &decoded)
	return rec.Code, decoded, rec.Body.String()
}

func mcpRequest(id any, method string, params any) string {
	payload := map[string]any{"jsonrpc": "2.0", "method": method}
	if id != nil {
		payload["id"] = id
	}
	if params != nil {
		payload["params"] = params
	}
	encoded, _ := json.Marshal(payload)
	return string(encoded)
}

func a2aCall(t *testing.T, s *Server, method string, params any, version string) (int, map[string]any, string) {
	t.Helper()
	headers := map[string]string{}
	if version != "" {
		headers["A2A-Version"] = version
	}
	c, rec := remoteContext(http.MethodPost, "/a2a", mcpRequest(1, method, params), headers)
	s.handleA2A(c)
	var decoded map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &decoded)
	return rec.Code, decoded, rec.Body.String()
}

// parseA2AFrames 解析 SSE data 帧（每帧一个 JSON-RPC response）。
func parseA2AFrames(t *testing.T, body string) []map[string]any {
	t.Helper()
	frames := []map[string]any{}
	for _, block := range strings.Split(body, "\n\n") {
		for _, line := range strings.Split(block, "\n") {
			if strings.HasPrefix(line, "data: ") {
				var decoded map[string]any
				if err := json.Unmarshal([]byte(line[6:]), &decoded); err == nil {
					frames = append(frames, decoded)
				}
			}
		}
	}
	return frames
}

func a2aFrameStates(frames []map[string]any) []string {
	states := []string{}
	for _, frame := range frames {
		result, _ := frame["result"].(map[string]any)
		if result == nil {
			continue
		}
		if status, ok := result["status"].(map[string]any); ok {
			if state, ok := status["state"].(string); ok {
				states = append(states, state)
			}
		}
	}
	return states
}

// ---- 鉴权与总开关 ----

func TestAgentRemoteAuthScopes(t *testing.T) {
	s := newAgentIntegrationServer(t)
	ctx := context.Background()
	if err := s.store.UpsertAPIToken(ctx, storage.APIToken{Name: "scoped", Token: "tok-scoped", Enabled: true, Scopes: []string{storage.TokenScopeAgent}}); err != nil {
		t.Fatalf("seed scoped: %v", err)
	}
	if err := s.store.UpsertAPIToken(ctx, storage.APIToken{Name: "plain", Token: "tok-plain", Enabled: true}); err != nil {
		t.Fatalf("seed plain: %v", err)
	}
	s.invalidateRouteCache()

	router := gin.New()
	stub := func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) }
	router.GET("/probe", s.agentRemoteGate(), s.agentRemoteAuth(), stub)

	get := func(auth string) int {
		req := httptest.NewRequest(http.MethodGet, "/probe", nil)
		if auth != "" {
			req.Header.Set("Authorization", "Bearer "+auth)
		}
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec.Code
	}
	if code := get(""); code != http.StatusUnauthorized {
		t.Fatalf("no token = %d, want 401", code)
	}
	if code := get("tok-plain"); code != http.StatusForbidden {
		t.Fatalf("no scope = %d, want 403", code)
	}
	if code := get("tok-scoped"); code != http.StatusOK {
		t.Fatalf("scoped = %d, want 200", code)
	}

	// 未知作用域在写路径被清洗（NormalizeScopes 只认 agent）。
	if err := s.store.UpsertAPIToken(ctx, storage.APIToken{Name: "dirty", Token: "tok-dirty", Enabled: true, Scopes: []string{"admin", "AGENT"}}); err != nil {
		t.Fatalf("seed dirty: %v", err)
	}
	dirty, _, _ := s.store.FindAPITokenByName(ctx, "dirty")
	if len(dirty.Scopes) != 1 || dirty.Scopes[0] != storage.TokenScopeAgent {
		t.Fatalf("scopes not normalized: %v", dirty.Scopes)
	}

	// 总开关关闭 → 404。
	s.config.AgentRemote.Enabled = new(bool)
	*s.config.AgentRemote.Enabled = false
	if code := get("tok-scoped"); code != http.StatusNotFound {
		t.Fatalf("gate off = %d, want 404", code)
	}
}

// ---- MCP ----

func TestMCPLegacyHandshakeAndToolsList(t *testing.T) {
	s := newOpsTestServer(t)
	headers := map[string]string{"MCP-Protocol-Version": "2025-06-18"}

	code, result, _ := mcpCall(t, s, mcpRequest(1, "initialize", map[string]any{
		"protocolVersion": "2025-06-18", "capabilities": map[string]any{},
		"clientInfo": map[string]any{"name": "test", "version": "0"},
	}), headers)
	if code != http.StatusOK || result["result"] == nil {
		t.Fatalf("initialize: %d %v", code, result)
	}
	init := result["result"].(map[string]any)
	if init["protocolVersion"] != "2025-06-18" {
		t.Fatalf("protocolVersion = %v", init["protocolVersion"])
	}

	// 通知 → 202 无 body。
	c, rec := remoteContext(http.MethodPost, "/mcp", mcpRequest(nil, "notifications/initialized", nil), mcpHeaders(headers))
	s.handleMCP(c)
	if rec.Code != http.StatusAccepted || rec.Body.Len() != 0 {
		t.Fatalf("initialized notification: %d %q", rec.Code, rec.Body.String())
	}

	_, ping, _ := mcpCall(t, s, mcpRequest(2, "ping", nil), headers)
	if ping["result"] == nil {
		t.Fatalf("ping: %v", ping)
	}

	_, listed, _ := mcpCall(t, s, mcpRequest(3, "tools/list", nil), headers)
	tools := listed["result"].(map[string]any)["tools"].([]any)
	names := map[string]bool{}
	for _, item := range tools {
		tool := item.(map[string]any)
		names[tool["name"].(string)] = true
	}
	for _, want := range []string{"agent_send_message", "agent_respond", "agent_list_sessions", "agent_stop"} {
		if !names[want] {
			t.Fatalf("tools/list missing %s", want)
		}
	}

	// 未知方法/未知工具。
	_, missing, _ := mcpCall(t, s, mcpRequest(4, "resources/list", nil), headers)
	if missing["error"] == nil {
		t.Fatalf("unknown method must error: %v", missing)
	}
	_, unknownTool, _ := mcpCall(t, s, mcpRequest(5, "tools/call", map[string]any{"name": "nope"}), headers)
	if unknownTool["error"] == nil {
		t.Fatalf("unknown tool must error: %v", unknownTool)
	}

	// Accept 不全 → 406。
	c, rec = remoteContext(http.MethodPost, "/mcp", mcpRequest(6, "ping", nil), map[string]string{"Accept": "application/json"})
	s.handleMCP(c)
	if rec.Code != http.StatusNotAcceptable {
		t.Fatalf("partial accept = %d", rec.Code)
	}
	// GET → 405。
	c, rec = remoteContext(http.MethodGet, "/mcp", "", mcpHeaders(headers))
	s.handleMCP(c)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET = %d", rec.Code)
	}
}

func TestMCPQuickToolsCall(t *testing.T) {
	s := newAgentIntegrationServer(t)
	// 建两个会话，过滤 + 会话创建工具。
	if _, err := s.createRemoteAgentSession(context.Background(), "甲", "create", "", nil); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := s.createRemoteAgentSession(context.Background(), "乙", "create", "", nil); err != nil {
		t.Fatalf("create: %v", err)
	}
	_, result, _ := mcpCall(t, s, mcpRequest(1, "tools/call", map[string]any{
		"name": "agent_list_sessions", "arguments": map[string]any{"limit": 1},
	}), nil)
	payload := result["result"].(map[string]any)
	if payload["isError"] == true {
		t.Fatalf("list errored: %v", payload)
	}
	structured := payload["structuredContent"].(map[string]any)
	if structured["total"] != float64(2) {
		t.Fatalf("total = %v", structured["total"])
	}
	if len(structured["items"].([]any)) != 1 {
		t.Fatalf("limit not applied: %v", structured)
	}
}

func TestMCPSendMessageStreaming(t *testing.T) {
	s := newAgentIntegrationServer(t)
	fake := newFakeAgentModelServer(t, [][]string{
		{openAIChunk("c1", map[string]any{"role": "assistant", "content": "远程接入成功"}, "", nil),
			openAIChunk("c1", map[string]any{}, "stop", nil),
			openAIDone()},
	})
	seedAgentModel(t, s, fake.URL)
	session, err := s.createRemoteAgentSession(context.Background(), "远程", "create", "", &agent.Settings{ModelSourceID: "s1", ModelName: "fake-model"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	c, rec := remoteContext(http.MethodPost, "/mcp", mcpRequest(7, "tools/call", map[string]any{
		"name":      "agent_send_message",
		"arguments": map[string]any{"sessionId": session.ID, "text": "你好"},
		"_meta":     map[string]any{"progressToken": "tok-1"},
	}), mcpHeaders(nil))
	s.handleMCP(c)
	body := rec.Body.String()
	frames := parseA2AFrames(t, body)
	if len(frames) < 2 {
		t.Fatalf("expected progress + final frames: %s", body)
	}
	// progress 通知帧 + 终帧响应。
	sawProgress := false
	for _, frame := range frames {
		if method, ok := frame["method"].(string); ok && method == "notifications/progress" {
			sawProgress = true
		}
	}
	if !sawProgress {
		t.Fatalf("missing progress notification: %s", body)
	}
	final := frames[len(frames)-1]["result"].(map[string]any)
	if final["isError"] == true {
		t.Fatalf("tool errored: %v", final)
	}
	structured := final["structuredContent"].(map[string]any)
	if structured["reply"] != "远程接入成功" || structured["status"] != agent.StatusIdle {
		t.Fatalf("outcome wrong: %v", structured)
	}
}

func TestMCPModernEra(t *testing.T) {
	s := newOpsTestServer(t)
	modernParams := func(version string) map[string]any {
		return map[string]any{"_meta": map[string]any{
			"io.modelcontextprotocol/protocolVersion":    version,
			"io.modelcontextprotocol/clientCapabilities": map[string]any{},
		}}
	}
	headers := map[string]string{"MCP-Protocol-Version": mcpModernProtocolVersion, "Mcp-Method": "server/discover"}

	code, result, _ := mcpCall(t, s, mcpRequest(1, "server/discover", modernParams(mcpModernProtocolVersion)), headers)
	if code != http.StatusOK {
		t.Fatalf("discover: %d %v", code, result)
	}
	discovered := result["result"].(map[string]any)
	if _, ok := discovered["supportedVersions"]; !ok {
		t.Fatalf("discover result wrong: %v", discovered)
	}

	// 头与 body 版本不一致 → -32020。
	_, mismatch, _ := mcpCall(t, s, mcpRequest(2, "ping", modernParams("2025-06-18")), headers)
	if errObj := mismatch["error"].(map[string]any); errObj == nil || errObj["code"] != float64(mcpErrHeaderMismatch) {
		t.Fatalf("header mismatch: %v", mismatch)
	}

	// 不支持的版本 → -32022 + supported。
	_, unsupported, _ := mcpCall(t, s, mcpRequest(3, "ping", modernParams("1999-01-01")), map[string]string{"MCP-Protocol-Version": "1999-01-01"})
	errObj := unsupported["error"].(map[string]any)
	if errObj["code"] != float64(mcpErrUnsupportedVersion) {
		t.Fatalf("unsupported version: %v", unsupported)
	}

	// modern tools/list 带 resultType 与 ttlMs。
	_, listed, _ := mcpCall(t, s, mcpRequest(4, "tools/list", modernParams(mcpModernProtocolVersion)),
		map[string]string{"MCP-Protocol-Version": mcpModernProtocolVersion, "Mcp-Method": "tools/list"})
	listResult := listed["result"].(map[string]any)
	if listResult["resultType"] != "complete" || listResult["ttlMs"] == nil {
		t.Fatalf("modern tools/list wrong: %v", listResult)
	}
}

// ---- A2A ----

func TestA2AAgentCard(t *testing.T) {
	s := newOpsTestServer(t)
	c, rec := remoteContext(http.MethodGet, "/.well-known/agent-card.json", "", nil)
	c.Request.Host = "gw.example.com"
	s.handleAgentCard(c)
	var card map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &card); err != nil {
		t.Fatalf("card: %v", err)
	}
	if card["protocolVersion"] != a2aVersionV03 || !strings.HasSuffix(card["url"].(string), "/a2a") {
		t.Fatalf("v0.3 card wrong: %v", card)
	}

	c, rec = remoteContext(http.MethodGet, "/.well-known/agent-card.json", "", map[string]string{"A2A-Version": a2aVersionV10})
	c.Request.Host = "gw.example.com"
	s.handleAgentCard(c)
	var v1Card map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &v1Card)
	if v1Card["supportedInterfaces"] == nil || v1Card["protocolVersion"] != nil {
		t.Fatalf("v1.0 card wrong: %v", v1Card)
	}
}

// a2aMessageSeq 保证每个测试消息的 messageId 唯一——幂等 probe 按 messageId
// 命中，拼串碰撞会让恢复请求被误判为重发。
var a2aMessageSeq atomic.Int64

func a2aMessageParams(contextID, taskID, text string, data any) map[string]any {
	parts := []map[string]any{}
	if text != "" {
		parts = append(parts, map[string]any{"kind": "text", "text": text})
	}
	if data != nil {
		parts = append(parts, map[string]any{"kind": "data", "data": data})
	}
	message := map[string]any{"messageId": fmt.Sprintf("m-%d", a2aMessageSeq.Add(1)), "role": "user", "parts": parts}
	if contextID != "" {
		message["contextId"] = contextID
	}
	if taskID != "" {
		message["taskId"] = taskID
	}
	return map[string]any{"message": message}
}

func TestA2AStreamApprovalResumeFlow(t *testing.T) {
	s := newAgentIntegrationServer(t)
	// 第 1 次：请求门控工具 → input-required；第 2 次：终稿。
	fake := newFakeAgentModelServer(t, [][]string{
		{openAIChunk("c1", toolCallDelta(0, "call_1", "bash", `{"command":"elysia protocol test"}`), "", nil),
			openAIChunk("c1", map[string]any{}, "tool_calls", nil),
			openAIDone()},
		{openAIChunk("c2", map[string]any{"role": "assistant", "content": "测试通过，接入完成"}, "", nil),
			openAIChunk("c2", map[string]any{}, "stop", nil),
			openAIDone()},
	})
	seedAgentModel(t, s, fake.URL)
	vendor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"answer":{"text":"hi"},"finish":"stop"}`))
	}))
	defer vendor.Close()

	session, err := s.createRemoteAgentSession(context.Background(), "a2a", "create", "", nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := s.updateRemoteAgentSession(context.Background(), session.ID, nil, &agent.SettingsPatch{
		ModelSourceID: ptrString("s1"), ModelName: ptrString("fake-model"), TestBaseURL: ptrString(vendor.URL),
	}); err != nil {
		t.Fatalf("patch: %v", err)
	}
	if err := s.store.UpdateSessionState(context.Background(), session.ID, stateUpdateWithDraft(json.RawMessage(agentTestConfig("a2a-proto")))); err != nil {
		t.Fatalf("draft: %v", err)
	}

	// message/stream：新任务 → input-required。
	c, rec := remoteContext(http.MethodPost, "/a2a", mcpRequest(1, "message/stream", a2aMessageParams(session.ID, "", "请测试", nil)), nil)
	s.handleA2A(c)
	frames := parseA2AFrames(t, rec.Body.String())
	if len(frames) == 0 {
		t.Fatalf("no frames: %s", rec.Body.String())
	}
	task := frames[0]["result"].(map[string]any)
	taskID, _ := task["id"].(string)
	if !strings.HasPrefix(taskID, session.ID+":") {
		t.Fatalf("taskId = %q", taskID)
	}
	states := a2aFrameStates(frames)
	if !containsString(states, a2aStateInputRequired) {
		t.Fatalf("missing input-required: %v %s", states, rec.Body.String())
	}

	// tasks/get 快照同状态。
	_, got, _ := a2aCall(t, s, "tasks/get", map[string]any{"id": taskID}, "")
	snapshot := got["result"].(map[string]any)
	status := snapshot["status"].(map[string]any)
	if status["state"] != a2aStateInputRequired {
		t.Fatalf("snapshot state = %v", status)
	}

	// 无 data part 的恢复 → invalid params。
	_, invalid, _ := a2aCall(t, s, "message/send", a2aMessageParams(session.ID, taskID, "同意", nil), "")
	if invalid["error"] == nil {
		t.Fatalf("resume without data part must fail: %v", invalid)
	}

	// data part 批准 → 续跑 → completed + artifact。
	c, rec = remoteContext(http.MethodPost, "/a2a", mcpRequest(2, "message/stream", a2aMessageParams(session.ID, taskID, "", map[string]any{"approved": true})), nil)
	s.handleA2A(c)
	frames = parseA2AFrames(t, rec.Body.String())
	states = a2aFrameStates(frames)
	if !containsString(states, a2aStateCompleted) {
		t.Fatalf("missing completed: %v %s", states, rec.Body.String())
	}
	foundReply := false
	for _, frame := range frames {
		if result, ok := frame["result"].(map[string]any); ok {
			if artifact, ok := result["artifact"].(map[string]any); ok {
				for _, part := range artifact["parts"].([]any) {
					if text := part.(map[string]any)["text"].(string); strings.Contains(text, "测试通过") {
						foundReply = true
					}
				}
			}
		}
	}
	if !foundReply {
		t.Fatalf("reply artifact missing: %s", rec.Body.String())
	}

	// 终态后 tasks/get 仍是完整快照（artifacts 保留）。
	_, got, _ = a2aCall(t, s, "tasks/get", map[string]any{"id": taskID}, "")
	snapshot = got["result"].(map[string]any)
	if len(snapshot["artifacts"].([]any)) == 0 {
		t.Fatalf("artifacts lost: %v", snapshot)
	}
}

func TestA2ASendNonBlockingAndV1Wire(t *testing.T) {
	s := newAgentIntegrationServer(t)
	fake := newFakeAgentModelServer(t, [][]string{
		{openAIChunk("c1", map[string]any{"role": "assistant", "content": "收到"}, "", nil),
			openAIChunk("c1", map[string]any{}, "stop", nil),
			openAIDone()},
		{openAIChunk("c2", map[string]any{"role": "assistant", "content": "v1 收到"}, "", nil),
			openAIChunk("c2", map[string]any{}, "stop", nil),
			openAIDone()},
	})
	seedAgentModel(t, s, fake.URL)
	session, err := s.createRemoteAgentSession(context.Background(), "非阻塞", "create", "", &agent.Settings{ModelSourceID: "s1", ModelName: "fake-model"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// v0.3 message/send：立即返回 working 快照（非阻塞）。
	_, sent, _ := a2aCall(t, s, "message/send", a2aMessageParams(session.ID, "", "你好", nil), "")
	result := sent["result"].(map[string]any)
	state := result["status"].(map[string]any)["state"].(string)
	if state != a2aStateWorking && state != a2aStateCompleted {
		t.Fatalf("send state = %q", state)
	}
	taskID := result["id"].(string)

	// 轮询 tasks/get 到终态（伪模型一轮很快）。
	for i := 0; i < 50; i++ {
		_, got, _ := a2aCall(t, s, "tasks/get", map[string]any{"id": taskID}, "")
		state = got["result"].(map[string]any)["status"].(map[string]any)["state"].(string)
		if state == a2aStateCompleted {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if state != a2aStateCompleted {
		t.Fatalf("task did not complete: %q", state)
	}

	// v1.0 线格式：SendMessage（PascalCase）+ ROLE_* 枚举 + 无 kind Part。
	_, v1Sent, _ := a2aCall(t, s, "SendMessage",
		map[string]any{"message": map[string]any{"messageId": "m-v1", "role": "ROLE_USER",
			"contextId": session.ID, "parts": []map[string]any{{"text": "v1 你好"}}}},
		a2aVersionV10)
	v1Result, ok := v1Sent["result"].(map[string]any)
	if !ok {
		t.Fatalf("v1 send failed: %v", v1Sent)
	}
	v1State := v1Result["status"].(map[string]any)["state"].(string)
	if !strings.HasPrefix(v1State, "TASK_STATE_") {
		t.Fatalf("v1 state = %q", v1State)
	}
	v1TaskID := v1Result["id"].(string)
	for i := 0; i < 50; i++ {
		_, got, _ := a2aCall(t, s, "GetTask", map[string]any{"id": v1TaskID}, a2aVersionV10)
		if got["result"] == nil {
			break
		}
		v1State = got["result"].(map[string]any)["status"].(map[string]any)["state"].(string)
		if v1State == "TASK_STATE_COMPLETED" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if v1State != "TASK_STATE_COMPLETED" {
		t.Fatalf("v1 task did not complete: %q", v1State)
	}

	// 空 contextId：服务端新建会话并照常建任务（未配模型 → 转终态）。
	_, fresh, _ := a2aCall(t, s, "message/send", a2aMessageParams("", "", "新线程", nil), "")
	freshTask, ok := fresh["result"].(map[string]any)
	if !ok || freshTask["contextId"] == "" || freshTask["id"] == "" {
		t.Fatalf("fresh task wrong: %v", fresh)
	}

	// 未知版本头 → 400。
	c, rec := remoteContext(http.MethodPost, "/a2a", mcpRequest(9, "message/send", nil), map[string]string{"A2A-Version": "9.9.9"})
	s.handleA2A(c)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown version = %d", rec.Code)
	}
}

func TestA2APushConfigUnsupported(t *testing.T) {
	s := newOpsTestServer(t)
	_, push, _ := a2aCall(t, s, "tasks/pushNotificationConfig/set", map[string]any{}, "")
	errObj := push["error"].(map[string]any)
	if errObj == nil || errObj["code"] != float64(a2aErrPushUnsupported) {
		t.Fatalf("push config: %v", push)
	}
}

// ---- REST 列表过滤分页 ----

func TestRemoteRESTListFilterPagination(t *testing.T) {
	s := newAgentIntegrationServer(t)
	for _, title := range []string{"a", "b", "c"} {
		if _, err := s.createRemoteAgentSession(context.Background(), title, "create", "", nil); err != nil {
			t.Fatalf("create: %v", err)
		}
	}
	call := func(query string) map[string]any {
		c, rec := remoteContext(http.MethodGet, "/api/agent/sessions"+query, "", nil)
		s.listAgentSessionsFiltered(c)
		if rec.Code != http.StatusOK {
			t.Fatalf("list %q: %d %s", query, rec.Code, rec.Body.String())
		}
		var envelope struct {
			Data struct {
				Items []map[string]any `json:"items"`
				Total int              `json:"total"`
			} `json:"data"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return map[string]any{"items": envelope.Data.Items, "total": envelope.Data.Total}
	}
	all := call("")
	if all["total"] != 3 || len(all["items"].([]map[string]any)) != 3 {
		t.Fatalf("all = %v", all)
	}
	page := call("?limit=2&offset=1")
	if page["total"] != 3 || len(page["items"].([]map[string]any)) != 2 {
		t.Fatalf("page = %v", page)
	}
	filtered := call("?status=waiting_approval")
	if filtered["total"] != 0 {
		t.Fatalf("filter = %v", filtered)
	}
	// 非法参数 → 400。
	c, rec := remoteContext(http.MethodGet, "/api/agent/sessions?limit=0", "", nil)
	s.listAgentSessionsFiltered(c)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid limit = %d", rec.Code)
	}
	c, rec = remoteContext(http.MethodGet, "/api/agent/sessions?status=bogus", "", nil)
	s.listAgentSessionsFiltered(c)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid status = %d", rec.Code)
	}
}

// ---- 小工具 ----

func ptrString(value string) *string { return &value }

func containsString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

// ---- 远程面缺陷回归（终态不被覆盖 / 悬空任务取代 / 显式裁决 / 负值拒绝）----

// seedGatedAgentSession 造一个带门控工具暂停链路的会话（假模型第 1 脚本
// 请求 test_upstream，第 2 脚本终稿），返回会话 id。
func seedGatedAgentSession(t *testing.T, s *Server, fake *fakeAgentModelServer, vendorURL string) string {
	t.Helper()
	session, err := s.createRemoteAgentSession(context.Background(), "门控", "create", "", nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := s.updateRemoteAgentSession(context.Background(), session.ID, nil, &agent.SettingsPatch{
		ModelSourceID: ptrString("s1"), ModelName: ptrString("fake-model"), TestBaseURL: ptrString(vendorURL),
	}); err != nil {
		t.Fatalf("patch: %v", err)
	}
	if err := s.store.UpdateSessionState(context.Background(), session.ID, stateUpdateWithDraft(json.RawMessage(agentTestConfig("gated-proto")))); err != nil {
		t.Fatalf("draft: %v", err)
	}
	return session.ID
}

func newGatedVendor(t *testing.T) *httptest.Server {
	t.Helper()
	vendor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"answer":{"text":"hi"},"finish":"stop"}`))
	}))
	t.Cleanup(vendor.Close)
	return vendor
}

func taskState(t *testing.T, s *Server, taskID string) string {
	t.Helper()
	_, got, _ := a2aCall(t, s, "tasks/get", map[string]any{"id": taskID}, "")
	result, ok := got["result"].(map[string]any)
	if !ok {
		return "missing"
	}
	status, _ := result["status"].(map[string]any)
	state, _ := status["state"].(string)
	return state
}

func waitForTaskState(t *testing.T, s *Server, taskID, want string) {
	t.Helper()
	for i := 0; i < 100; i++ {
		if taskState(t, s, taskID) == want {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("task %s never reached %q (now %q)", taskID, want, taskState(t, s, taskID))
}

// 取消后的终态不得被后台轮次的迟到帧（完成/失败）改写。
func TestA2ACancelFinalStateNotOverwritten(t *testing.T) {
	s := newAgentIntegrationServer(t)
	// 慢模型：轮次存活足够久，保证取消发生在运行中。
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(400 * time.Millisecond)
		w.Header().Set("Content-Type", "text/event-stream")
		for _, chunk := range []string{
			openAIChunk("c1", map[string]any{"role": "assistant", "content": "慢响应"}, "", nil),
			openAIChunk("c1", map[string]any{}, "stop", nil),
			openAIDone(),
		} {
			_, _ = w.Write([]byte(chunk))
		}
	}))
	t.Cleanup(slow.Close)
	seedAgentModel(t, s, slow.URL)
	session, err := s.createRemoteAgentSession(context.Background(), "取消", "create", "", &agent.Settings{ModelSourceID: "s1", ModelName: "fake-model"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	_, sent, _ := a2aCall(t, s, "message/send", a2aMessageParams(session.ID, "", "慢一点", nil), "")
	taskID := sent["result"].(map[string]any)["id"].(string)
	time.Sleep(80 * time.Millisecond) // 让轮次进入模型调用中
	if state := taskState(t, s, taskID); state != a2aStateWorking {
		t.Fatalf("pre-cancel state = %q, want working", state)
	}

	_, canceled, _ := a2aCall(t, s, "tasks/cancel", map[string]any{"id": taskID}, "")
	if state := canceled["result"].(map[string]any)["status"].(map[string]any)["state"].(string); state != a2aStateCanceled {
		t.Fatalf("cancel state = %q", state)
	}
	// 轮次被引擎停止后 watcher 会拿到失败事件并尝试 append——终态必须保持
	// canceled 不被改写。等轮次收尾后再断言若干次。
	waitForTaskState(t, s, taskID, a2aStateCanceled)
	time.Sleep(300 * time.Millisecond)
	if state := taskState(t, s, taskID); state != a2aStateCanceled {
		t.Fatalf("terminal state overwritten: %q", state)
	}
	// 再次取消 → 不可取消错误。
	_, again, _ := a2aCall(t, s, "tasks/cancel", map[string]any{"id": taskID}, "")
	if again["error"] == nil {
		t.Fatalf("cancel of terminal task must fail: %v", again)
	}
}

// 不带 taskId 的新消息开启新轮次后，同会话悬空的 input-required 旧任务
// 应转 failed（已被取代）。
func TestA2ASupersedeStaleTask(t *testing.T) {
	s := newAgentIntegrationServer(t)
	fake := newFakeAgentModelServer(t, [][]string{
		{openAIChunk("c1", toolCallDelta(0, "call_1", "bash", `{"command":"elysia protocol test"}`), "", nil),
			openAIChunk("c1", map[string]any{}, "tool_calls", nil),
			openAIDone()},
		{openAIChunk("c2", map[string]any{"role": "assistant", "content": "新任务完成"}, "", nil),
			openAIChunk("c2", map[string]any{}, "stop", nil),
			openAIDone()},
	})
	seedAgentModel(t, s, fake.URL)
	vendor := newGatedVendor(t)
	sessionID := seedGatedAgentSession(t, s, fake, vendor.URL)

	_, first, _ := a2aCall(t, s, "message/send", a2aMessageParams(sessionID, "", "请测试", nil), "")
	oldTask := first["result"].(map[string]any)["id"].(string)
	waitForTaskState(t, s, oldTask, a2aStateInputRequired)

	// 直接发新消息（不带 taskId）：引擎清待批开新轮次。
	_, second, _ := a2aCall(t, s, "message/send", a2aMessageParams(sessionID, "", "换个任务", nil), "")
	newTask := second["result"].(map[string]any)["id"].(string)
	waitForTaskState(t, s, newTask, a2aStateCompleted)
	waitForTaskState(t, s, oldTask, a2aStateFailed)
	if newTask == oldTask {
		t.Fatalf("new task must differ: %s", newTask)
	}
}

// MCP agent_respond：审批型待批必须显式携带 approved，零值不得静默拒绝。
func TestMCPRespondRequiresExplicitApproval(t *testing.T) {
	s := newAgentIntegrationServer(t)
	fake := newFakeAgentModelServer(t, [][]string{
		{openAIChunk("c1", toolCallDelta(0, "call_1", "bash", `{"command":"elysia protocol test"}`), "", nil),
			openAIChunk("c1", map[string]any{}, "tool_calls", nil),
			openAIDone()},
		{openAIChunk("c2", map[string]any{"role": "assistant", "content": "已按拒绝继续"}, "", nil),
			openAIChunk("c2", map[string]any{}, "stop", nil),
			openAIDone()},
	})
	seedAgentModel(t, s, fake.URL)
	vendor := newGatedVendor(t)
	sessionID := seedGatedAgentSession(t, s, fake, vendor.URL)

	// 驱动到 waiting_approval。
	c, rec := remoteContext(http.MethodPost, "/mcp", mcpRequest(11, "tools/call", map[string]any{
		"name": "agent_send_message", "arguments": map[string]any{"sessionId": sessionID, "text": "请测试"},
	}), mcpHeaders(nil))
	s.handleMCP(c)
	frames := parseA2AFrames(t, rec.Body.String())
	final := frames[len(frames)-1]["result"].(map[string]any)
	structured := final["structuredContent"].(map[string]any)
	if structured["status"] != agent.StatusWaitingApproval {
		t.Fatalf("status = %v, want waiting_approval: %v", structured["status"], structured)
	}

	// 漏传 approved → isError，且会话保持待批。
	c, rec = remoteContext(http.MethodPost, "/mcp", mcpRequest(12, "tools/call", map[string]any{
		"name": "agent_respond", "arguments": map[string]any{"sessionId": sessionID},
	}), mcpHeaders(nil))
	s.handleMCP(c)
	frames = parseA2AFrames(t, rec.Body.String())
	final = frames[len(frames)-1]["result"].(map[string]any)
	if final["isError"] != true {
		t.Fatalf("missing approved must be isError: %v", final)
	}
	session, _, _ := s.getRemoteAgentSession(context.Background(), sessionID)
	if session.Status != agent.StatusWaitingApproval {
		t.Fatalf("session state = %q, still waiting expected", session.Status)
	}

	// 显式 approved:false → 正常拒绝收尾。
	c, rec = remoteContext(http.MethodPost, "/mcp", mcpRequest(13, "tools/call", map[string]any{
		"name": "agent_respond", "arguments": map[string]any{"sessionId": sessionID, "approved": false},
	}), mcpHeaders(nil))
	s.handleMCP(c)
	frames = parseA2AFrames(t, rec.Body.String())
	final = frames[len(frames)-1]["result"].(map[string]any)
	if final["isError"] == true {
		t.Fatalf("explicit deny failed: %v", final)
	}
	structured = final["structuredContent"].(map[string]any)
	if structured["status"] != agent.StatusIdle || structured["reply"] != "已按拒绝继续" {
		t.Fatalf("deny outcome wrong: %v", structured)
	}
}

// afterSeq 负值直接拒绝。
func TestMCPClearMessagesRejectsNegativeAfterSeq(t *testing.T) {
	s := newOpsTestServer(t)
	_, result, _ := mcpCall(t, s, mcpRequest(14, "tools/call", map[string]any{
		"name": "agent_clear_messages", "arguments": map[string]any{"sessionId": "any", "afterSeq": -1},
	}), nil)
	payload := result["result"].(map[string]any)
	if payload["isError"] != true {
		t.Fatalf("negative afterSeq must be isError: %v", payload)
	}
}
