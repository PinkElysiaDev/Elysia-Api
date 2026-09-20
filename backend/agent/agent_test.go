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
	if update.Title != "" {
		session.Title = update.Title
	}
	if update.TestBaseURL != "" {
		session.TestBaseURL = update.TestBaseURL
	}
	if update.TestAPIKey != "" {
		session.TestAPIKey = update.TestAPIKey
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
	setDraft   json.RawMessage // 执行时写入的草稿
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
	if got := registry.GatedTools(); len(got) != 1 || got[0].Name() != "b" {
		t.Fatalf("gated = %+v", got)
	}
}
