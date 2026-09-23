package storage

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/elysia-api/backend/agent"
	"github.com/elysia-api/backend/relay"
)

func newAgentTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(t.TempDir() + "/agent-test.sqlite3")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func TestAgentSessionCRUDAndMessages(t *testing.T) {
	ctx := context.Background()
	store := newAgentTestStore(t)

	created, err := store.CreateAgentSession(ctx, AgentSessionUpsert{
		Title:      "DashScope 接入",
		Mode:       agent.ModeEdit,
		ProtocolID: "vendor-x",
		SeedConfig: `{"id":"vendor-x","request":{"path":"/v1/x"}}`,
		Settings:   agent.Settings{ModelSourceID: "src1", ModelName: "gpt-x", AllowLiveTest: "always"},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if !strings.HasPrefix(created.ID, "as_") {
		t.Fatalf("id = %q", created.ID)
	}
	if string(created.SeedConfig) == "" || string(created.DraftConfig) == "" {
		t.Fatalf("seed/draft not populated")
	}
	if created.Status != agent.StatusIdle {
		t.Fatalf("status = %q", created.Status)
	}

	// 消息追加 + seq 递增
	seq1, err := store.AppendMessage(ctx, created.ID, agent.RoleUser, agent.UserContent{Text: "hi"}, "", nil)
	if err != nil || seq1 != 1 {
		t.Fatalf("append1: seq=%d err=%v", seq1, err)
	}
	seq2, err := store.AppendMessage(ctx, created.ID, agent.RoleAssistant, agent.AssistantContent{Text: "hello"}, "gpt-x", json.RawMessage(`{"total_tokens":3}`))
	if err != nil || seq2 != 2 {
		t.Fatalf("append2: seq=%d err=%v", seq2, err)
	}
	messages, err := store.ListMessages(ctx, created.ID)
	if err != nil || len(messages) != 2 {
		t.Fatalf("list: %v %d", err, len(messages))
	}
	if messages[1].Role != agent.RoleAssistant || messages[1].Model != "gpt-x" || string(messages[1].Usage) == "" {
		t.Fatalf("assistant message meta lost: %+v", messages[1])
	}

	// 截断
	if err := store.TruncateMessages(ctx, created.ID, 1); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	messages, _ = store.ListMessages(ctx, created.ID)
	if len(messages) != 1 || messages[0].Seq != 1 {
		t.Fatalf("after truncate: %+v", messages)
	}

	// 引擎状态更新：draft + pending
	pending := &agent.PendingAction{
		Calls:  []relay.MaheshvaraToolCall{{ID: "c1", Type: "function", Name: "test_upstream"}},
		Reason: "需要真实测试",
	}
	waiting := agent.StatusWaitingApproval
	if err := store.UpdateSessionState(ctx, created.ID, agent.SessionStateUpdate{Status: &waiting, PendingAction: pending, DraftConfig: json.RawMessage(`{"id":"vendor-x","request":{"path":"/v2"}}`)}); err != nil {
		t.Fatalf("update state: %v", err)
	}
	got, err := store.GetSession(ctx, created.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status != agent.StatusWaitingApproval || got.PendingAction == nil || len(got.PendingAction.Calls) != 1 {
		t.Fatalf("state roundtrip: %+v", got)
	}
	if !strings.Contains(string(got.DraftConfig), "/v2") {
		t.Fatalf("draft not updated: %s", got.DraftConfig)
	}

	// 清除 pending
	if err := store.UpdateSessionState(ctx, created.ID, agent.SessionStateUpdate{ClearPending: true}); err != nil {
		t.Fatalf("clear pending: %v", err)
	}
	got, _ = store.GetSession(ctx, created.ID)
	if got.PendingAction != nil {
		t.Fatalf("pending not cleared")
	}

	// 列表
	sessions, err := store.ListAgentSessions(ctx)
	if err != nil || len(sessions) != 1 {
		t.Fatalf("list sessions: %v %d", err, len(sessions))
	}
	if sessions[0].Settings.TestAPIKeySet {
		t.Fatalf("no key configured; must not report set")
	}
}

func TestAgentSessionAPIKeyEncryption(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	masterKey := []byte("unit-test-master-key")
	store, err := OpenWithKey(dir+"/agent-key.sqlite3", masterKey)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = store.Close() }()

	created, err := store.CreateAgentSession(ctx, AgentSessionUpsert{Mode: agent.ModeCreate})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	key := "sk-secret-1"
	baseURL := "https://up.example"
	if _, err := store.UpdateAgentSessionSettings(ctx, created.ID, nil,
		&agent.SettingsPatch{TestBaseURL: &baseURL}, &key, false); err != nil {
		t.Fatalf("update settings: %v", err)
	}
	got, err := store.GetSession(ctx, created.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.TestAPIKey != key {
		t.Fatalf("api key not decrypted: %q", got.TestAPIKey)
	}
	if !got.Settings.TestAPIKeySet {
		t.Fatalf("TestAPIKeySet flag lost")
	}
	if got.TestBaseURL != "https://up.example" {
		t.Fatalf("base url lost")
	}

	// 落库必须是密文
	var stored string
	if err := store.db.QueryRowContext(ctx, `SELECT test_api_key FROM agent_sessions WHERE id = ?`, created.ID).Scan(&stored); err != nil {
		t.Fatalf("raw select: %v", err)
	}
	if !strings.HasPrefix(stored, "enc:v1:") {
		t.Fatalf("api key stored in plaintext: %q", stored)
	}

	// 引擎路径经 UpdateSessionState 写入的 key 同样加密
	if err := store.UpdateSessionState(ctx, created.ID, agent.SessionStateUpdate{TestAPIKey: "sk-approval-2"}); err != nil {
		t.Fatalf("state update: %v", err)
	}
	if err := store.db.QueryRowContext(ctx, `SELECT test_api_key FROM agent_sessions WHERE id = ?`, created.ID).Scan(&stored); err != nil || !strings.HasPrefix(stored, "enc:v1:") {
		t.Fatalf("engine path key not encrypted: %q %v", stored, err)
	}
	got, _ = store.GetSession(ctx, created.ID)
	if got.TestAPIKey != "sk-approval-2" {
		t.Fatalf("engine path key not decrypted: %q", got.TestAPIKey)
	}

	// 删除
	deleted, err := store.DeleteAgentSession(ctx, created.ID)
	if err != nil || !deleted {
		t.Fatalf("delete: %v %v", deleted, err)
	}
	messages, err := store.ListMessages(ctx, created.ID)
	if err != nil || len(messages) != 0 {
		t.Fatalf("messages should be deleted with session: %v", err)
	}
}

func TestAgentSessionThinkingRoundtrip(t *testing.T) {
	ctx := context.Background()
	store := newAgentTestStore(t)
	created, _ := store.CreateAgentSession(ctx, AgentSessionUpsert{Mode: agent.ModeCreate})
	sourceID, modelName, effort, allowSave := "src1", "thinker", "high", "never"
	thinking := true
	updated, err := store.UpdateAgentSessionSettings(ctx, created.ID, nil, &agent.SettingsPatch{
		ModelSourceID: &sourceID, ModelName: &modelName, ThinkingEnabled: &thinking,
		ThinkingEffort: &effort, AllowSave: &allowSave,
	}, nil, false)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if !updated.Settings.ThinkingEnabled || updated.Settings.ThinkingEffort != "high" || updated.Settings.AllowSave != "never" || updated.Settings.AllowLiveTest != "ask" {
		t.Fatalf("settings roundtrip: %+v", updated.Settings)
	}
}

func TestAgentSessionDraftRestoreRoundtrip(t *testing.T) {
	ctx := context.Background()
	store := newAgentTestStore(t)
	created, _ := store.CreateAgentSession(ctx, AgentSessionUpsert{Mode: agent.ModeCreate})
	if len(created.DraftRestore) != 0 {
		t.Fatalf("restore point must start empty")
	}
	if err := store.UpdateSessionState(ctx, created.ID, agent.SessionStateUpdate{DraftConfig: []byte(`{"a":1}`)}); err != nil {
		t.Fatalf("set draft: %v", err)
	}
	snapshot := []byte(`{"a":0}`)
	if err := store.UpdateSessionState(ctx, created.ID, agent.SessionStateUpdate{DraftRestore: snapshot}); err != nil {
		t.Fatalf("set restore point: %v", err)
	}
	got, err := store.GetSession(ctx, created.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if string(got.DraftRestore) != string(snapshot) || string(got.DraftConfig) != `{"a":1}` {
		t.Fatalf("restore=%s draft=%s", got.DraftRestore, got.DraftConfig)
	}
	// nil 增量不覆盖还原点
	if err := store.UpdateSessionState(ctx, created.ID, agent.SessionStateUpdate{Title: "t"}); err != nil {
		t.Fatalf("touch title: %v", err)
	}
	got2, _ := store.GetSession(ctx, created.ID)
	if string(got2.DraftRestore) != string(snapshot) {
		t.Fatalf("restore point overwritten by nil update: %s", got2.DraftRestore)
	}
}

func TestAgentSessionPlanModeRoundtrip(t *testing.T) {
	ctx := context.Background()
	store := newAgentTestStore(t)
	created, _ := store.CreateAgentSession(ctx, AgentSessionUpsert{Mode: agent.ModeCreate})
	if created.Settings.PlanMode {
		t.Fatalf("plan mode must default off")
	}
	on := true
	updated, err := store.UpdateAgentSessionSettings(ctx, created.ID, nil, &agent.SettingsPatch{PlanMode: &on}, nil, false)
	if err != nil {
		t.Fatalf("enable plan mode: %v", err)
	}
	if !updated.Settings.PlanMode {
		t.Fatalf("plan mode not persisted: %+v", updated.Settings)
	}
	// 关闭不带走其它设置
	off := false
	updated, err = store.UpdateAgentSessionSettings(ctx, created.ID, nil, &agent.SettingsPatch{PlanMode: &off}, nil, false)
	if err != nil {
		t.Fatalf("disable plan mode: %v", err)
	}
	if updated.Settings.PlanMode {
		t.Fatalf("plan mode not cleared: %+v", updated.Settings)
	}
}

// 回归：部分更新（只改思考等级）不得清空思考开关与模型选择——
// 旧实现整组覆盖设置列导致「选等级后思考被关闭」。
func TestAgentSessionPartialSettingsPatchKeepsUnmentionedFields(t *testing.T) {
	ctx := context.Background()
	store := newAgentTestStore(t)
	created, _ := store.CreateAgentSession(ctx, AgentSessionUpsert{Mode: agent.ModeCreate})
	sourceID, modelName, effort := "src1", "m1", "high"
	thinking := true
	if _, err := store.UpdateAgentSessionSettings(ctx, created.ID, nil, &agent.SettingsPatch{
		ModelSourceID: &sourceID, ModelName: &modelName,
		ThinkingEnabled: &thinking, ThinkingEffort: &effort,
	}, nil, false); err != nil {
		t.Fatalf("seed settings: %v", err)
	}

	// 只改等级
	newEffort := "max"
	updated, err := store.UpdateAgentSessionSettings(ctx, created.ID, nil, &agent.SettingsPatch{
		ThinkingEffort: &newEffort,
	}, nil, false)
	if err != nil {
		t.Fatalf("patch effort: %v", err)
	}
	if !updated.Settings.ThinkingEnabled || updated.Settings.ThinkingEffort != "max" {
		t.Fatalf("thinking lost: %+v", updated.Settings)
	}
	if updated.Settings.ModelSourceID != "src1" || updated.Settings.ModelName != "m1" {
		t.Fatalf("model selection wiped: %+v", updated.Settings)
	}

	// 只关思考，等级保留
	off := false
	updated, err = store.UpdateAgentSessionSettings(ctx, created.ID, nil, &agent.SettingsPatch{
		ThinkingEnabled: &off,
	}, nil, false)
	if err != nil {
		t.Fatalf("patch off: %v", err)
	}
	if updated.Settings.ThinkingEnabled || updated.Settings.ThinkingEffort != "max" {
		t.Fatalf("effort lost: %+v", updated.Settings)
	}
}

// 回归（W2-12）：启动对账只复位 running；waiting_approval（待批动作仍可恢复）保留。
func TestResetRunningSessionsReconcilesCrashLeftovers(t *testing.T) {
	ctx := context.Background()
	store := newAgentTestStore(t)

	running, err := store.CreateAgentSession(ctx, AgentSessionUpsert{Title: "崩溃轮次", Mode: agent.ModeCreate})
	if err != nil {
		t.Fatalf("create running: %v", err)
	}
	waiting, err := store.CreateAgentSession(ctx, AgentSessionUpsert{Title: "待审批", Mode: agent.ModeCreate})
	if err != nil {
		t.Fatalf("create waiting: %v", err)
	}
	runStatus := agent.StatusRunning
	if err := store.UpdateSessionState(ctx, running.ID, agent.SessionStateUpdate{Status: &runStatus}); err != nil {
		t.Fatalf("set running: %v", err)
	}
	waitStatus := agent.StatusWaitingApproval
	pending := &agent.PendingAction{Calls: []relay.MaheshvaraToolCall{{ID: "c1", Type: "function", Name: "save_protocol"}}}
	if err := store.UpdateSessionState(ctx, waiting.ID, agent.SessionStateUpdate{Status: &waitStatus, PendingAction: pending}); err != nil {
		t.Fatalf("set waiting: %v", err)
	}

	if err := store.ResetRunningSessions(ctx); err != nil {
		t.Fatalf("ResetRunningSessions: %v", err)
	}
	afterRun, _ := store.GetAgentSession(ctx, running.ID)
	if afterRun.Status != agent.StatusIdle {
		t.Fatalf("running session should reset to idle, got %q", afterRun.Status)
	}
	afterWait, _ := store.GetAgentSession(ctx, waiting.ID)
	if afterWait.Status != agent.StatusWaitingApproval || afterWait.PendingAction == nil {
		t.Fatalf("waiting_approval must be preserved: %+v", afterWait)
	}
}

// 回归：plan 型待批动作没有 Calls，读回时不能被静默丢弃（否则确认永远 409）。
func TestAgentSessionPendingActionWithoutCallsRoundtrip(t *testing.T) {
	ctx := context.Background()
	store := newAgentTestStore(t)
	created, _ := store.CreateAgentSession(ctx, AgentSessionUpsert{Mode: agent.ModeCreate})

	waiting := agent.StatusWaitingApproval
	pending := &agent.PendingAction{Kind: "plan", Plan: []agent.PlanStep{{Title: "建源", Status: "pending"}}}
	if err := store.UpdateSessionState(ctx, created.ID, agent.SessionStateUpdate{Status: &waiting, PendingAction: pending}); err != nil {
		t.Fatalf("update state: %v", err)
	}
	session, err := store.GetSession(ctx, created.ID)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if session.PendingAction == nil || session.PendingAction.Kind != "plan" || len(session.PendingAction.Plan) != 1 {
		t.Fatalf("plan pending lost on read-back: %+v", session.PendingAction)
	}
}

// 回归：非 assistant 消息的 usage_json 落库为空串，json_extract(”) 会抛
// malformed JSON 中止聚合——统计必须短路空串且正确计数。
func TestAgentSessionStatsAggregation(t *testing.T) {
	ctx := context.Background()
	store := newAgentTestStore(t)
	created, _ := store.CreateAgentSession(ctx, AgentSessionUpsert{Mode: agent.ModeCreate})
	other, _ := store.CreateAgentSession(ctx, AgentSessionUpsert{Mode: agent.ModeCreate})

	append := func(sessionID, role string, usage string) {
		content := map[string]any{"text": "x"}
		if role == agent.RoleToolResult {
			content = map[string]any{"callId": "c", "name": "t", "ok": true}
		}
		encoded, _ := json.Marshal(content)
		var usageJSON json.RawMessage
		if usage != "" {
			usageJSON = json.RawMessage(usage)
		}
		if _, err := store.AppendMessage(ctx, sessionID, role, json.RawMessage(encoded), "", usageJSON); err != nil {
			t.Fatalf("append %s/%s: %v", sessionID, role, err)
		}
	}
	append(created.ID, agent.RoleUser, "")                          // 轮 1
	append(created.ID, agent.RoleAssistant, `{"total_tokens":120}`) // 用量
	append(created.ID, agent.RoleToolResult, "")                    // 空串 usage
	append(created.ID, agent.RoleSystem, "")                        // 空串 usage
	append(created.ID, agent.RoleUser, "")                          // 轮 2
	append(created.ID, agent.RoleAssistant, `{"total_tokens":30,"input_tokens":20,"output_tokens":10}`)
	append(created.ID, agent.RoleAssistant, "") // assistant 无 usage（空串）
	append(other.ID, agent.RoleUser, "")

	sessions, err := store.ListAgentSessions(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	byID := map[string]agent.Session{}
	for _, item := range sessions {
		byID[item.ID] = item
	}
	if got := byID[created.ID].UserTurns; got != 2 {
		t.Fatalf("userTurns = %d, want 2", got)
	}
	if got := byID[created.ID].TotalTokens; got != 150 {
		t.Fatalf("totalTokens = %d, want 150", got)
	}
	if got := byID[other.ID].UserTurns; got != 1 {
		t.Fatalf("other userTurns = %d, want 1", got)
	}
	if got := byID[other.ID].TotalTokens; got != 0 {
		t.Fatalf("other totalTokens = %d, want 0", got)
	}
}

// 标题写入统一钳制 64 字：PATCH 与引擎路径都不例外。
func TestAgentSessionTitleClamp(t *testing.T) {
	ctx := context.Background()
	store := newAgentTestStore(t)
	created, _ := store.CreateAgentSession(ctx, AgentSessionUpsert{Mode: agent.ModeCreate})

	long := strings.Repeat("标", 80)
	updated, err := store.UpdateAgentSessionSettings(ctx, created.ID, &long, nil, nil, false)
	if err != nil {
		t.Fatalf("patch title: %v", err)
	}
	if got := []rune(updated.Title); len(got) != 64 {
		t.Fatalf("patched title runes = %d, want 64", len(got))
	}

	engineTitle := strings.Repeat("题", 100)
	if err := store.UpdateSessionState(ctx, created.ID, agent.SessionStateUpdate{Title: engineTitle}); err != nil {
		t.Fatalf("engine title: %v", err)
	}
	session, err := store.GetAgentSession(ctx, created.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got := []rune(session.Title); len(got) != 64 {
		t.Fatalf("engine title runes = %d, want 64", len(got))
	}
}
