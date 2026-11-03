package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/elysia-api/backend/protocol"
)

func TestTurnEventStreamClosesAfterCleanup(t *testing.T) {
	store := newFakeStore().seed(&Session{ID: "s1", Status: StatusIdle, Settings: Settings{ModelName: "m1"}})
	caller := &fakeCaller{responses: []scriptedResponse{{result: &CallResult{Text: "done"}}}}
	engine := newTestEngine(caller, store)
	ctx, handle, cancel, err := engine.beginTurn("s1", engine.opts.TurnTimeout)
	if err != nil {
		t.Fatal(err)
	}
	cancelStarted, releaseCancel := make(chan struct{}), make(chan struct{})
	events := engine.spawnTurn(ctx, "s1", handle, func() {
		cancel()
		close(cancelStarted)
		<-releaseCancel
	}, &UserContent{Text: "hi"}, nil, ApprovalDecision{})
	<-cancelStarted
	// The callback is a deterministic point inside deferred cleanup. Consumers
	// must not observe EOF while this turn still owns the session guard.
	isClosed := false
drain:
	for {
		select {
		case _, isOpen := <-events:
			if !isOpen {
				isClosed = true
				break drain
			}
		default:
			break drain
		}
	}
	close(releaseCancel)
	collectEvents(t, events)
	if isClosed {
		t.Fatal("event stream closed before deferred cleanup completed")
	}
	if engine.IsRunning("s1") {
		t.Fatal("completed stream retained the session guard")
	}
}

type approvalFailureStore struct {
	Store
	stage string
}

func (store approvalFailureStore) UpdateSessionState(ctx context.Context, id string, update SessionStateUpdate) error {
	if (store.stage == "pause" && update.PendingAction != nil) ||
		(store.stage == "plan" && update.SettingsPlanMode != nil) ||
		(store.stage == "target" && update.TestBaseURL != "") ||
		(store.stage == "clear" && update.ClearPending) {
		return errors.New("injected approval persistence failure")
	}
	return store.Store.UpdateSessionState(ctx, id, update)
}

func TestPausePersistenceFailureEndsTurn(t *testing.T) {
	for _, kind := range []string{"plan", "question"} {
		t.Run(kind, func(t *testing.T) {
			store := newFakeStore().seed(&Session{ID: "s1", Status: StatusIdle, Settings: Settings{ModelName: "m1", PlanMode: kind == "plan"}})
			call := toolCall("p1", "update_plan", `{"ready_for_approval":true}`)
			if kind == "question" {
				call = toolCall("q1", ToolNameAskUser, `{"question":"Continue?"}`)
			}
			caller := &fakeCaller{responses: []scriptedResponse{
				{result: &CallResult{ToolCalls: []FunctionCall{call}}},
				{result: &CallResult{Text: "must not continue"}},
			}}
			tool := &fakeTool{name: "update_plan", result: ToolResult{OK: true}}
			engine := newTestEngine(caller, approvalFailureStore{Store: store, stage: "pause"}, tool)
			engine.opts.ParseAsk = stubAskParser(t).ParseAsk
			events, err := engine.RunTurn(t.Context(), "s1", &UserContent{Text: "start"})
			if err != nil {
				t.Fatal(err)
			}
			collected := collectEvents(t, events)
			after, err := store.GetSession(t.Context(), "s1")
			if err != nil || after.Status != StatusIdle || after.PendingAction != nil || engine.IsRunning("s1") {
				t.Fatalf("failed pause retained running state: %+v %v", after, err)
			}
			if !hasEvent(collected, EventError) || !hasEvent(collected, EventTurnDone) || hasEvent(collected, EventApprovalPending) || caller.requestCount() != 1 {
				t.Fatalf("failed pause continued or reported success: calls=%d events=%+v", caller.requestCount(), collected)
			}
		})
	}
}

func TestResumePersistenceFailureDoesNotCallModel(t *testing.T) {
	for _, stage := range []string{"plan", "target", "clear"} {
		t.Run(stage, func(t *testing.T) {
			pending := &PendingAction{Calls: []FunctionCall{toolCall("c1", "probe", `{}`)}}
			if stage == "plan" {
				pending = &PendingAction{Kind: PendingKindPlan, Plan: []PlanStep{{Title: "step", Status: "pending"}}}
			}
			store := newFakeStore().seed(&Session{ID: "s1", Status: StatusWaitingApproval, PendingAction: pending, TestBaseURL: "https://old.example", Settings: Settings{ModelName: "m1", PlanMode: stage == "plan"}})
			caller := &fakeCaller{responses: []scriptedResponse{{result: &CallResult{Text: "must not run"}}}}
			tool := &fakeTool{name: "probe", gated: true, permKey: "live_test", result: ToolResult{OK: true}}
			engine := newTestEngine(caller, approvalFailureStore{Store: store, stage: stage}, tool)
			events, err := engine.ResumeApproval(t.Context(), "s1", ApprovalDecision{Approved: true, BaseURL: "https://new.example"})
			if err != nil {
				t.Fatal(err)
			}
			collected := collectEvents(t, events)
			if !hasEvent(collected, EventError) || caller.requestCount() != 0 {
				t.Fatalf("failed persistence continued execution: calls=%d events=%+v", caller.requestCount(), collected)
			}
			wantExecutions := 0
			if stage == "clear" {
				wantExecutions = 1 // The completed tool is never repeated to repair its receipt.
			}
			if tool.executions != wantExecutions {
				t.Fatalf("tool executions=%d, want %d", tool.executions, wantExecutions)
			}
			after, err := store.GetSession(t.Context(), "s1")
			if err != nil || after.Status != StatusIdle || engine.IsRunning("s1") {
				t.Fatalf("failed resume remained active: %+v %v", after, err)
			}
			if stage == "plan" && !after.Settings.PlanMode || stage == "target" && after.TestBaseURL != "https://old.example" {
				t.Fatal("failed settings update changed the previous value")
			}
			if _, err := engine.ResumeApproval(t.Context(), "s1", ApprovalDecision{Approved: true}); !errors.Is(err, ErrNoPendingApproval) {
				t.Fatalf("failed turn allowed stale approval to execute again: %v", err)
			}
			if stage == "clear" {
				events, err := engine.RunTurn(t.Context(), "s1", &UserContent{Text: "new instruction"})
				if err != nil {
					t.Fatal(err)
				}
				continued := collectEvents(t, events)
				if hasEvent(continued, EventToolResult) || hasEvent(continued, EventError) || caller.requestCount() != 1 || tool.executions != 1 {
					t.Fatal("idle stale pending was treated as unexecuted approval", continued)
				}
			}
		})
	}
}

func TestSupersededApprovalPersistence(t *testing.T) {
	for _, scenario := range []string{"clear-failure", "result-write-failure", "question", "plan"} {
		t.Run(scenario, func(t *testing.T) {
			call := toolCall("pending", "probe", `{}`)
			pending := &PendingAction{Calls: []FunctionCall{call}}
			if scenario == "question" {
				call = toolCall("pending", ToolNameAskUser, `{"question":"Continue?"}`)
				pending.Calls = []FunctionCall{call}
				pending.Kind = PendingKindQuestion
				pending.Question = &AskQuestion{CallID: call.ID, Question: "Continue?"}
			} else if scenario == "plan" {
				pending = &PendingAction{Kind: PendingKindPlan, Plan: []PlanStep{{Title: "step", Status: "pending"}}}
			}
			store := newFakeStore().seed(&Session{ID: "s1", Status: StatusWaitingApproval, PendingAction: pending, Settings: Settings{ModelName: "m1"}})
			if len(pending.Calls) > 0 {
				if _, err := store.AppendMessage(t.Context(), "s1", RoleAssistant, AssistantContent{ToolCalls: pending.Calls}, "m1", nil); err != nil {
					t.Fatal(err)
				}
			}
			var selected Store = store
			if scenario == "clear-failure" {
				selected = approvalFailureStore{Store: store, stage: "clear"}
			} else if scenario == "result-write-failure" {
				selected = &failingToolResultStore{fakeStore: store}
			}
			caller := &fakeCaller{responses: []scriptedResponse{{result: &CallResult{Text: "done"}}}}
			tool := &fakeTool{name: "probe", result: ToolResult{OK: true}}
			engine := newTestEngine(caller, selected, tool)
			events, err := engine.RunTurn(t.Context(), "s1", &UserContent{Text: "new instruction"})
			if err != nil {
				t.Fatal(err)
			}
			collected := collectEvents(t, events)
			if tool.executions != 0 || engine.IsRunning("s1") {
				t.Fatalf("supersession executed a tool or retained the turn: executions=%d", tool.executions)
			}
			if scenario == "clear-failure" {
				if !hasEvent(collected, EventError) || caller.requestCount() != 0 || hasEvent(collected, EventToolResult) {
					t.Fatal("failed approval retirement continued", collected)
				}
				for _, role := range store.roles("s1") {
					if role == RoleUser || role == RoleToolResult {
						t.Fatal("failed retirement added replacement history", role)
					}
				}
				return
			}
			if hasEvent(collected, EventError) || !hasEvent(collected, EventTurnDone) || caller.requestCount() != 1 {
				t.Fatal("replacement model call did not finish exactly once", collected, caller.requestCount())
			}
			var results int
			request := caller.lastRequest()
			for _, node := range request.Content[:len(request.Content)-1] {
				if node.Kind == protocol.ToolResultNode {
					results++
					if !node.CallID.IsString(call.ID) || !strings.Contains(node.Payload.JSONString(), "未执行") {
						t.Fatal("missing unexecuted result in model history", node)
					}
				}
			}
			if results != len(pending.Calls) || !request.Content[len(request.Content)-1].Role.IsString(RoleUser) {
				t.Fatal("replacement reordered or fabricated tool results", request.Content)
			}
			if scenario == "result-write-failure" {
				hasWriteFailure := false
				for _, event := range collected {
					hasWriteFailure = hasWriteFailure || event.Type == EventStatus && strings.Contains(event.Text, "工具结果落库失败")
				}
				if !hasWriteFailure || hasEvent(collected, EventToolResult) {
					t.Fatal("result write failure was hidden or published as durable", collected)
				}
			}
			after, err := store.GetSession(t.Context(), "s1")
			if err != nil || after.PendingAction != nil || after.Status != StatusIdle {
				t.Fatal("replacement retained stale approval", after, err)
			}
		})
	}
}
