package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/bytedance/sonic"
	"github.com/elysia-api/backend/internal/jsonutil"
)

type secretResultTool struct {
	*fakeTool
	isParallel bool
}

type failingToolResultStore struct {
	*fakeStore
	attempted json.RawMessage
}

func (store *failingToolResultStore) AppendMessage(ctx context.Context, sessionID, role string, content any, model string, usage json.RawMessage) (int, error) {
	if role == RoleToolResult {
		store.attempted, _ = sonic.ConfigStd.Marshal(content)
		return 0, errors.New("injected tool result write failure")
	}
	return store.fakeStore.AppendMessage(ctx, sessionID, role, content, model, usage)
}

func TestToolResultWriteFailureKeepsModelCopyRedacted(t *testing.T) {
	const secret = "private-marker-generated-secret"
	store := newFakeStore().seed(&Session{ID: "write-failure", Status: StatusIdle, Settings: Settings{ModelName: "test"}})
	failing := &failingToolResultStore{fakeStore: store}
	tool := &fakeTool{name: "secret", result: ToolResult{OK: true, Summary: secret, Data: json.RawMessage(`{"key":"` + secret + `","count":9007199254740993}`), SecretValues: []string{secret}}}
	caller := &fakeCaller{responses: []scriptedResponse{{result: &CallResult{ToolCalls: []FunctionCall{toolCall("one", "secret", `{}`)}}}, {result: &CallResult{Text: "done"}}}}
	engine := newTestEngine(caller, store, tool)
	engine.store = failing
	events, err := engine.RunTurn(t.Context(), "write-failure", &UserContent{Text: "test"})
	if err != nil {
		t.Fatal(err)
	}
	collectEvents(t, events)
	if tool.executions != 1 || caller.requestCount() != 2 {
		t.Fatalf("executions=%d, model calls=%d", tool.executions, caller.requestCount())
	}
	request, _ := sonic.ConfigStd.Marshal(caller.lastRequest())
	for _, raw := range [][]byte{request, failing.attempted} {
		if strings.Contains(string(raw), secret) || !strings.Contains(string(raw), "9007199254740993") {
			t.Fatalf("unsafe result after storage failure: %s", raw)
		}
	}
}

func (tool *secretResultTool) Meta() ToolMeta { return ToolMeta{ConcurrentSafe: tool.isParallel} }

func TestToolSecretsStayOutOfModelAndHistory(t *testing.T) {
	for _, mode := range []string{"serial", "parallel", "approval", "truncated", "prepared"} {
		t.Run(mode, func(t *testing.T) {
			secret := "private-marker-" + strings.Repeat("secret", 40)
			data := json.RawMessage(`{"key":"` + secret + `","count":9007199254740993}`)
			store := newFakeStore().seed(&Session{ID: "secret", Status: StatusIdle, Settings: Settings{ModelName: "test", AllowLiveTest: PermissionAsk}})
			first := &secretResultTool{fakeTool: &fakeTool{name: "first", gated: mode == "approval", result: ToolResult{OK: true, Summary: secret, Data: data, SecretValues: []string{secret}}}, isParallel: mode == "parallel"}
			if first.gated {
				first.permKey = "live_test"
			}
			if mode == "prepared" {
				first.result.Data = json.RawMessage(`{"key":"private-marker-fragment","count":9007199254740993}`)
				first.result.RedactedData = json.RawMessage(`{"key":"***","count":9007199254740993}`)
			}
			tools := []Tool{first}
			calls := []FunctionCall{toolCall("one", "first", `{}`)}
			if mode == "parallel" {
				tools = append(tools, &secretResultTool{fakeTool: &fakeTool{name: "second", result: first.result}, isParallel: true})
				calls = append(calls, toolCall("two", "second", `{}`))
			}
			caller := &fakeCaller{responses: []scriptedResponse{{result: &CallResult{ToolCalls: calls}}, {result: &CallResult{Text: "done"}}}}
			engine := newTestEngine(caller, store, tools...)
			if mode == "truncated" {
				engine.opts.ToolResultStoreLimit = 128
			}
			events, err := engine.RunTurn(context.Background(), "secret", &UserContent{Text: "test"})
			if err != nil {
				t.Fatal(err)
			}
			collected := collectEvents(t, events)
			if mode == "approval" {
				events, err = engine.ResumeApproval(context.Background(), "secret", ApprovalDecision{Approved: true})
				if err != nil {
					t.Fatal(err)
				}
				collected = append(collected, collectEvents(t, events)...)
			}
			if first.executions != 1 || caller.requestCount() != 2 {
				t.Fatalf("tool executions=%d, model calls=%d", first.executions, caller.requestCount())
			}
			request, _ := sonic.ConfigStd.Marshal(caller.lastRequest())
			if strings.Contains(string(request), "private-marker") || !strings.Contains(string(request), "9007199254740993") {
				t.Fatalf("model result leaked or changed numeric precision: %s", request)
			}
			messages, _ := store.ListMessages(context.Background(), "secret")
			for _, message := range messages {
				if strings.Contains(string(message.Content), "private-marker") {
					t.Fatal("secret persisted")
				}
			}
			hasLiveSecret := false
			for _, event := range collected {
				if event.Type == EventToolResult && event.Result != nil {
					hasLiveSecret = hasLiveSecret || strings.Contains(event.Result.Summary, "private-marker")
					if event.Message == nil || strings.Contains(string(event.Message.Content), "private-marker") {
						t.Fatal("unsafe replay message")
					}
				}
			}
			if !hasLiveSecret {
				t.Fatal("one-time client delivery disappeared")
			}
			session, _ := store.GetSession(context.Background(), "secret")
			conversation, _, err := engine.loadConversation(context.Background(), session)
			if err != nil {
				t.Fatal(err)
			}
			replay, _ := sonic.ConfigStd.Marshal(conversation)
			if strings.Contains(string(replay), "private-marker") {
				t.Fatal("secret in history replay")
			}
		})
	}
}

func TestInvalidToolResultIsFailureWithoutRetry(t *testing.T) {
	for name, data := range map[string]any{"raw": json.RawMessage(`{"key":"secret"`), "unsupported": make(chan int)} {
		t.Run(name, func(t *testing.T) {
			store := newFakeStore().seed(&Session{ID: "invalid", Status: StatusIdle, Settings: Settings{ModelName: "test"}})
			tool := &fakeTool{name: "invalid", result: ToolResult{OK: true, Data: data}}
			caller := &fakeCaller{responses: []scriptedResponse{{result: &CallResult{ToolCalls: []FunctionCall{toolCall("call", "invalid", `{}`)}}}, {result: &CallResult{Text: "done"}}}}
			engine := newTestEngine(caller, store, tool)
			events, err := engine.RunTurn(context.Background(), "invalid", &UserContent{Text: "test"})
			if err != nil {
				t.Fatal(err)
			}
			for _, event := range collectEvents(t, events) {
				if event.Type == EventToolResult && (event.Result.OK || !strings.Contains(string(event.Result.Data), "invalid_tool_result")) {
					t.Fatalf("invalid result reported success: %+v", event.Result)
				}
			}
			if tool.executions != 1 {
				t.Fatalf("executions=%d", tool.executions)
			}
			request, _ := sonic.ConfigStd.Marshal(caller.lastRequest())
			if !strings.Contains(string(request), "invalid_tool_result") {
				t.Fatalf("missing failure result: %s", request)
			}
		})
	}
}

func TestMaskingPreservesNumbersAndRejectsInvalidJSON(t *testing.T) {
	raw := json.RawMessage(`{"token":"private-marker","n":9007199254740993}`)
	for _, masked := range [][]byte{MaskSecretData(raw, []string{"private-marker"}), MaskSecretInputs(raw)} {
		if strings.Contains(string(masked), "private-marker") || !strings.Contains(string(masked), "9007199254740993") {
			t.Fatalf("incorrect masking: %s", masked)
		}
	}
	if masked := MaskSecretData([]byte(`private-marker`), []string{"private-marker"}); !jsonutil.Valid(masked) || strings.Contains(string(masked), "private-marker") {
		t.Fatalf("invalid JSON escaped masking: %s", masked)
	}
}
