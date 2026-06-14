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
