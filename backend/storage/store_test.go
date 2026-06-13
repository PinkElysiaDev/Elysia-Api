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
	if len(groups) != 1 || len(groups[0].Models) != 1 || groups[0].Models[0] != "model-a" {
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
