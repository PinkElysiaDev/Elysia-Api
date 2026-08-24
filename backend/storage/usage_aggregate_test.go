package storage

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestUsageDailyUsesFixedUTCOffset(t *testing.T) {
	ctx := context.Background()
	store, err := Open(filepath.Join(t.TempDir(), "usage-daily.sqlite3"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer store.Close()

	records := []UsageLogItem{
		{RequestID: "before-east-boundary", StartedAt: time.Date(2026, 8, 1, 15, 30, 0, 0, time.UTC), ModelName: "model-a", StatusCode: 200, TotalTokens: 10},
		{RequestID: "after-east-boundary", StartedAt: time.Date(2026, 8, 1, 16, 30, 0, 0, time.UTC), ModelName: "model-b", StatusCode: 500, TotalTokens: 20},
		{RequestID: "same-east-day", StartedAt: time.Date(2026, 8, 1, 17, 0, 0, 0, time.UTC), ModelName: "model-c", StatusCode: 200, TotalTokens: 5},
	}
	for _, item := range records {
		saveUsageAggregateFixture(t, store, item)
	}

	east, err := store.UsageDaily(ctx, UsageQuery{}, 8*60)
	if err != nil {
		t.Fatalf("UsageDaily(+08:00) error = %v", err)
	}
	if len(east) != 2 || east[0] != (UsageDailyBucket{Date: "2026-08-01", Requests: 1, Tokens: 10}) || east[1] != (UsageDailyBucket{Date: "2026-08-02", Requests: 2, Tokens: 25}) {
		t.Fatalf("UsageDaily(+08:00) = %#v", east)
	}

	west, err := store.UsageDaily(ctx, UsageQuery{}, -7*60)
	if err != nil {
		t.Fatalf("UsageDaily(-07:00) error = %v", err)
	}
	if len(west) != 1 || west[0] != (UsageDailyBucket{Date: "2026-08-01", Requests: 3, Tokens: 35}) {
		t.Fatalf("UsageDaily(-07:00) = %#v", west)
	}

	empty, err := store.UsageDaily(ctx, UsageQuery{ModelName: "missing"}, 0)
	if err != nil {
		t.Fatalf("UsageDaily(empty) error = %v", err)
	}
	if empty == nil || len(empty) != 0 {
		t.Fatalf("UsageDaily(empty) = %#v, want non-nil empty slice", empty)
	}
}

func TestUsageByModelAndStatusFilters(t *testing.T) {
	ctx := context.Background()
	store, err := Open(filepath.Join(t.TempDir(), "usage-by-model.sqlite3"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer store.Close()

	base := time.Date(2026, 8, 2, 0, 0, 0, 0, time.UTC)
	records := []UsageLogItem{
		{RequestID: "a-success", StartedAt: base.Add(time.Minute), ModelName: "model-a", StatusCode: 200, TotalTokens: 10},
		{RequestID: "a-http-failure", StartedAt: base.Add(2 * time.Minute), ModelName: "model-a", StatusCode: 500, TotalTokens: 20},
		{RequestID: "a-no-status", StartedAt: base.Add(3 * time.Minute), ModelName: "model-a", StatusCode: 0, TotalTokens: 30},
		{RequestID: "empty-model", StartedAt: base.Add(4 * time.Minute), ModelName: "", StatusCode: 200, TotalTokens: 5},
		{RequestID: "b-success", StartedAt: base.Add(5 * time.Minute), ModelName: "model-b", StatusCode: 204, TotalTokens: 40},
		{RequestID: "c-failure", StartedAt: base.Add(6 * time.Minute), ModelName: "model-c", StatusCode: 429, TotalTokens: 50},
	}
	for _, item := range records {
		saveUsageAggregateFixture(t, store, item)
	}

	byModel, err := store.UsageByModel(ctx, UsageQuery{})
	if err != nil {
		t.Fatalf("UsageByModel() error = %v", err)
	}
	want := []UsageModelBucket{
		{Model: "model-a", Requests: 3, Failed: 2, Tokens: 60},
		{Model: "", Requests: 1, Failed: 0, Tokens: 5},
		{Model: "model-b", Requests: 1, Failed: 0, Tokens: 40},
		{Model: "model-c", Requests: 1, Failed: 1, Tokens: 50},
	}
	if len(byModel) != len(want) {
		t.Fatalf("UsageByModel() length = %d, want %d: %#v", len(byModel), len(want), byModel)
	}
	for i := range want {
		if byModel[i] != want[i] {
			t.Fatalf("UsageByModel()[%d] = %#v, want %#v", i, byModel[i], want[i])
		}
	}

	failedTotal, failedLogs, err := store.QueryUsageLogs(ctx, UsageQuery{Status: "failed", Limit: 20})
	if err != nil {
		t.Fatalf("QueryUsageLogs(failed) error = %v", err)
	}
	if failedTotal != 3 || len(failedLogs) != 3 {
		t.Fatalf("QueryUsageLogs(failed) total=%d logs=%#v", failedTotal, failedLogs)
	}
	for _, item := range failedLogs {
		if item.StatusCode >= 200 && item.StatusCode < 400 {
			t.Fatalf("success status %d leaked into failed filter", item.StatusCode)
		}
	}

	exactTotal, exactLogs, err := store.QueryUsageLogs(ctx, UsageQuery{Status: "failed", StatusCode: 500, Limit: 20})
	if err != nil {
		t.Fatalf("QueryUsageLogs(exact) error = %v", err)
	}
	if exactTotal != 1 || len(exactLogs) != 1 || exactLogs[0].StatusCode != 500 {
		t.Fatalf("exact status must take precedence, total=%d logs=%#v", exactTotal, exactLogs)
	}

	empty, err := store.UsageByModel(ctx, UsageQuery{ModelName: "missing"})
	if err != nil {
		t.Fatalf("UsageByModel(empty) error = %v", err)
	}
	if empty == nil || len(empty) != 0 {
		t.Fatalf("UsageByModel(empty) = %#v, want non-nil empty slice", empty)
	}
}

func TestUsageQueryHalfOpenToBoundary(t *testing.T) {
	ctx := context.Background()
	store, err := Open(filepath.Join(t.TempDir(), "usage-half-open.sqlite3"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer store.Close()

	t0 := time.Date(2026, 8, 2, 0, 0, 0, 0, time.UTC)
	t1 := t0.Add(time.Minute)
	records := []UsageLogItem{
		{RequestID: "at-from", StartedAt: t0, StatusCode: 200, TotalTokens: 1},
		{RequestID: "inside", StartedAt: t0.Add(30 * time.Second), StatusCode: 200, TotalTokens: 2},
		{RequestID: "at-to", StartedAt: t1, StatusCode: 200, TotalTokens: 4},
	}
	for _, item := range records {
		saveUsageAggregateFixture(t, store, item)
	}

	first, err := store.UsageTotals(ctx, UsageQuery{From: t0, To: t1})
	if err != nil {
		t.Fatalf("UsageTotals([t0,t1)) error = %v", err)
	}
	if first["requests"] != 2 || first["totalTokens"] != 3 {
		t.Fatalf("UsageTotals([t0,t1)) = %#v, want requests=2 totalTokens=3", first)
	}

	second, err := store.UsageTotals(ctx, UsageQuery{From: t1, To: t1.Add(time.Minute)})
	if err != nil {
		t.Fatalf("UsageTotals([t1,t2)) error = %v", err)
	}
	if second["requests"] != 1 || second["totalTokens"] != 4 {
		t.Fatalf("UsageTotals([t1,t2)) = %#v, want requests=1 totalTokens=4", second)
	}
}

func saveUsageAggregateFixture(t *testing.T, store *Store, item UsageLogItem) {
	t.Helper()
	if err := store.SaveUsageRecordJSON(context.Background(), []byte(`{"requestId":"`+item.RequestID+`"}`), item, item.StartedAt.Add(time.Second)); err != nil {
		t.Fatalf("SaveUsageRecordJSON(%s) error = %v", item.RequestID, err)
	}
}
