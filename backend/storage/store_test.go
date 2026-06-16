package storage

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestOpenEnablesWALAndMigrates(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "elysia.sqlite3"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer store.Close()

	var journalMode string
	if err := store.db.QueryRow("PRAGMA journal_mode").Scan(&journalMode); err != nil {
		t.Fatalf("journal_mode query error = %v", err)
	}
	if journalMode != "wal" {
		t.Fatalf("journal_mode = %q, want wal", journalMode)
	}

	var busyTimeout int
	if err := store.db.QueryRow("PRAGMA busy_timeout").Scan(&busyTimeout); err != nil {
		t.Fatalf("busy_timeout query error = %v", err)
	}
	if busyTimeout != 5000 {
		t.Fatalf("busy_timeout = %d, want 5000", busyTimeout)
	}

	if err := store.migrate(context.Background()); err != nil {
		t.Fatalf("second migrate error = %v", err)
	}
}

func TestSourceModelsGroupsTokensAndUsage(t *testing.T) {
	ctx := context.Background()
	store, err := Open(filepath.Join(t.TempDir(), "elysia.sqlite3"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer store.Close()

	source := ModelSource{ID: "src", Name: "Source", BaseURL: "https://example.test/v1", APIKey: "secret", Platform: "openai-compatible", Enabled: true, AutoFetchModels: false}
	if err := store.UpsertSource(ctx, source); err != nil {
		t.Fatalf("UpsertSource() error = %v", err)
	}
	if err := store.ReplaceSourceModels(ctx, source, []Model{{ID: "model-a", Name: "Model A", Type: "llm", Available: true}}); err != nil {
		t.Fatalf("ReplaceSourceModels() error = %v", err)
	}
	models, err := store.ListModels(ctx)
	if err != nil {
		t.Fatalf("ListModels() error = %v", err)
	}
	if len(models) != 1 || models[0].Platform != "openai" || models[0].APIKey != "secret" {
		t.Fatalf("models = %#v", models)
	}

	group := ModelGroup{ID: "group", Name: "Group", Enabled: true, Models: []string{"model-a"}, Strategy: "round-robin", Type: "llm"}
	if err := store.UpsertGroup(ctx, group); err != nil {
		t.Fatalf("UpsertGroup() error = %v", err)
	}
	groups, err := store.ListGroups(ctx)
	if err != nil {
		t.Fatalf("ListGroups() error = %v", err)
	}
	if len(groups) != 1 || len(groups[0].Models) != 1 || groups[0].Models[0] != "src:model-a" {
		t.Fatalf("groups = %#v", groups)
	}

	if err := store.UpsertAPIToken(ctx, APIToken{Name: "default", Token: "tok", Enabled: true}); err != nil {
		t.Fatalf("UpsertAPIToken() error = %v", err)
	}
	if token, ok, err := store.FindAPIToken(ctx, "tok"); err != nil || !ok || token.Name != "default" {
		t.Fatalf("FindAPIToken() = %#v, %v, %v", token, ok, err)
	}

	started := time.Now().UTC().Add(-time.Minute)
	usage := UsageLogItem{RequestID: "req", StartedAt: started, KeyName: "default", GroupName: "Group", ModelName: "Model A", StatusCode: 200, InputTokens: 1, OutputTokens: 2, TotalTokens: 3}
	if err := store.SaveUsageRecordJSON(ctx, []byte(`{"requestId":"req"}`), usage, time.Now().UTC()); err != nil {
		t.Fatalf("SaveUsageRecordJSON() error = %v", err)
	}
	total, logs, err := store.QueryUsageLogs(ctx, UsageQuery{From: started.Add(-time.Second), To: time.Now().UTC(), Limit: 10})
	if err != nil {
		t.Fatalf("QueryUsageLogs() error = %v", err)
	}
	if total != 1 || len(logs) != 1 || logs[0].TotalTokens != 3 {
		t.Fatalf("total=%d logs=%#v", total, logs)
	}
	summary, err := store.UsageTotals(ctx, UsageQuery{})
	if err != nil {
		t.Fatalf("UsageTotals() error = %v", err)
	}
	if summary["totalTokens"].(int) != 3 {
		t.Fatalf("summary = %#v", summary)
	}
}

// 回归 #10：空库时 UsageTotals 不应因 SUM(CASE...) 返回 NULL 而 Scan 失败。
func TestUsageTotalsEmptyDB(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "empty.sqlite3"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()
	summary, err := store.UsageTotals(context.Background(), UsageQuery{})
	if err != nil {
		t.Fatalf("UsageTotals on empty db should not error, got: %v", err)
	}
	if summary["requests"].(int) != 0 || summary["success"].(int) != 0 || summary["failed"].(int) != 0 {
		t.Fatalf("empty db totals should be zero, got: %#v", summary)
	}
}

// 回归 #3：两个不同源的同名模型，模型组用复合键 sourceId:modelId 区分，
// UpsertGroup/ListGroups 应精确保留各自身份，不互相覆盖。
func TestModelGroupCompositeKeyDistinctSources(t *testing.T) {
	ctx := context.Background()
	store, err := Open(filepath.Join(t.TempDir(), "composite.sqlite3"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()

	// 两个源，各有一个同名模型 claude-3-5-sonnet。
	srcA := ModelSource{ID: "src-a", Name: "A", BaseURL: "https://a.example.com", Platform: "openai", Enabled: true}
	srcB := ModelSource{ID: "src-b", Name: "B", BaseURL: "https://b.example.com", Platform: "openai", Enabled: true}
	for _, src := range []ModelSource{srcA, srcB} {
		if err := store.UpsertSource(ctx, src); err != nil {
			t.Fatalf("UpsertSource %s: %v", src.ID, err)
		}
		if err := store.ReplaceSourceModels(ctx, src, []Model{{ID: "claude-3-5-sonnet", Name: "Sonnet", Type: "llm", Available: true}}); err != nil {
			t.Fatalf("ReplaceSourceModels %s: %v", src.ID, err)
		}
	}

	// 组里用复合键选两个同名模型。
	group := ModelGroup{ID: "g1", Name: "grp", Enabled: true, Strategy: "round-robin",
		Models: []string{"src-a:claude-3-5-sonnet", "src-b:claude-3-5-sonnet"}}
	if err := store.UpsertGroup(ctx, group); err != nil {
		t.Fatalf("UpsertGroup: %v", err)
	}

	groups, err := store.ListGroups(ctx)
	if err != nil {
		t.Fatalf("ListGroups: %v", err)
	}
	var got []string
	for _, g := range groups {
		if g.ID == "g1" {
			got = g.Models
		}
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 composite models, got %v", got)
	}
	seen := map[string]bool{}
	for _, m := range got {
		seen[m] = true
	}
	if !seen["src-a:claude-3-5-sonnet"] || !seen["src-b:claude-3-5-sonnet"] {
		t.Fatalf("composite keys not preserved: %v", got)
	}
}

// 回归：模型组改名后，引用它的 token 的 allowed_groups_json 应级联更新为新名，
// 不再残留旧名（旧名残留会成为无法去掉的悬空引用）。
func TestUpsertGroupRenameCascadesToTokens(t *testing.T) {
	ctx := context.Background()
	store, err := Open(filepath.Join(t.TempDir(), "rename-cascade.sqlite3"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()

	if err := store.UpsertGroup(ctx, ModelGroup{ID: "g1", Name: "旧组名", Enabled: true, Strategy: "round-robin", Type: "llm"}); err != nil {
		t.Fatalf("UpsertGroup create: %v", err)
	}
	if err := store.UpsertGroup(ctx, ModelGroup{ID: "g2", Name: "其它组", Enabled: true, Strategy: "round-robin", Type: "llm"}); err != nil {
		t.Fatalf("UpsertGroup create g2: %v", err)
	}
	// token 引用旧组名 + 一个无关组名。
	if err := store.UpsertAPIToken(ctx, APIToken{Name: "k1", Token: "sk-a", Enabled: true, AllowedGroups: []string{"旧组名", "其它组"}}); err != nil {
		t.Fatalf("UpsertAPIToken: %v", err)
	}
	// 不引用该组的 token 不应受影响。
	if err := store.UpsertAPIToken(ctx, APIToken{Name: "k2", Token: "sk-b", Enabled: true, AllowedGroups: []string{"其它组"}}); err != nil {
		t.Fatalf("UpsertAPIToken k2: %v", err)
	}

	// 改名（同 id，新 name）。
	if err := store.UpsertGroup(ctx, ModelGroup{ID: "g1", Name: "新组名", Enabled: true, Strategy: "round-robin", Type: "llm"}); err != nil {
		t.Fatalf("UpsertGroup rename: %v", err)
	}

	tokens, err := store.ListAPITokens(ctx)
	if err != nil {
		t.Fatalf("ListAPITokens: %v", err)
	}
	byName := map[string][]string{}
	for _, tk := range tokens {
		byName[tk.Name] = tk.AllowedGroups
	}
	if got := byName["k1"]; len(got) != 2 || got[0] != "新组名" || got[1] != "其它组" {
		t.Fatalf("k1 allowedGroups after rename = %v, want [新组名 其它组]", got)
	}
	if got := byName["k2"]; len(got) != 1 || got[0] != "其它组" {
		t.Fatalf("k2 allowedGroups should be untouched, got %v", got)
	}
}

// 改名级联在 token 内部去重：replaceGroupName 把旧名替换为新名时，
// 若新名已存在于该 token 列表，应去重避免重复 Badge。
func TestUpsertGroupRenameDedupesWithinToken(t *testing.T) {
	if _, changed := replaceGroupName([]string{"a", "b", "a"}, "x", "y"); changed {
		t.Fatalf("no oldName present but reported changed")
	}
	out, changed := replaceGroupName([]string{"old", "keep", "new"}, "old", "new")
	if !changed {
		t.Fatalf("expected changed when oldName replaced")
	}
	if len(out) != 2 || out[0] != "new" || out[1] != "keep" {
		t.Fatalf("dedupe result = %v, want [new keep]", out)
	}
}

// 向后兼容：裸 id（无 source_id）的旧组数据，ListGroups 仍返回裸 id。
func TestModelGroupLegacyBareIDCompat(t *testing.T) {
	ctx := context.Background()
	store, err := Open(filepath.Join(t.TempDir(), "legacy-group.sqlite3"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()
	src := ModelSource{ID: "s1", Name: "S1", BaseURL: "https://s.example.com", Platform: "openai", Enabled: true}
	if err := store.UpsertSource(ctx, src); err != nil {
		t.Fatalf("UpsertSource: %v", err)
	}
	if err := store.ReplaceSourceModels(ctx, src, []Model{{ID: "m1", Name: "M1", Type: "llm", Available: true}}); err != nil {
		t.Fatalf("ReplaceSourceModels: %v", err)
	}
	// 用裸 id 建组（模拟旧数据路径）。
	if err := store.UpsertGroup(ctx, ModelGroup{ID: "g1", Name: "grp", Enabled: true, Strategy: "round-robin", Models: []string{"m1"}}); err != nil {
		t.Fatalf("UpsertGroup: %v", err)
	}
	groups, err := store.ListGroups(ctx)
	if err != nil {
		t.Fatalf("ListGroups: %v", err)
	}
	// findModel 能解析出 source_id，所以会返回复合键；这验证裸 id 输入被正确解析。
	for _, g := range groups {
		if g.ID == "g1" {
			if len(g.Models) != 1 {
				t.Fatalf("expected 1 model, got %v", g.Models)
			}
			if g.Models[0] != "s1:m1" {
				t.Fatalf("bare id should resolve to composite s1:m1, got %q", g.Models[0])
			}
		}
	}
}

// 回归 #1：同一 token 值不允许配置到两个不同 name 上。
func TestUpsertAPITokenRejectsDuplicateValue(t *testing.T) {
	ctx := context.Background()
	store, err := Open(filepath.Join(t.TempDir(), "dup.sqlite3"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()

	if err := store.UpsertAPIToken(ctx, APIToken{Name: "k1", Token: "sk-same-value", Enabled: true}); err != nil {
		t.Fatalf("first upsert should succeed: %v", err)
	}
	// 不同 name 用同一 token 值 → 应被拒。
	if err := store.UpsertAPIToken(ctx, APIToken{Name: "k2", Token: "sk-same-value", Enabled: true}); err == nil {
		t.Fatalf("duplicate token value across names should be rejected")
	}
	// 同名更新自身（token 不变）→ 应允许。
	if err := store.UpsertAPIToken(ctx, APIToken{Name: "k1", Token: "sk-same-value", Enabled: false}); err != nil {
		t.Fatalf("updating same token's own record should succeed: %v", err)
	}
	// 不同 name 用不同 token 值 → 应允许。
	if err := store.UpsertAPIToken(ctx, APIToken{Name: "k3", Token: "sk-other-value", Enabled: true}); err != nil {
		t.Fatalf("distinct token value should succeed: %v", err)
	}
}

// 回归：token_hash 回填迁移在单连接池上不得死锁（曾因 rows 未关时做 Exec 死锁，
// 导致后端启动崩溃 "all goroutines are asleep - deadlock!"）。
// 覆盖"已有 token 数据需回填"的真实崩溃场景：建库写 token → 关闭 →
// 手动清空 token_hash 模拟旧数据 → 重新 Open 触发回填，应在超时内完成。
func TestTokenHashBackfillNoDeadlock(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "backfill.sqlite3")

	store, err := OpenWithKey(dbPath, []byte("master-key"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := store.UpsertAPIToken(ctx, APIToken{Name: "k1", Token: "sk-a", Enabled: true}); err != nil {
		t.Fatalf("upsert k1: %v", err)
	}
	if err := store.UpsertAPIToken(ctx, APIToken{Name: "k2", Token: "sk-b", Enabled: true}); err != nil {
		t.Fatalf("upsert k2: %v", err)
	}
	// 模拟旧数据：清空 token_hash，使重新 Open 时触发回填迁移。
	if _, err := store.db.ExecContext(ctx, `UPDATE api_tokens SET token_hash = ''`); err != nil {
		t.Fatalf("reset hash: %v", err)
	}
	store.Close()

	// 重新打开应在超时内完成（回填不死锁）。
	done := make(chan error, 1)
	go func() {
		s2, err := OpenWithKey(dbPath, []byte("master-key"))
		if s2 != nil {
			s2.Close()
		}
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("reopen with backfill failed: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("token_hash backfill deadlocked on reopen")
	}
}
