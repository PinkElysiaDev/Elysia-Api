package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elysia-api/backend/relay"
)

// ---- fakes ----

// fakeCaller 按脚本依次返回结果；记录收到的请求供断言。
type fakeCaller struct {
	mu        sync.Mutex
	responses []scriptedResponse
	calls     []CallRequest
}

type scriptedResponse struct {
	result *CallResult
	err    error
	// streamText 模拟流式回调（验证增量事件）。
	streamText []string
}

func (f *fakeCaller) Call(ctx context.Context, req CallRequest, cb StreamCallbacks) (*CallResult, error) {
	f.mu.Lock()
	if len(f.responses) == 0 {
		f.mu.Unlock()
		return nil, errors.New("no scripted response left")
	}
	next := f.responses[0]
	f.responses = f.responses[1:]
	f.calls = append(f.calls, req)
	f.mu.Unlock()
	for _, delta := range next.streamText {
		if cb.OnText != nil {
			cb.OnText(delta)
		}
	}
	return next.result, next.err
}

func (f *fakeCaller) requestCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func (f *fakeCaller) lastRequest() CallRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[len(f.calls)-1]
}

// fakeStore 内存实现 agent.Store。
type fakeStore struct {
	mu       sync.Mutex
	sessions map[string]*Session
	messages map[string][]Message
	nextSeq  map[string]int
}

func newFakeStore() *fakeStore {
	return &fakeStore{sessions: map[string]*Session{}, messages: map[string][]Message{}, nextSeq: map[string]int{}}
}

func (f *fakeStore) GetSession(ctx context.Context, id string) (*Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	session, ok := f.sessions[id]
	if !ok {
		return nil, fmt.Errorf("session %q not found", id)
	}
	copy := *session
	return &copy, nil
}

func (f *fakeStore) UpdateSessionState(ctx context.Context, id string, update SessionStateUpdate) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	session, ok := f.sessions[id]
	if !ok {
		return fmt.Errorf("session %q not found", id)
	}
	if update.Status != nil {
		session.Status = *update.Status
	}
	if update.ClearPending {
		session.PendingAction = nil
	} else if update.PendingAction != nil {
		session.PendingAction = update.PendingAction
	}
	if len(update.DraftConfig) > 0 {
		session.DraftConfig = update.DraftConfig
	}
	if update.DraftRestore != nil {
		session.DraftRestore = update.DraftRestore
	}
	if update.Title != "" {
		session.Title = update.Title
	}
	if update.Plan != nil {
		session.Plan = update.Plan
	}
	if update.TestBaseURL != "" {
		session.TestBaseURL = update.TestBaseURL
	}
	if update.TestAPIKey != "" {
		session.TestAPIKey = update.TestAPIKey
	}
	if update.SettingsPlanMode != nil {
		session.Settings.PlanMode = *update.SettingsPlanMode
	}
	return nil
}

func (f *fakeStore) AppendMessage(ctx context.Context, sessionID string, role string, content any, model string, usage json.RawMessage) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	encoded, err := json.Marshal(content)
	if err != nil {
		return 0, err
	}
	f.nextSeq[sessionID]++
	seq := f.nextSeq[sessionID]
	f.messages[sessionID] = append(f.messages[sessionID], Message{Seq: seq, Role: role, Content: encoded, Model: model, Usage: usage, CreatedAt: time.Now()})
	return seq, nil
}

func (f *fakeStore) ListMessages(ctx context.Context, sessionID string) ([]Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Message(nil), f.messages[sessionID]...), nil
}

func (f *fakeStore) TruncateMessages(ctx context.Context, sessionID string, afterSeq int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	var kept []Message
	for _, message := range f.messages[sessionID] {
		if message.Seq <= afterSeq {
			kept = append(kept, message)
		}
	}
	f.messages[sessionID] = kept
	return nil
}

func (f *fakeStore) seed(session *Session) *fakeStore {
	f.sessions[session.ID] = session
	return f
}

func (f *fakeStore) roles(sessionID string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var roles []string
	for _, message := range f.messages[sessionID] {
		roles = append(roles, message.Role)
	}
	return roles
}

// fakeTool 脚本化工具。
type fakeTool struct {
	name       string
	gated      bool
	permKey    string
	result     ToolResult
	executions int
	setDraft   json.RawMessage        // 执行时写入的草稿
	onExecute  func(tctx ToolContext) // 可选：捕获工具上下文（如 TestTarget）
}

func (t *fakeTool) Name() string { return t.name }
func (t *fakeTool) Definition() relay.MaheshvaraTool {
	return relay.MaheshvaraTool{Type: "function", Name: t.name, Description: t.name}
}
func (t *fakeTool) Gated() bool           { return t.gated }
func (t *fakeTool) PermissionKey() string { return t.permKey }
func (t *fakeTool) Description() string   { return t.name + " 工具" }
func (t *fakeTool) Execute(ctx context.Context, tctx ToolContext, args json.RawMessage) ToolResult {
	t.executions++
	if t.setDraft != nil {
		_ = tctx.SetDraft(t.setDraft)
	}
	if t.onExecute != nil {
		t.onExecute(tctx)
	}
	return t.result
}

// ---- helpers ----

func collectEvents(t *testing.T, events <-chan Event) []Event {
	t.Helper()
	var collected []Event
	for event := range events {
		collected = append(collected, event)
	}
	return collected
}

func hasEvent(events []Event, eventType string) bool {
	for _, event := range events {
		if event.Type == eventType {
			return true
		}
	}
	return false
}

func newTestEngine(caller StreamCaller, store Store, tools ...Tool) *Engine {
	registry, err := NewRegistry(tools...)
	if err != nil {
		panic(err)
	}
	return NewEngine(caller, store, registry, nil, func(*Session) string { return "test prompt" }, Options{TurnTimeout: 5 * time.Second})
}

func toolCall(id, name string, args string) relay.MaheshvaraToolCall {
	return relay.MaheshvaraToolCall{ID: id, Type: "function", Name: name, Arguments: json.RawMessage(args)}
}

// ---- tests ----

func TestRunTurn_PlainReplyWithoutTools(t *testing.T) {
	store := newFakeStore().seed(&Session{ID: "s1", Status: StatusIdle, Settings: Settings{ModelName: "m1"}})
	caller := &fakeCaller{responses: []scriptedResponse{{
		result:     &CallResult{Text: "你好，我是终稿", Usage: &relay.MaheshvaraUsage{InputTokens: 10, OutputTokens: 5, TotalTokens: 15}},
		streamText: []string{"你好", "，我是终稿"},
	}}}
	engine := newTestEngine(caller, store)

	events, err := engine.RunTurn(context.Background(), "s1", &UserContent{Text: "打个招呼"})
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	collected := collectEvents(t, events)

	if !hasEvent(collected, EventTextDelta) || !hasEvent(collected, EventTurnDone) {
		t.Fatalf("missing text_delta or turn_done: %+v", collected)
	}
	if hasEvent(collected, EventToolCall) {
		t.Fatalf("unexpected tool call")
	}
	// 消息序列：user → assistant
	roles := store.roles("s1")
	if len(roles) != 2 || roles[0] != RoleUser || roles[1] != RoleAssistant {
		t.Fatalf("roles = %v", roles)
	}
	// 状态回落 idle
	session, _ := store.GetSession(context.Background(), "s1")
	if session.Status != StatusIdle {
		t.Fatalf("status = %q", session.Status)
	}
	// 首条用户消息取标题
	if session.Title != "打个招呼" {
		t.Fatalf("title = %q", session.Title)
	}
	// turn_done 带用量
	var done Event
	for _, event := range collected {
		if event.Type == EventTurnDone {
			done = event
		}
	}
	if done.Usage == nil || done.Usage.TotalTokens != 15 {
		t.Fatalf("turn_done usage = %+v", done.Usage)
	}
}

func TestRunTurn_ToolLoopThenFinalReply(t *testing.T) {
	store := newFakeStore().seed(&Session{ID: "s1", Status: StatusIdle, Settings: Settings{ModelName: "m1"}})
	caller := &fakeCaller{responses: []scriptedResponse{
		{result: &CallResult{Text: "我先查一下", ToolCalls: []relay.MaheshvaraToolCall{toolCall("c1", "lookup", `{}`)}}},
		{result: &CallResult{Text: "查完了，这是结论"}},
	}}
	lookup := &fakeTool{name: "lookup", result: ToolResult{OK: true, Summary: "ok", Data: map[string]any{"value": 42}}}
	engine := newTestEngine(caller, store, lookup)

	events, _ := engine.RunTurn(context.Background(), "s1", &UserContent{Text: "查"})
	collected := collectEvents(t, events)

	if !hasEvent(collected, EventToolCall) || !hasEvent(collected, EventToolResult) || !hasEvent(collected, EventTurnDone) {
		t.Fatalf("missing tool events: %+v", collected)
	}
	if lookup.executions != 1 {
		t.Fatalf("executions = %d", lookup.executions)
	}
	// 消息序列：user → assistant(tool_call) → tool_result → assistant(终稿)
	roles := store.roles("s1")
	want := []string{RoleUser, RoleAssistant, RoleToolResult, RoleAssistant}
	if strings.Join(roles, ",") != strings.Join(want, ",") {
		t.Fatalf("roles = %v", roles)
	}
	// 第二次调用时对话应包含工具输出消息
	last := caller.lastRequest()
	if len(last.Messages) != 3 {
		t.Fatalf("second call messages = %d", len(last.Messages))
	}
	toolMsg := last.Messages[2]
	if toolMsg.Role != "tool" || len(toolMsg.Content) == 0 || toolMsg.Content[0].Type != relay.MaheshvaraContentToolOutput {
		t.Fatalf("tool message shape wrong: %+v", toolMsg)
	}
	if toolMsg.Content[0].ToolOutput == "" || !strings.Contains(toolMsg.Content[0].ToolOutput, "42") {
		t.Fatalf("tool output missing: %+v", toolMsg.Content[0])
	}
}

func TestRunTurn_GatedToolPausesAndResumeApproves(t *testing.T) {
	store := newFakeStore().seed(&Session{ID: "s1", Status: StatusIdle, Settings: Settings{ModelName: "m1", AllowLiveTest: PermissionAsk}})
	caller := &fakeCaller{responses: []scriptedResponse{
		{result: &CallResult{Text: "需要真实测试", ToolCalls: []relay.MaheshvaraToolCall{toolCall("c1", "danger", `{"x":1}`)}}},
		{result: &CallResult{Text: "测试完成"}},
	}}
	danger := &fakeTool{name: "danger", gated: true, permKey: "live_test", result: ToolResult{OK: true, Summary: "已执行"}}
	engine := newTestEngine(caller, store, danger)

	events, _ := engine.RunTurn(context.Background(), "s1", &UserContent{Text: "测试一下"})
	collected := collectEvents(t, events)
	if !hasEvent(collected, EventApprovalPending) {
		t.Fatalf("missing approval_required: %+v", collected)
	}
	if hasEvent(collected, EventTurnDone) {
		t.Fatalf("paused turn must not emit turn_done")
	}
	// 状态与待定动作
	session, _ := store.GetSession(context.Background(), "s1")
	if session.Status != StatusWaitingApproval || session.PendingAction == nil || len(session.PendingAction.Calls) != 1 {
		t.Fatalf("session state = %+v", session)
	}
	if danger.executions != 0 {
		t.Fatalf("gated tool must not run before approval")
	}

	// 未决状态再发新消息应被拒绝
	if _, err := engine.RunTurn(context.Background(), "s1", nil); err != nil {
		t.Fatalf("new turn while waiting approval should be allowed (truncation path): %v", err)
	}

	// 重新走到暂停状态，再走审批恢复
	store2 := newFakeStore().seed(&Session{ID: "s2", Status: StatusIdle, Settings: Settings{ModelName: "m1", AllowLiveTest: PermissionAsk}})
	caller2 := &fakeCaller{responses: []scriptedResponse{
		{result: &CallResult{Text: "需要测试", ToolCalls: []relay.MaheshvaraToolCall{toolCall("c1", "danger", `{"x":1}`)}}},
		{result: &CallResult{Text: "测试完成"}},
	}}
	danger2 := &fakeTool{name: "danger", gated: true, permKey: "live_test", result: ToolResult{OK: true, Summary: "已执行"}}
	engine2 := newTestEngine(caller2, store2, danger2)
	events2, _ := engine2.RunTurn(context.Background(), "s2", &UserContent{Text: "go"})
	collectEvents(t, events2)

	resumeEvents, err := engine2.ResumeApproval(context.Background(), "s2", ApprovalDecision{Approved: true, BaseURL: "https://up.example", APIKey: "sk-1"})
	if err != nil {
		t.Fatalf("ResumeApproval: %v", err)
	}
	resumed := collectEvents(t, resumeEvents)
	if !hasEvent(resumed, EventToolResult) || !hasEvent(resumed, EventTurnDone) {
		t.Fatalf("resume missing events: %+v", resumed)
	}
	if danger2.executions != 1 {
		t.Fatalf("approved tool not executed")
	}
	session2, _ := store2.GetSession(context.Background(), "s2")
	if session2.Status != StatusIdle {
		t.Fatalf("status = %q", session2.Status)
	}
	if session2.TestBaseURL != "https://up.example" || session2.TestAPIKey != "sk-1" {
		t.Fatalf("credentials not updated: %+v", session2)
	}
	// 审批记录存在
	var sawApproval bool
	for _, role := range store2.roles("s2") {
		if role == RoleApproval {
			sawApproval = true
		}
	}
	if !sawApproval {
		t.Fatalf("approval message not persisted")
	}
}

func TestRunTurn_DenyApprovalSynthesizesDenial(t *testing.T) {
	store := newFakeStore().seed(&Session{ID: "s1", Status: StatusWaitingApproval,
		PendingAction: &PendingAction{Calls: []relay.MaheshvaraToolCall{toolCall("c1", "danger", `{"x":1}`)}},
		Settings:      Settings{ModelName: "m1"}})
	caller := &fakeCaller{responses: []scriptedResponse{
		{result: &CallResult{Text: "好吧，我换个思路"}},
	}}
	danger := &fakeTool{name: "danger", gated: true, permKey: "live_test", result: ToolResult{OK: true}}
	engine := newTestEngine(caller, store, danger)

	events, err := engine.ResumeApproval(context.Background(), "s1", ApprovalDecision{Approved: false, Note: "别测"})
	if err != nil {
		t.Fatalf("ResumeApproval: %v", err)
	}
	collected := collectEvents(t, events)
	if !hasEvent(collected, EventToolResult) || !hasEvent(collected, EventTurnDone) {
		t.Fatalf("missing events: %+v", collected)
	}
	if danger.executions != 0 {
		t.Fatalf("denied tool must not execute")
	}
	// 拒绝结果回传给了模型
	last := caller.lastRequest()
	if len(last.Messages) == 0 {
		t.Fatalf("no messages")
	}
	found := false
	for _, msg := range last.Messages {
		for _, part := range msg.Content {
			if part.Type == relay.MaheshvaraContentToolOutput && strings.Contains(part.ToolOutput, "denied") {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("denial not fed back to model")
	}
}

func TestRunTurn_AlwaysPermissionSkipsApproval(t *testing.T) {
	store := newFakeStore().seed(&Session{ID: "s1", Status: StatusIdle, Settings: Settings{ModelName: "m1", AllowLiveTest: PermissionAlways}})
	caller := &fakeCaller{responses: []scriptedResponse{
		{result: &CallResult{Text: "直接测", ToolCalls: []relay.MaheshvaraToolCall{toolCall("c1", "danger", `{"x":1}`)}}},
		{result: &CallResult{Text: "完成"}},
	}}
	danger := &fakeTool{name: "danger", gated: true, permKey: "live_test", result: ToolResult{OK: true}}
	engine := newTestEngine(caller, store, danger)

	events, _ := engine.RunTurn(context.Background(), "s1", &UserContent{Text: "go"})
	collected := collectEvents(t, events)
	if hasEvent(collected, EventApprovalPending) {
		t.Fatalf("always policy must not pause")
	}
	if danger.executions != 1 {
		t.Fatalf("tool not executed")
	}
}

func TestRunTurn_NeverPermissionSynthesizesDenial(t *testing.T) {
	store := newFakeStore().seed(&Session{ID: "s1", Status: StatusIdle, Settings: Settings{ModelName: "m1", AllowLiveTest: PermissionNever}})
	caller := &fakeCaller{responses: []scriptedResponse{
		{result: &CallResult{Text: "直接测", ToolCalls: []relay.MaheshvaraToolCall{toolCall("c1", "danger", `{"x":1}`)}}},
		{result: &CallResult{Text: "换思路"}},
	}}
	danger := &fakeTool{name: "danger", gated: true, permKey: "live_test", result: ToolResult{OK: true}}
	engine := newTestEngine(caller, store, danger)

	events, _ := engine.RunTurn(context.Background(), "s1", &UserContent{Text: "go"})
	collectEvents(t, events)
	if danger.executions != 0 {
		t.Fatalf("never policy must block execution")
	}
}

func TestRunTurn_PlanModeBlocksGatedTools(t *testing.T) {
	// 计划模式优先于 always 策略：gated 工具一律拒绝且不进入审批暂停。
	store := newFakeStore().seed(&Session{ID: "s1", Status: StatusIdle,
		Settings: Settings{ModelName: "m1", PlanMode: true, AllowLiveTest: PermissionAlways, AllowSave: PermissionAlways}})
	caller := &fakeCaller{responses: []scriptedResponse{
		{result: &CallResult{Text: "我来改配置", ToolCalls: []relay.MaheshvaraToolCall{toolCall("c1", "danger", `{"x":1}`)}}},
		{result: &CallResult{Text: "好的，先给出方案"}},
	}}
	danger := &fakeTool{name: "danger", gated: true, permKey: "save", result: ToolResult{OK: true}}
	engine := newTestEngine(caller, store, danger)

	events, _ := engine.RunTurn(context.Background(), "s1", &UserContent{Text: "新增模型源"})
	collected := collectEvents(t, events)
	if hasEvent(collected, EventApprovalPending) {
		t.Fatalf("plan mode must not pause for approval")
	}
	if !hasEvent(collected, EventTurnDone) {
		t.Fatalf("turn should complete after synthesized plan-mode denial: %+v", collected)
	}
	if danger.executions != 0 {
		t.Fatalf("plan mode must block gated tool execution")
	}
	session, _ := store.GetSession(context.Background(), "s1")
	if session.Status != StatusIdle || session.PendingAction != nil {
		t.Fatalf("session state = %+v", session)
	}
	// 计划模式拒绝理由回传给模型
	last := caller.lastRequest()
	found := false
	for _, msg := range last.Messages {
		for _, part := range msg.Content {
			if part.Type == relay.MaheshvaraContentToolOutput && strings.Contains(part.ToolOutput, "计划模式") {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("plan-mode denial not fed back to model")
	}
}

func TestRunTurn_SnapshotsDraftRestorePoint(t *testing.T) {
	draftV1 := json.RawMessage(`{"id":"p1","v":1}`)
	store := newFakeStore().seed(&Session{ID: "s1", Status: StatusIdle, DraftConfig: draftV1, Settings: Settings{ModelName: "m1"}})
	caller := &fakeCaller{responses: []scriptedResponse{{result: &CallResult{Text: "第一轮"}}}}
	engine := newTestEngine(caller, store)

	events, _ := engine.RunTurn(context.Background(), "s1", &UserContent{Text: "go"})
	collectEvents(t, events)
	session, _ := store.GetSession(context.Background(), "s1")
	if string(session.DraftRestore) != string(draftV1) {
		t.Fatalf("restore point = %s, want %s", session.DraftRestore, draftV1)
	}

	// 草稿变化后进入下一轮：还原点更新为新一轮开始前的值（单槽覆盖）。
	draftV2 := json.RawMessage(`{"id":"p1","v":2}`)
	if err := store.UpdateSessionState(context.Background(), "s1", SessionStateUpdate{DraftConfig: draftV2}); err != nil {
		t.Fatalf("set draft: %v", err)
	}
	caller2 := &fakeCaller{responses: []scriptedResponse{{result: &CallResult{Text: "第二轮"}}}}
	events2, _ := newTestEngine(caller2, store).RunTurn(context.Background(), "s1", &UserContent{Text: "again"})
	collectEvents(t, events2)
	session2, _ := store.GetSession(context.Background(), "s1")
	if string(session2.DraftRestore) != string(draftV2) {
		t.Fatalf("restore point not overwritten: %s, want %s", session2.DraftRestore, draftV2)
	}
}

func TestRunTurn_ModelErrorEmitsRetryableError(t *testing.T) {
	store := newFakeStore().seed(&Session{ID: "s1", Status: StatusIdle, Settings: Settings{ModelName: "m1"}})
	caller := &fakeCaller{responses: []scriptedResponse{{err: errors.New("connection refused")}}}
	engine := newTestEngine(caller, store)

	events, _ := engine.RunTurn(context.Background(), "s1", &UserContent{Text: "hi"})
	collected := collectEvents(t, events)
	var errEvent Event
	for _, event := range collected {
		if event.Type == EventError {
			errEvent = event
		}
	}
	if errEvent.Type != EventError || !errEvent.Retryable {
		t.Fatalf("error event = %+v", errEvent)
	}
	if !hasEvent(collected, EventTurnDone) {
		t.Fatalf("turn_done should follow error for usage stats")
	}
	session, _ := store.GetSession(context.Background(), "s1")
	if session.Status != StatusIdle {
		t.Fatalf("status = %q", session.Status)
	}
}

func TestRunTurn_ConcurrencyGuard(t *testing.T) {
	store := newFakeStore().seed(&Session{ID: "s1", Status: StatusIdle, Settings: Settings{ModelName: "m1"}})
	release := make(chan struct{})
	caller := &blockingCaller{release: release}
	engine := newTestEngine(caller, store)

	first, err := engine.RunTurn(context.Background(), "s1", &UserContent{Text: "slow"})
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	if _, err := engine.RunTurn(context.Background(), "s1", &UserContent{Text: "second"}); !errors.Is(err, ErrSessionRunning) {
		t.Fatalf("expected ErrSessionRunning, got %v", err)
	}
	close(release)
	collectEvents(t, first)
	if engine.IsRunning("s1") {
		t.Fatalf("session should be idle after turn")
	}
}

type blockingCaller struct{ release chan struct{} }

func (b *blockingCaller) Call(ctx context.Context, req CallRequest, cb StreamCallbacks) (*CallResult, error) {
	<-b.release
	return &CallResult{Text: "done"}, nil
}

func TestEngine_Stop(t *testing.T) {
	store := newFakeStore().seed(&Session{ID: "s1", Status: StatusIdle, Settings: Settings{ModelName: "m1"}})
	caller := &ctxAwareHangCaller{}
	engine := newTestEngine(caller, store)

	events, _ := engine.RunTurn(context.Background(), "s1", &UserContent{Text: "hang"})
	go func() {
		time.Sleep(100 * time.Millisecond)
		engine.Stop("s1")
	}()
	collected := collectEvents(t, events)
	var errEvent Event
	for _, event := range collected {
		if event.Type == EventError {
			errEvent = event
		}
	}
	if errEvent.Type != EventError {
		t.Fatalf("stop should surface an error event: %+v", collected)
	}
	if errEvent.Retryable {
		t.Fatalf("stopped turn is not retryable")
	}
	if !hasEvent(collected, EventTurnDone) {
		t.Fatalf("turn_done should still be emitted")
	}
	if engine.IsRunning("s1") {
		t.Fatalf("engine still running after stop")
	}
}

// ctxAwareHangCaller 挂起直到 ctx 取消（真实模型调用经 HTTP ctx 感知取消），
// 取消时返回带部分文本的结果（引擎照常落库部分内容）。
type ctxAwareHangCaller struct{}

func (c *ctxAwareHangCaller) Call(ctx context.Context, req CallRequest, cb StreamCallbacks) (*CallResult, error) {
	cb.OnText("部分输出")
	<-ctx.Done()
	return &CallResult{Text: "部分输出"}, ctx.Err()
}

func TestRunTurn_DraftUpdateEmitsDraftUpdated(t *testing.T) {
	store := newFakeStore().seed(&Session{ID: "s1", Status: StatusIdle, Settings: Settings{ModelName: "m1"}})
	caller := &fakeCaller{responses: []scriptedResponse{
		{result: &CallResult{Text: "写草稿", ToolCalls: []relay.MaheshvaraToolCall{toolCall("c1", "draft", `{"id":"x"}`)}}},
		{result: &CallResult{Text: "完成"}},
	}}
	draft := json.RawMessage(`{"id":"x","request":{}}`)
	draftTool := &fakeTool{name: "draft", result: ToolResult{OK: true}, setDraft: draft}
	engine := newTestEngine(caller, store, draftTool)

	events, _ := engine.RunTurn(context.Background(), "s1", &UserContent{Text: "go"})
	collected := collectEvents(t, events)
	if !hasEvent(collected, EventDraftUpdated) {
		t.Fatalf("missing draft_updated")
	}
	session, _ := store.GetSession(context.Background(), "s1")
	if string(session.DraftConfig) != string(draft) {
		t.Fatalf("draft = %s", session.DraftConfig)
	}
	// 第二次模型调用的系统提示词应包含草稿块
	last := caller.lastRequest()
	if !strings.Contains(last.Instructions, `"id":"x"`) || !strings.Contains(last.Instructions, "当前工作草稿") {
		t.Fatalf("instructions missing draft block")
	}
}

func TestRunTurn_UnknownToolSynthesizesError(t *testing.T) {
	store := newFakeStore().seed(&Session{ID: "s1", Status: StatusIdle, Settings: Settings{ModelName: "m1"}})
	caller := &fakeCaller{responses: []scriptedResponse{
		{result: &CallResult{Text: "调用", ToolCalls: []relay.MaheshvaraToolCall{toolCall("c1", "nope", `{"x":1}`)}}},
		{result: &CallResult{Text: "ok"}},
	}}
	engine := newTestEngine(caller, store)

	events, _ := engine.RunTurn(context.Background(), "s1", &UserContent{Text: "go"})
	collected := collectEvents(t, events)
	if !hasEvent(collected, EventToolResult) || !hasEvent(collected, EventTurnDone) {
		t.Fatalf("unknown tool should produce error result and continue: %+v", collected)
	}
}

func TestRunTurn_MaxRoundsCap(t *testing.T) {
	store := newFakeStore().seed(&Session{ID: "s1", Status: StatusIdle, Settings: Settings{ModelName: "m1"}})
	const rounds = 3
	responses := make([]scriptedResponse, rounds+2)
	for i := range responses {
		responses[i] = scriptedResponse{result: &CallResult{Text: "again", ToolCalls: []relay.MaheshvaraToolCall{toolCall(fmt.Sprintf("c%d", i), "loop", `{"x":1}`)}}}
	}
	caller := &fakeCaller{responses: responses}
	tool := &fakeTool{name: "loop", result: ToolResult{OK: true}}
	registry, _ := NewRegistry(tool)
	engine := NewEngine(caller, store, registry, nil, nil, Options{MaxModelCalls: rounds, TurnTimeout: 5 * time.Second})

	events, _ := engine.RunTurn(context.Background(), "s1", &UserContent{Text: "go"})
	collectEvents(t, events)
	if got := caller.requestCount(); got != rounds {
		t.Fatalf("model calls = %d, want %d", got, rounds)
	}
}

func TestRunTurn_EmptyContinueRegenerates(t *testing.T) {
	// 既有历史：user → assistant；input=nil 重新生成
	store := newFakeStore()
	store.seed(&Session{ID: "s1", Status: StatusIdle, Settings: Settings{ModelName: "m1"}})
	_, _ = store.AppendMessage(context.Background(), "s1", RoleUser, UserContent{Text: "hi"}, "", nil)
	_, _ = store.AppendMessage(context.Background(), "s1", RoleAssistant, AssistantContent{Text: "旧回复"}, "m1", nil)

	caller := &fakeCaller{responses: []scriptedResponse{{result: &CallResult{Text: "新回复"}}}}
	engine := newTestEngine(caller, store)
	events, err := engine.RunTurn(context.Background(), "s1", nil)
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	collectEvents(t, events)
	// 不新增 user 消息
	var userCount int
	for _, role := range store.roles("s1") {
		if role == RoleUser {
			userCount++
		}
	}
	if userCount != 1 {
		t.Fatalf("user messages = %d", userCount)
	}
	// 对话包含历史 user 消息
	req := caller.lastRequest()
	if len(req.Messages) == 0 || req.Messages[0].Role != "user" {
		t.Fatalf("history not loaded: %+v", req.Messages)
	}
}

func TestToolResultMarshal(t *testing.T) {
	result := ToolResult{OK: true, Data: map[string]any{"a": 1}}
	if string(result.MarshalData()) != `{"a":1}` {
		t.Fatalf("data = %s", result.MarshalData())
	}
	str := ToolResult{OK: true, Data: "纯文本"}
	if string(str.MarshalData()) != `"纯文本"` {
		t.Fatalf("string data = %s", str.MarshalData())
	}
}

func TestRegistry(t *testing.T) {
	a := &fakeTool{name: "a"}
	b := &fakeTool{name: "b", gated: true, permKey: "save"}
	registry, err := NewRegistry(a, b)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	if registry.Get("a") != a || registry.Get("missing") != nil {
		t.Fatalf("Get broken")
	}
	if len(registry.Definitions()) != 2 {
		t.Fatalf("definitions = %d", len(registry.Definitions()))
	}
	if _, err := NewRegistry(a, a); err == nil {
		t.Fatalf("duplicate registration should fail")
	}
}

// 回归（W1-1）：clampJSON 截断产物必须是合法 JSON——旧实现「JSON 前缀 +
// 文本尾巴」让超限工具结果 AppendMessage 时 Marshal 失败，历史留下无结果的
// tool_calls，会话后续每轮被上游 400 拒绝。
type blockingTool struct {
	fakeTool
	started chan struct{}
	release chan struct{}
}

func (b *blockingTool) Meta() ToolMeta { return ToolMeta{ConcurrentSafe: true} }

func (b *blockingTool) Execute(ctx context.Context, tctx ToolContext, args json.RawMessage) ToolResult {
	select {
	case <-b.started:
	default:
		close(b.started)
	}
	select {
	case <-b.release:
	case <-ctx.Done():
		return ToolResult{OK: false, Summary: "canceled"}
	}
	return b.result
}

// 两个声明可并行的只读工具应重叠执行，而不是等第一个结束才开始第二个。
func TestExecuteCalls_ParallelReadOnlyToolsOverlap(t *testing.T) {
	release := make(chan struct{})
	first := &blockingTool{fakeTool: fakeTool{name: "list_a", result: ToolResult{OK: true, Summary: "a"}}, started: make(chan struct{}), release: release}
	second := &blockingTool{fakeTool: fakeTool{name: "list_b", result: ToolResult{OK: true, Summary: "b"}}, started: make(chan struct{}), release: release}
	registry, err := NewRegistry(first, second)
	if err != nil {
		t.Fatalf("registry: %v", err)
	}
	store := newFakeStore().seed(&Session{ID: "s1", Status: StatusIdle})
	engine := NewEngine(&fakeCaller{}, store, registry, nil, nil, Options{})
	session, _ := store.GetSession(context.Background(), "s1")
	conversation := []relay.MaheshvaraMessage{}
	done := make(chan struct{})
	go func() {
		_, _ = engine.executeCalls(context.Background(), "s1", session, &conversation, []relay.MaheshvaraToolCall{
			toolCall("c1", "list_a", `{}`), toolCall("c2", "list_b", `{}`),
		}, "", nil, make(chan Event, 8))
		close(done)
	}()
	select {
	case <-first.started:
	case <-time.After(time.Second):
		t.Fatal("first tool did not start")
	}
	select {
	case <-second.started:
	case <-time.After(time.Second):
		t.Fatal("second tool did not overlap the first")
	}
	close(release)
	<-done
}

func TestClampJSON_TailKeepsEnding(t *testing.T) {
	raw := json.RawMessage(`{"head":"` + strings.Repeat("a", 400) + `","tail":"ENDMARK"}`)
	clamped := clampJSON(raw, 80, "tail")
	if !json.Valid(clamped) || !strings.Contains(string(clamped), "ENDMARK") {
		t.Fatalf("tail clamp lost the ending: %s", clamped)
	}
}

func TestMaskSecretInputs_Authorization(t *testing.T) {
	masked := MaskSecretInputs(json.RawMessage(`{"authorization":"Bearer secret","name":"x"}`))
	if strings.Contains(string(masked), "secret") || !strings.Contains(string(masked), `"name":"x"`) {
		t.Fatalf("mask = %s", masked)
	}
}

func TestMicroCompact_ClearsOldToolResults(t *testing.T) {
	conversation := make([]relay.MaheshvaraMessage, 0, 6)
	for index := 0; index < 6; index++ {
		conversation = append(conversation, relay.MaheshvaraMessage{Role: "tool", Content: []relay.MaheshvaraContentPart{{
			Type: relay.MaheshvaraContentToolOutput, ToolCallID: fmt.Sprintf("c%d", index), ToolOutput: strings.Repeat("x", 1000),
		}}})
	}
	compacted, cleared := microCompact(conversation)
	if cleared != 2 {
		t.Fatalf("cleared = %d", cleared)
	}
	if !strings.Contains(compacted[0].Content[0].ToolOutput, "旧工具结果已清除") {
		t.Fatalf("oldest result not cleared: %s", compacted[0].Content[0].ToolOutput)
	}
	if strings.Contains(compacted[5].Content[0].ToolOutput, "旧工具结果已清除") {
		t.Fatal("newest result should be kept")
	}
}

func TestClampJSON_TruncationStaysValidJSON(t *testing.T) {
	huge := json.RawMessage(`{"data":"` + strings.Repeat("x", 200*1024) + `"}`)
	clamped := clampJSON(huge, 64*1024, "head")
	if !json.Valid(clamped) {
		t.Fatalf("clamped output must be valid JSON: %.80s", clamped)
	}
	var envelope map[string]any
	if err := json.Unmarshal(clamped, &envelope); err != nil || envelope["truncated"] != true {
		t.Fatalf("envelope shape wrong: %.120s", clamped)
	}
	small := clampJSON(json.RawMessage(`{"a":1}`), 64*1024, "head")
	if string(small) != `{"a":1}` {
		t.Fatalf("small payload must pass through untouched: %s", small)
	}
}

// 回归（W1-1）：超限工具结果必须照常落库为 tool_result 消息（可再解析）。
func TestRunTurn_OversizedToolResultPersists(t *testing.T) {
	store := newFakeStore().seed(&Session{ID: "s1", Status: StatusIdle, Settings: Settings{ModelName: "m1"}})
	big := map[string]any{"data": strings.Repeat("x", 100*1024)}
	caller := &fakeCaller{responses: []scriptedResponse{
		{result: &CallResult{Text: "查", ToolCalls: []relay.MaheshvaraToolCall{toolCall("c1", "lookup", `{}`)}}},
		{result: &CallResult{Text: "完成"}},
	}}
	lookup := &fakeTool{name: "lookup", result: ToolResult{OK: true, Summary: "大结果", Data: big}}
	engine := newTestEngine(caller, store, lookup)

	events, _ := engine.RunTurn(context.Background(), "s1", &UserContent{Text: "查"})
	collected := collectEvents(t, events)
	if !hasEvent(collected, EventToolResult) || !hasEvent(collected, EventTurnDone) {
		t.Fatalf("tool result / turn_done missing: %+v", collected)
	}
	messages, _ := store.ListMessages(context.Background(), "s1")
	var sawToolResult bool
	for _, message := range messages {
		if message.Role != RoleToolResult {
			continue
		}
		sawToolResult = true
		if !json.Valid(message.Content) {
			t.Fatalf("persisted tool_result content is not valid JSON: %.80s", message.Content)
		}
	}
	if !sawToolResult {
		t.Fatalf("oversized tool result was never persisted (roles=%v)", store.roles("s1"))
	}
}

// 回归（W1-2）：审批补交的 APIKey 必须同步进内存 session——恢复路径同轮用
// 它构造工具上下文，只落库不同步会让首次批准的实测拿旧/空 key 跑。
func TestResumeApproval_SuppliedAPIKeyVisibleToTool(t *testing.T) {
	store := newFakeStore().seed(&Session{ID: "s1", Status: StatusIdle, Settings: Settings{ModelName: "m1", AllowLiveTest: PermissionAsk}})
	caller := &fakeCaller{responses: []scriptedResponse{
		{result: &CallResult{Text: "要测", ToolCalls: []relay.MaheshvaraToolCall{toolCall("c1", "probe", `{}`)}}},
		{result: &CallResult{Text: "完成"}},
	}}
	gotKey := ""
	probe := &fakeTool{name: "probe", gated: true, permKey: "live_test", result: ToolResult{OK: true},
		onExecute: func(tctx ToolContext) { _, gotKey = tctx.TestTarget() }}
	engine := newTestEngine(caller, store, probe)

	events, _ := engine.RunTurn(context.Background(), "s1", &UserContent{Text: "测"})
	collectEvents(t, events)
	resumeEvents, err := engine.ResumeApproval(context.Background(), "s1", ApprovalDecision{Approved: true, APIKey: "sk-approved"})
	if err != nil {
		t.Fatalf("ResumeApproval: %v", err)
	}
	collectEvents(t, resumeEvents)
	if probe.executions != 1 {
		t.Fatalf("approved tool not executed")
	}
	if gotKey != "sk-approved" {
		t.Fatalf("tool saw stale key %q during resumed turn, want sk-approved", gotKey)
	}
}

// 回归（W1-3）：审批恢复只豁免 ask 级暂停。批量 [ask 工具, never 工具] 一次
// 批准后，never 的那个仍必须被拒绝并回传拒绝结果。
func TestResumeApproval_BatchStillEnforcesNever(t *testing.T) {
	store := newFakeStore().seed(&Session{ID: "s1", Status: StatusIdle,
		Settings: Settings{ModelName: "m1", AllowLiveTest: PermissionAsk, AllowSave: PermissionNever}})
	caller := &fakeCaller{responses: []scriptedResponse{
		{result: &CallResult{Text: "一起做", ToolCalls: []relay.MaheshvaraToolCall{
			toolCall("c1", "probe", `{}`),
			toolCall("c2", "saver", `{}`),
		}}},
		{result: &CallResult{Text: "收到拒绝，改走只读路径"}},
	}}
	probe := &fakeTool{name: "probe", gated: true, permKey: "live_test", result: ToolResult{OK: true, Summary: "已测"}}
	saver := &fakeTool{name: "saver", gated: true, permKey: "save", result: ToolResult{OK: true, Summary: "已保存"}}
	engine := newTestEngine(caller, store, probe, saver)

	events, _ := engine.RunTurn(context.Background(), "s1", &UserContent{Text: "做"})
	collectEvents(t, events)
	session, _ := store.GetSession(context.Background(), "s1")
	if session.Status != StatusWaitingApproval || len(session.PendingAction.Calls) != 2 {
		t.Fatalf("pending should capture both calls: %+v", session.PendingAction)
	}

	resumeEvents, err := engine.ResumeApproval(context.Background(), "s1", ApprovalDecision{Approved: true})
	if err != nil {
		t.Fatalf("ResumeApproval: %v", err)
	}
	resumed := collectEvents(t, resumeEvents)
	if probe.executions != 1 {
		t.Fatalf("approved ask-level tool must run")
	}
	if saver.executions != 0 {
		t.Fatalf("never-level tool must NOT run even after batch approval")
	}
	if !hasEvent(resumed, EventTurnDone) {
		t.Fatalf("turn must continue after mixed batch: %+v", resumed)
	}
	// 拒绝结果回传模型
	last := caller.lastRequest()
	foundDenied := false
	for _, msg := range last.Messages {
		for _, part := range msg.Content {
			if part.Type == relay.MaheshvaraContentToolOutput && strings.Contains(part.ToolOutput, "denied") {
				foundDenied = true
			}
		}
	}
	if !foundDenied {
		t.Fatalf("never-denial not fed back to model")
	}
}

// 回归（W1-3）：审批挂起期间开启计划模式，批准后旧批次仍被计划模式拒绝。
func TestResumeApproval_PlanModeEnabledAfterPauseStillDenies(t *testing.T) {
	store := newFakeStore().seed(&Session{ID: "s1", Status: StatusWaitingApproval,
		PendingAction: &PendingAction{Calls: []relay.MaheshvaraToolCall{toolCall("c1", "saver", `{}`)}},
		Settings:      Settings{ModelName: "m1", AllowSave: PermissionAsk, PlanMode: true}})
	caller := &fakeCaller{responses: []scriptedResponse{
		{result: &CallResult{Text: "改走方案"}},
	}}
	saver := &fakeTool{name: "saver", gated: true, permKey: "save", result: ToolResult{OK: true}}
	engine := newTestEngine(caller, store, saver)

	events, err := engine.ResumeApproval(context.Background(), "s1", ApprovalDecision{Approved: true})
	if err != nil {
		t.Fatalf("ResumeApproval: %v", err)
	}
	collected := collectEvents(t, events)
	if saver.executions != 0 {
		t.Fatalf("plan mode must deny the approved call")
	}
	if !hasEvent(collected, EventTurnDone) {
		t.Fatalf("turn must continue with denial: %+v", collected)
	}
}

func (f *fakeStore) ResetRunningSessions(ctx context.Context) error { return nil }

// 回归（W2-20）：工具入参中的密钥字段落库前脱敏（apiKey/token/secret 等），
// 非密钥字段与嵌套结构不受影响；非法 JSON 原样返回不阻断。
func TestMaskSecretInputs(t *testing.T) {
	masked := MaskSecretInputs(json.RawMessage(`{"baseUrl":"http://x","apiKey":"sk-live-123","nested":{"api_key":"k2","name":"ok"},"token":"t1","note":"keep"}`))
	var decoded map[string]any
	if err := json.Unmarshal(masked, &decoded); err != nil {
		t.Fatalf("masked output invalid: %s", masked)
	}
	if decoded["apiKey"] != "***" {
		t.Fatalf("apiKey not masked: %v", decoded["apiKey"])
	}
	nested, _ := decoded["nested"].(map[string]any)
	if nested == nil || nested["api_key"] != "***" || nested["name"] != "ok" {
		t.Fatalf("nested masking wrong: %v", decoded["nested"])
	}
	if decoded["token"] != "***" || decoded["note"] != "keep" || decoded["baseUrl"] != "http://x" {
		t.Fatalf("unexpected collateral masking: %s", masked)
	}
	broken := MaskSecretInputs(json.RawMessage(`{oops`))
	if string(broken) != `{oops` {
		t.Fatalf("invalid JSON should pass through, got %s", broken)
	}
}

// ---- ask_user / 方案确认链路（bb8bb98 回归） ----

// stubAskParser 返回带测试用 ask_user 解析器的引擎 Options（生产经
// protocolAgentEngine 的 Options.ParseAsk 注入）。
func stubAskParser(t *testing.T) Options {
	t.Helper()
	return Options{ParseAsk: func(call relay.MaheshvaraToolCall) (AskQuestion, bool) {
		if call.Name != ToolNameAskUser {
			return AskQuestion{}, false
		}
		var payload struct {
			Question    string `json:"question"`
			AllowCustom bool   `json:"allow_custom"`
		}
		if err := json.Unmarshal(call.Arguments, &payload); err != nil || payload.Question == "" {
			return AskQuestion{}, false
		}
		return AskQuestion{CallID: call.ID, Question: payload.Question, AllowCustom: payload.AllowCustom}, true
	}, TurnTimeout: 5 * time.Second}
}

// ask_user 暂停后带作答恢复：答案要送进模型，同批其余调用必须有取消结果
// （悬挂 tool_calls 会让下一次模型调用被上游 400）。
func TestResumeApproval_QuestionAnswerFeedsModelAndCancelsRest(t *testing.T) {
	opts := stubAskParser(t)
	store := newFakeStore().seed(&Session{ID: "s1", Status: StatusIdle, Settings: Settings{ModelName: "m1"}})
	caller := &fakeCaller{responses: []scriptedResponse{
		{result: &CallResult{Text: "先确认方向", ToolCalls: []relay.MaheshvaraToolCall{
			toolCall("q1", "ask_user", `{"question":"用哪个源？"}`),
			toolCall("c2", "lookup", `{}`),
		}}},
		{result: &CallResult{Text: "好的，用源A继续"}},
	}}
	lookup := &fakeTool{name: "lookup", result: ToolResult{OK: true, Summary: "查询成功"}}
	engine := newTestEngineWithOptions(caller, store, opts, lookup)

	events, _ := engine.RunTurn(context.Background(), "s1", &UserContent{Text: "接入"})
	collected := collectEvents(t, events)
	if !hasEvent(collected, EventApprovalPending) {
		t.Fatalf("missing approval_required: %+v", collected)
	}
	session, _ := store.GetSession(context.Background(), "s1")
	if session.PendingAction == nil || session.PendingAction.Kind != PendingKindQuestion || session.PendingAction.Question == nil {
		t.Fatalf("pending action = %+v", session.PendingAction)
	}

	resumeEvents, err := engine.ResumeApproval(context.Background(), "s1", ApprovalDecision{Approved: true, Answer: "源A"})
	if err != nil {
		t.Fatalf("ResumeApproval: %v", err)
	}
	resumed := collectEvents(t, resumeEvents)
	if !hasEvent(resumed, EventTurnDone) {
		t.Fatalf("resume missing turn_done: %+v", resumed)
	}
	if lookup.executions != 0 {
		t.Fatalf("trailing call must be cancelled, not executed (%d times)", lookup.executions)
	}
	// 作答后的模型请求里：ask_user 有答案、尾随调用有取消结果。
	last := caller.lastRequest()
	toolOutputs := 0
	sawAnswer, sawCancel := false, false
	for _, message := range last.Messages {
		if message.Role != "tool" {
			continue
		}
		toolOutputs++
		for _, part := range message.Content {
			if strings.Contains(part.ToolOutput, "源A") {
				sawAnswer = true
			}
			if strings.Contains(part.ToolOutput, "已取消") {
				sawCancel = true
			}
		}
	}
	if toolOutputs != 2 || !sawAnswer || !sawCancel {
		t.Fatalf("model request after resume: toolOutputs=%d answer=%v cancel=%v", toolOutputs, sawAnswer, sawCancel)
	}
	// 恢复完成后 pending 必须被清掉，避免二次批准。
	after, _ := store.GetSession(context.Background(), "s1")
	if after.PendingAction != nil {
		t.Fatalf("pending action not cleared after resume: %+v", after.PendingAction)
	}
}

// 方案定稿暂停（plan 型，无 Calls）→ 确认后关闭计划模式并续跑。
func TestPlanReadyPausesAndApprovalClosesPlanMode(t *testing.T) {
	store := newFakeStore().seed(&Session{ID: "s1", Status: StatusIdle, Settings: Settings{ModelName: "m1", PlanMode: true}})
	caller := &fakeCaller{responses: []scriptedResponse{
		{result: &CallResult{Text: "方案如下", ToolCalls: []relay.MaheshvaraToolCall{
			toolCall("p1", "update_plan", `{"plan":[{"title":"建源","status":"pending"},{"title":"测试","status":"pending"}],"ready_for_approval":true}`),
		}}},
		{result: &CallResult{Text: "开始执行"}},
	}}
	planTool := &fakeTool{name: "update_plan", result: ToolResult{OK: true, Summary: "方案已更新"},
		onExecute: func(tctx ToolContext) {
			_ = tctx.SetPlan([]PlanStep{{Title: "建源", Status: "pending"}, {Title: "测试", Status: "pending"}})
		}}
	engine := newTestEngine(caller, store, planTool)

	events, _ := engine.RunTurn(context.Background(), "s1", &UserContent{Text: "接入"})
	collected := collectEvents(t, events)
	if !hasEvent(collected, EventApprovalPending) {
		t.Fatalf("missing approval_required: %+v", collected)
	}
	session, _ := store.GetSession(context.Background(), "s1")
	if session.PendingAction == nil || session.PendingAction.Kind != PendingKindPlan || len(session.PendingAction.Plan) != 2 {
		t.Fatalf("plan pending = %+v", session.PendingAction)
	}

	resumeEvents, err := engine.ResumeApproval(context.Background(), "s1", ApprovalDecision{Approved: true})
	if err != nil {
		t.Fatalf("plan-kind resume must not be rejected by the Calls guard: %v", err)
	}
	resumed := collectEvents(t, resumeEvents)
	if !hasEvent(resumed, EventTurnDone) {
		t.Fatalf("resume missing turn_done: %+v", resumed)
	}
	after, _ := store.GetSession(context.Background(), "s1")
	if after.Settings.PlanMode {
		t.Fatalf("plan mode must be off after approval")
	}
	if after.PendingAction != nil {
		t.Fatalf("pending action not cleared: %+v", after.PendingAction)
	}
}

// 方案无变化但声明定稿：ready 标志仍要触发暂停（回归：notePlanReady 曾挂在
// planStepsEqual 分支下，原样重发步骤时确认流程凭空消失）。
func TestPlanReadyTriggersEvenWhenPlanUnchanged(t *testing.T) {
	steps := []PlanStep{{Title: "建源", Status: "pending"}}
	store := newFakeStore().seed(&Session{ID: "s1", Status: StatusIdle, Plan: steps, Settings: Settings{ModelName: "m1", PlanMode: true}})
	caller := &fakeCaller{responses: []scriptedResponse{
		{result: &CallResult{Text: "方案不变，请求确认", ToolCalls: []relay.MaheshvaraToolCall{
			toolCall("p1", "update_plan", `{"plan":[{"title":"建源","status":"pending"}],"ready_for_approval":true}`),
		}}},
		{result: &CallResult{Text: "收到确认"}},
	}}
	planTool := &fakeTool{name: "update_plan", result: ToolResult{OK: true, Summary: "方案已更新"},
		onExecute: func(tctx ToolContext) { _ = tctx.SetPlan(steps) }}
	engine := newTestEngine(caller, store, planTool)

	events, _ := engine.RunTurn(context.Background(), "s1", &UserContent{Text: "确认一下"})
	collected := collectEvents(t, events)
	if !hasEvent(collected, EventApprovalPending) {
		t.Fatalf("ready_for_approval with unchanged plan must still pause: %+v", collected)
	}
}

// maskedPendingAction 必须保留 Kind/Question/Plan——前端靠它们选卡型。
func TestMaskedPendingAction_PreservesKindQuestionPlan(t *testing.T) {
	plan := []PlanStep{{Title: "步骤", Status: "pending"}}
	pending := &PendingAction{
		Kind:  PendingKindPlan,
		Plan:  plan,
		Calls: []relay.MaheshvaraToolCall{toolCall("c1", "test_upstream", `{"apiKey":"sk-1"}`)},
	}
	masked := MaskedPendingAction(pending)
	if masked.Kind != PendingKindPlan || len(masked.Plan) != 1 || masked.Plan[0].Title != "步骤" {
		t.Fatalf("kind/plan lost: %+v", masked)
	}
	if strings.Contains(string(masked.Calls[0].Arguments), "sk-1") {
		t.Fatalf("arguments must stay masked: %s", masked.Calls[0].Arguments)
	}
	if strings.Contains(string(pending.Calls[0].Arguments), "***") {
		t.Fatalf("original pending must stay unmasked for execution")
	}

	question := &PendingAction{Kind: PendingKindQuestion, Question: &AskQuestion{CallID: "q1", Question: "选哪个？", AllowCustom: false}}
	maskedQ := MaskedPendingAction(question)
	if maskedQ.Kind != PendingKindQuestion || maskedQ.Question == nil || maskedQ.Question.CallID != "q1" {
		t.Fatalf("question lost: %+v", maskedQ)
	}
}

// ---- 上下文摘要压缩（d134314 回归） ----

func TestSummaryCut_LandsOnUserBoundary(t *testing.T) {
	roles := func(values ...string) []relay.MaheshvaraMessage {
		messages := make([]relay.MaheshvaraMessage, 0, len(values))
		for _, value := range values {
			messages = append(messages, relay.MaheshvaraMessage{Role: value})
		}
		return messages
	}
	if cut := summaryCut(roles("user", "assistant", "tool", "assistant", "user", "assistant", "tool", "assistant", "user")); cut != 4 {
		t.Fatalf("cut = %d, want 4 (the user message at the midpoint)", cut)
	}
	// 中点附近没有 user 边界：放弃而不是切在批中间。
	if cut := summaryCut(roles("user", "assistant", "tool", "assistant", "tool", "assistant", "user", "assistant", "user")); cut != -1 {
		t.Fatalf("cut = %d, want -1 (no user boundary before midpoint)", cut)
	}
	if cut := summaryCut(roles("user", "assistant", "user")); cut != -1 {
		t.Fatalf("short conversation must refuse: %d", cut)
	}
}

// 轮首摘要：切点落在 user 边界、boundary 记被摘要前缀的真实 seq；下一轮
// loadConversation 回放「摘要 + 保留半段原文」，被摘要内容不再出现。
func TestTurnStartSummary_RecordsBoundaryAndReplaysKeptHalf(t *testing.T) {
	ctx := context.Background()
	store := newFakeStore().seed(&Session{ID: "s1", Status: StatusIdle, Settings: Settings{ModelName: "m1"}})
	bigData := json.RawMessage(`{"data":"` + strings.Repeat("x", 15_000) + `"}`)
	mustAppend := func(role string, content any) int {
		seq, err := store.AppendMessage(ctx, "s1", role, content, "", nil)
		if err != nil {
			t.Fatalf("append %s: %v", role, err)
		}
		return seq
	}
	seqFirst := mustAppend(RoleUser, UserContent{Text: "marker-first 第一轮问题"})
	mustAppend(RoleAssistant, AssistantContent{ToolCalls: []relay.MaheshvaraToolCall{toolCall("c1", "lookup", `{}`)}})
	mustAppend(RoleToolResult, ToolResultInfo{CallID: "c1", Name: "lookup", OK: true, Data: bigData})
	seqMid := mustAppend(RoleAssistant, AssistantContent{Text: "第一轮结论"})
	mustAppend(RoleUser, UserContent{Text: "marker-second 第二轮问题"})
	mustAppend(RoleAssistant, AssistantContent{ToolCalls: []relay.MaheshvaraToolCall{toolCall("c2", "lookup", `{}`)}})
	mustAppend(RoleToolResult, ToolResultInfo{CallID: "c2", Name: "lookup", OK: true, Data: bigData})
	mustAppend(RoleAssistant, AssistantContent{Text: "第二轮结论"})
	if seqFirst == 0 || seqMid == 0 {
		t.Fatalf("seqs not assigned")
	}

	caller := &fakeCaller{responses: []scriptedResponse{
		{result: &CallResult{Text: "摘要：目标是接入协议 marker-summary"}},
		{result: &CallResult{Text: "继续完成"}},
	}}
	engine := newTestEngineWithOptions(caller, store, Options{ContextWindowTokens: 11_000, TurnTimeout: 5 * time.Second})

	events, _ := engine.RunTurn(ctx, "s1", &UserContent{Text: "继续"})
	collected := collectEvents(t, events)
	if !hasEvent(collected, EventTurnDone) {
		t.Fatalf("turn must complete: %+v", collected)
	}

	// 摘要消息已落库，boundary 是被摘要前缀（m1..m4）的最后一条 seq，
	// 而不是压缩时刻的最新 seq——否则保留半段下一轮会被静默丢掉。
	messages, _ := store.ListMessages(ctx, "s1")
	var summary *Message
	for index := range messages {
		if messages[index].Role != RoleSystem {
			continue
		}
		var content SystemContent
		if json.Unmarshal(messages[index].Content, &content) == nil && content.Kind == "summary" {
			summary = &messages[index]
		}
	}
	if summary == nil {
		t.Fatalf("summary message not persisted: %+v", messages)
	}
	var content SystemContent
	if err := json.Unmarshal(summary.Content, &content); err != nil {
		t.Fatalf("summary content: %v", err)
	}
	if content.BoundarySeq != seqMid {
		t.Fatalf("boundary = %d, want seq of last summarized message = %d", content.BoundarySeq, seqMid)
	}

	// 本轮发给模型的对话：保留半段原文在、被摘要前缀不在、摘要在。
	last := caller.lastRequest()
	var sawKept, sawSummary, sawDropped bool
	for _, message := range last.Messages {
		for _, part := range message.Content {
			if strings.Contains(part.Text, "marker-second") || strings.Contains(part.ToolOutput, "marker-second") {
				sawKept = true
			}
			if strings.Contains(part.Text, "marker-summary") {
				sawSummary = true
			}
			if strings.Contains(part.Text, "marker-first") {
				sawDropped = true
			}
		}
	}
	if !sawKept || !sawSummary || sawDropped {
		t.Fatalf("kept=%v summary=%v dropped=%v", sawKept, sawSummary, sawDropped)
	}

	// 下一轮回放：摘要替代前缀，保留半段仍在。
	session, _ := store.GetSession(ctx, "s1")
	replayed, _, err := engine.loadConversation(ctx, session)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	var replayKept, replayDropped, replaySummary bool
	for _, message := range replayed {
		for _, part := range message.Content {
			text := part.Text + part.ToolOutput
			if strings.Contains(text, "marker-second") {
				replayKept = true
			}
			if strings.Contains(text, "marker-first") {
				replayDropped = true
			}
			if strings.Contains(text, "以下是此前对话的摘要") {
				replaySummary = true
			}
		}
	}
	if !replayKept || replayDropped || !replaySummary {
		t.Fatalf("replay kept=%v dropped=%v summary=%v", replayKept, replayDropped, replaySummary)
	}
	// 保留段的 tool 结果必须跟在自己的 assistant tool_calls 之后（无孤儿）。
	firstTool := -1
	firstAssistantWithCalls := -1
	for index, message := range replayed {
		if message.Role == "tool" && firstTool < 0 {
			firstTool = index
		}
		if message.Role == "assistant" && len(message.ToolCalls) > 0 && firstAssistantWithCalls < 0 {
			firstAssistantWithCalls = index
		}
	}
	if firstTool >= 0 && firstAssistantWithCalls < 0 {
		t.Fatalf("orphan tool message at %d without preceding tool_calls", firstTool)
	}
	if firstTool >= 0 && firstAssistantWithCalls >= firstTool {
		t.Fatalf("tool message at %d precedes its tool_calls at %d", firstTool, firstAssistantWithCalls)
	}
}

func newTestEngineWithOptions(caller StreamCaller, store Store, opts Options, tools ...Tool) *Engine {
	registry, err := NewRegistry(tools...)
	if err != nil {
		panic(err)
	}
	return NewEngine(caller, store, registry, nil, func(*Session) string { return "test prompt" }, opts)
}

// 方案陈旧计数节奏：第 planStaleCalls 轮提醒一次并复位，空方案恒不提醒；
// update_plan 归零（外部置 0）后重新累计。
func TestAdvancePlanStaleRoundsCadence(t *testing.T) {
	session := &Session{Plan: []PlanStep{{Title: "步骤"}}}
	for round := 1; round < planStaleCalls; round++ {
		if advancePlanStaleRounds(session) {
			t.Fatalf("round %d must not nudge before planStaleCalls", round)
		}
	}
	if !advancePlanStaleRounds(session) {
		t.Fatalf("round %d must nudge", planStaleCalls)
	}
	if session.PlanStaleRounds != 0 {
		t.Fatalf("counter must reset after nudge, got %d", session.PlanStaleRounds)
	}
	if advancePlanStaleRounds(session) {
		t.Fatal("round right after nudge must not nudge again")
	}
	empty := &Session{}
	if advancePlanStaleRounds(empty) || empty.PlanStaleRounds != 0 {
		t.Fatal("empty plan must never advance or nudge")
	}
}
