package storage

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
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
	if len(east) != 2 || east[0].Date != "2026-08-01" || east[0].Requests != 1 || east[0].Tokens != 10 || east[1].Date != "2026-08-02" || east[1].Requests != 2 || east[1].Tokens != 25 {
		t.Fatalf("UsageDaily(+08:00) = %#v", east)
	}
	if east[0].ModelTokens["model-a"] != 10 || east[1].ModelTokens["model-b"] != 20 || east[1].ModelTokens["model-c"] != 5 {
		t.Fatalf("UsageDaily(+08:00) modelTokens = %#v, %#v", east[0].ModelTokens, east[1].ModelTokens)
	}

	west, err := store.UsageDaily(ctx, UsageQuery{}, -7*60)
	if err != nil {
		t.Fatalf("UsageDaily(-07:00) error = %v", err)
	}
	if len(west) != 1 || west[0].Date != "2026-08-01" || west[0].Requests != 3 || west[0].Tokens != 35 {
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

func TestUsagePulseBucketsAndP95(t *testing.T) {
	ctx := context.Background()
	store, err := Open(filepath.Join(t.TempDir(), "usage-pulse.sqlite3"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer store.Close()

	t0 := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	records := []UsageLogItem{
		{RequestID: "a", StartedAt: t0, DurationMs: 10, StatusCode: 200},
		{RequestID: "b", StartedAt: t0.Add(10 * time.Second), DurationMs: 20, StatusCode: 200},
		{RequestID: "c", StartedAt: t0.Add(20 * time.Second), DurationMs: 100, StatusCode: 200},
		{RequestID: "d", StartedAt: t0.Add(time.Minute), DurationMs: 40, StatusCode: 200},
	}
	for _, item := range records {
		saveUsageAggregateFixture(t, store, item)
	}

	result, err := store.UsagePulse(ctx, UsageQuery{From: t0, To: t0.Add(2 * time.Minute)}, 0, 1)
	if err != nil {
		t.Fatalf("UsagePulse() error = %v", err)
	}
	points := result.Points
	if len(points) != 2 {
		t.Fatalf("UsagePulse() len = %d, want 2: %#v", len(points), points)
	}
	if points[0].T != t0.UnixMilli() || points[0].Requests != 3 {
		t.Fatalf("first bucket = %#v", points[0])
	}
	if points[0].AvgDurationMs < 43.3 || points[0].AvgDurationMs > 43.4 {
		t.Fatalf("first avg = %v, want ~43.33", points[0].AvgDurationMs)
	}
	if points[0].P95DurationMs != 100 {
		t.Fatalf("first p95 = %v, want 100", points[0].P95DurationMs)
	}
	if points[1].T != t0.Add(time.Minute).UnixMilli() || points[1].Requests != 1 || points[1].AvgDurationMs != 40 {
		t.Fatalf("second bucket = %#v", points[1])
	}
	if result.Window.Requests != 4 || result.Window.P95DurationMs != 100 {
		t.Fatalf("window = %#v, want 4 requests / p95 100", result.Window)
	}

	empty, err := store.UsagePulse(ctx, UsageQuery{From: t0, To: t0.Add(2 * time.Minute), ModelName: "missing"}, 0, 1)
	if err != nil {
		t.Fatalf("UsagePulse(empty) error = %v", err)
	}
	if empty.Points == nil || len(empty.Points) != 0 {
		t.Fatalf("UsagePulse(empty) = %#v, want non-nil empty slice", empty)
	}

	if _, err := store.UsagePulse(ctx, UsageQuery{}, 0, 1); !errors.Is(err, ErrPulseFromRequired) {
		t.Fatalf("UsagePulse(no from) error = %v, want ErrPulseFromRequired", err)
	}
	if _, err := store.UsagePulse(ctx, UsageQuery{From: t0, To: t0.Add(MaxPulseSpan + time.Hour)}, 0, 1); !errors.Is(err, ErrPulseWindowTooLong) {
		t.Fatalf("UsagePulse(49h) error = %v, want ErrPulseWindowTooLong", err)
	}
	if _, err := store.UsagePulse(ctx, UsageQuery{From: t0.Add(time.Hour), To: t0}, 0, 1); !errors.Is(err, ErrPulseInvertedRange) {
		t.Fatalf("UsagePulse(inverted) error = %v, want ErrPulseInvertedRange", err)
	}
}

func TestInt64ReservoirCapsSamples(t *testing.T) {
	r := int64Reservoir{samples: make([]int64, 0, 8)}
	for i := int64(0); i < 1000; i++ {
		r.add(i)
	}
	if len(r.samples) != 8 || cap(r.samples) != 8 || r.seen != 1000 {
		t.Fatalf("reservoir len=%d cap=%d seen=%d", len(r.samples), cap(r.samples), r.seen)
	}
}

func TestUsageByModelDailyTopNAndTimezone(t *testing.T) {
	ctx := context.Background()
	store, err := Open(filepath.Join(t.TempDir(), "usage-model-daily.sqlite3"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer store.Close()

	// UTC 15:30 在 +08:00 仍是 8/1；UTC 16:30 是 8/2。
	records := []UsageLogItem{
		{RequestID: "a1", StartedAt: time.Date(2026, 8, 1, 15, 30, 0, 0, time.UTC), ModelName: "alpha", StatusCode: 200},
		{RequestID: "a2", StartedAt: time.Date(2026, 8, 1, 17, 0, 0, 0, time.UTC), ModelName: "alpha", StatusCode: 200},
		{RequestID: "b1", StartedAt: time.Date(2026, 8, 1, 17, 10, 0, 0, time.UTC), ModelName: "beta", StatusCode: 200},
		{RequestID: "c1", StartedAt: time.Date(2026, 8, 1, 17, 20, 0, 0, time.UTC), ModelName: "gamma", StatusCode: 200},
		{RequestID: "c2", StartedAt: time.Date(2026, 8, 1, 17, 21, 0, 0, time.UTC), ModelName: "delta", StatusCode: 200},
	}
	for _, item := range records {
		saveUsageAggregateFixture(t, store, item)
	}

	rows, err := store.UsageByModelDaily(ctx, UsageQuery{}, 8*60, 2)
	if err != nil {
		t.Fatalf("UsageByModelDaily() error = %v", err)
	}
	// alpha=2, beta/gamma/delta=1 each. Top 2: alpha then beta (name asc among 1s). rest → isOther.
	got := map[string]int{}
	var other UsageModelDailyBucket
	for _, r := range rows {
		key := r.Date + "|" + r.Model
		if r.Other {
			key = r.Date + "|*"
			other = r
		}
		got[key] += r.Requests
	}
	if got["2026-08-01|alpha"] != 1 {
		t.Fatalf("8/1 alpha = %#v", rows)
	}
	if got["2026-08-02|alpha"] != 1 {
		t.Fatalf("8/2 alpha = %#v", rows)
	}
	if got["2026-08-02|beta"] != 1 {
		t.Fatalf("8/2 beta = %#v", rows)
	}
	if got["2026-08-02|*"] != 2 {
		t.Fatalf("8/2 other = %#v, want 2 (gamma+delta)", rows)
	}
	if other.Model != "" || !other.Other {
		t.Fatalf("other bucket = %#v, want empty model and isOther", other)
	}
	if _, ok := got["2026-08-02|gamma"]; ok {
		t.Fatalf("gamma should be folded into isOther: %#v", rows)
	}
}

func TestUsageByModelDailyKeepsLiteralOtherName(t *testing.T) {
	ctx := context.Background()
	store, err := Open(filepath.Join(t.TempDir(), "usage-model-daily-other.sqlite3"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer store.Close()

	t0 := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	records := []UsageLogItem{
		{RequestID: "a1", StartedAt: t0, ModelName: "alpha", StatusCode: 200},
		{RequestID: "a2", StartedAt: t0, ModelName: "alpha", StatusCode: 200},
		{RequestID: "a3", StartedAt: t0, ModelName: "alpha", StatusCode: 200},
		{RequestID: "o1", StartedAt: t0, ModelName: "其他", StatusCode: 200},
		{RequestID: "o2", StartedAt: t0, ModelName: "其他", StatusCode: 200},
		{RequestID: "g1", StartedAt: t0, ModelName: "gamma", StatusCode: 200},
	}
	for _, item := range records {
		saveUsageAggregateFixture(t, store, item)
	}

	rows, err := store.UsageByModelDaily(ctx, UsageQuery{From: t0, To: t0.Add(time.Hour)}, 0, 2)
	if err != nil {
		t.Fatalf("UsageByModelDaily() error = %v", err)
	}
	var namedOther, folded UsageModelDailyBucket
	for _, r := range rows {
		if r.Other {
			folded = r
		}
		if r.Model == "其他" {
			namedOther = r
		}
	}
	if namedOther.Requests != 2 || namedOther.Other {
		t.Fatalf("literal 其他 = %#v, want 2 requests and isOther=false", namedOther)
	}
	if folded.Requests != 1 || folded.Model != "" || !folded.Other {
		t.Fatalf("folded other = %#v, want gamma only", folded)
	}
}

func TestUsageSourceIDFilterDistinguishesSameModelName(t *testing.T) {
	ctx := context.Background()
	store, err := Open(filepath.Join(t.TempDir(), "usage-source-id.sqlite3"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()

	base := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	saveUsageAggregateFixture(t, store, UsageLogItem{RequestID: "a", StartedAt: base, ModelName: "gpt-4", SourceID: "openai", StatusCode: 200, TotalTokens: 10})
	saveUsageAggregateFixture(t, store, UsageLogItem{RequestID: "b", StartedAt: base.Add(time.Minute), ModelName: "gpt-4", SourceID: "azure", StatusCode: 200, TotalTokens: 20})

	totals, err := store.UsageTotals(ctx, UsageQuery{SourceIDs: []string{"openai"}})
	if err != nil {
		t.Fatalf("UsageTotals: %v", err)
	}
	if totals["requests"].(int) != 1 || totals["totalTokens"].(int) != 10 {
		t.Fatalf("source openai totals = %#v, want 1 request / 10 tokens", totals)
	}

	logsTotal, items, err := store.QueryUsageLogs(ctx, UsageQuery{SourceIDs: []string{"azure"}, Limit: 10})
	if err != nil {
		t.Fatalf("QueryUsageLogs: %v", err)
	}
	if logsTotal != 1 || len(items) != 1 || items[0].RequestID != "b" {
		t.Fatalf("source azure logs total=%d items=%#v", logsTotal, items)
	}
}

func TestSaveUsageRecordJSONDuplicateDoesNotDoubleCount(t *testing.T) {
	ctx := context.Background()
	store, err := Open(filepath.Join(t.TempDir(), "usage-dup.sqlite3"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()

	item := UsageLogItem{RequestID: "same", StartedAt: time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC), ModelName: "m", StatusCode: 200, TotalTokens: 7}
	saveUsageAggregateFixture(t, store, item)
	if err := store.SaveUsageRecordJSON(ctx, []byte(`{"requestId":"same"}`), item, item.StartedAt.Add(time.Second)); err != nil {
		t.Fatalf("duplicate save: %v", err)
	}
	totals, err := store.UsageTotals(ctx, UsageQuery{})
	if err != nil {
		t.Fatalf("UsageTotals: %v", err)
	}
	if totals["requests"].(int) != 1 || totals["totalTokens"].(int) != 7 {
		t.Fatalf("duplicate insert must not increment rollup, got %#v", totals)
	}
}

func TestUsageDailyZeroSuccessRequestsStayInJSON(t *testing.T) {
	ctx := context.Background()
	store, err := Open(filepath.Join(t.TempDir(), "usage-zero-success.sqlite3"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()

	saveUsageAggregateFixture(t, store, UsageLogItem{
		RequestID: "fail", StartedAt: time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC),
		ModelName: "m", StatusCode: 500, TotalTokens: 1,
	})
	buckets, err := store.UsageDaily(ctx, UsageQuery{}, 0)
	if err != nil {
		t.Fatalf("UsageDaily: %v", err)
	}
	if len(buckets) != 1 || buckets[0].SuccessRequests != 0 || buckets[0].FailedRequests != 1 {
		t.Fatalf("all-fail day = %#v", buckets)
	}
	payload, err := json.Marshal(buckets[0])
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(payload), `"successRequests":0`) {
		t.Fatalf("zero successRequests must not be omitted: %s", payload)
	}
}

func saveUsageAggregateFixture(t *testing.T, store *Store, item UsageLogItem) {
	t.Helper()
	if err := store.SaveUsageRecordJSON(context.Background(), []byte(`{"requestId":"`+item.RequestID+`"}`), item, item.StartedAt.Add(time.Second)); err != nil {
		t.Fatalf("SaveUsageRecordJSON(%s) error = %v", item.RequestID, err)
	}
}
