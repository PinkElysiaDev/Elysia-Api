package storage

import (
	"context"
	"math/rand"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// seedRollupRecords 通过正式写路径（原始行 + rollup 同事务增量）灌入随机记录，
// 覆盖多模型 / 组 / key / 状态码 / 首字节为 0 的组合，时间散布在若干天内。
func seedRollupRecords(tb testing.TB, store *Store, now time.Time, count int) {
	tb.Helper()
	ctx := context.Background()
	rng := rand.New(rand.NewSource(42))
	models := []string{"gpt-x", "claude-y", "gemini-z", ""}
	groups := []string{"default", "prod", ""}
	keys := []string{"alice", "bob"}
	codes := []int{200, 201, 400, 500, 502}
	for i := 0; i < count; i++ {
		started := now.Add(-time.Duration(rng.Int63n(int64(72 * time.Hour)))) // 近 3 天随机时刻
		if i%17 == 0 {
			started = now.Add(-time.Duration(rng.Int63n(int64(30 * time.Minute)))) // 少量落在当前小时内
		}
		item := UsageLogItem{
			RequestID:      "req-" + time.Now().Format("150405.000000000") + "-" + string(rune('a'+i%26)) + "-" + itoa(i),
			StartedAt:      started,
			KeyName:        keys[rng.Intn(len(keys))],
			GroupName:      groups[rng.Intn(len(groups))],
			ModelName:      models[rng.Intn(len(models))],
			StatusCode:     codes[rng.Intn(len(codes))],
			DurationMs:     int64(rng.Intn(5000)),
			InputTokens:    rng.Intn(2000),
			OutputTokens:   rng.Intn(4000),
			CacheHitTokens: rng.Intn(500),
		}
		item.TotalTokens = item.InputTokens + item.OutputTokens
		if i%3 != 0 {
			item.FirstByteMs = int64(rng.Intn(2000))
		}
		if err := store.SaveUsageRecordJSON(ctx, []byte("{}"), item, started.Add(time.Duration(item.DurationMs)*time.Millisecond)); err != nil {
			tb.Fatalf("seed record %d: %v", i, err)
		}
	}
}

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	digits := ""
	for v > 0 {
		digits = string(rune('0'+v%10)) + digits
		v /= 10
	}
	return digits
}

// forceRawPath 强制后续查询走 raw 路径，返回恢复函数（defer 调用）。
// 测试对等性时作为参照实现。
func forceRawPath(store *Store) func() {
	prev := store.rollupReady.Load()
	store.rollupReady.Store(false)
	return func() { store.rollupReady.Store(prev) }
}

// rollupParityCases 是对等性验证的查询集合：整窗、小时中间起止、多选筛选、
// 状态筛选、精确状态码、无上界 to。
func rollupParityCases(now time.Time) []UsageQuery {
	return []UsageQuery{
		{}, // 全部时间（from/to 均无界）
		{To: now},
		{From: now.Add(-72 * time.Hour), To: now},
		{From: now.Add(-48*time.Hour - 17*time.Minute), To: now.Add(-time.Hour - 3*time.Minute)}, // 双侧小时中间
		{From: now.Add(-24 * time.Hour), To: now, ModelNames: []string{"gpt-x", "claude-y"}},
		{From: now.Add(-24 * time.Hour), To: now, GroupNames: []string{"prod"}},
		{From: now.Add(-24 * time.Hour), To: now, KeyNames: []string{"alice"}},
		{From: now.Add(-24 * time.Hour), To: now, Status: "failed"},
		{From: now.Add(-24 * time.Hour), To: now, StatusCode: 200},
		{From: now.Add(-2 * time.Hour), To: now}, // 不足回填粒度的窄窗（仍应精确）
	}
}

func assertTotalsEqual(t *testing.T, viaRollup, viaRaw map[string]any, label string) {
	t.Helper()
	for _, key := range []string{"requests", "success", "failed", "inputTokens", "outputTokens", "totalTokens", "cacheHitTokens"} {
		if viaRollup[key] != viaRaw[key] {
			t.Fatalf("%s: totals[%s] rollup=%v raw=%v", label, key, viaRollup[key], viaRaw[key])
		}
	}
	for _, key := range []string{"avgDurationMs", "avgFirstByteMs"} {
		r, w := viaRollup[key].(float64), viaRaw[key].(float64)
		if r != w && (r-w > 0.5 || w-r > 0.5) {
			t.Fatalf("%s: totals[%s] rollup=%v raw=%v", label, key, r, w)
		}
	}
	if viaRollup["firstUsedAt"] != viaRaw["firstUsedAt"] || viaRollup["lastUsedAt"] != viaRaw["lastUsedAt"] {
		t.Fatalf("%s: first/lastUsedAt rollup=%v/%v raw=%v/%v", label, viaRollup["firstUsedAt"], viaRollup["lastUsedAt"], viaRaw["firstUsedAt"], viaRaw["lastUsedAt"])
	}
}

// TestRollupParityWithRaw 是阶段二的核心正确性测试：随机数据集下，rollup 路径
// 与强制 raw 路径在四类查询上逐字段一致（含小时中间的边缘窗口、多选筛选、
// 无上界时间窗）。
func TestRollupParityWithRaw(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "rollup-parity.sqlite3"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer store.Close()
	ctx := context.Background()

	now := time.Now()
	seedRollupRecords(t, store, now, 400)
	if err := store.RunRollupBackfill(ctx); err != nil {
		t.Fatalf("backfill: %v", err)
	}
	if !store.rollupReady.Load() {
		t.Fatalf("rollup should be ready after backfill")
	}

	for i, q := range rollupParityCases(now) {
		totalsRollup, err := store.UsageTotals(ctx, q)
		if err != nil {
			t.Fatalf("case %d totals(rollup): %v", i, err)
		}
		restore := forceRawPath(store)
		totalsRaw, err := store.UsageTotals(ctx, q)
		restore()
		if err != nil {
			t.Fatalf("case %d totals(raw): %v", i, err)
		}
		assertTotalsEqual(t, totalsRollup, totalsRaw, "totals")

		dailyRollup, err := store.UsageDaily(ctx, q, 480) // UTC+8
		if err != nil {
			t.Fatalf("case %d daily(rollup): %v", i, err)
		}
		restore = forceRawPath(store)
		dailyRaw, err := store.UsageDaily(ctx, q, 480)
		restore()
		if err != nil {
			t.Fatalf("case %d daily(raw): %v", i, err)
		}
		if !reflect.DeepEqual(dailyRollup, dailyRaw) {
			t.Fatalf("case %d daily mismatch:\nrollup=%+v\nraw=%+v", i, dailyRollup, dailyRaw)
		}

		byModelRollup, err := store.UsageByModel(ctx, q)
		if err != nil {
			t.Fatalf("case %d byModel(rollup): %v", i, err)
		}
		restore = forceRawPath(store)
		byModelRaw, err := store.UsageByModel(ctx, q)
		restore()
		if err != nil {
			t.Fatalf("case %d byModel(raw): %v", i, err)
		}
		if !reflect.DeepEqual(byModelRollup, byModelRaw) {
			t.Fatalf("case %d byModel mismatch:\nrollup=%+v\nraw=%+v", i, byModelRollup, byModelRaw)
		}

		countRollup, _, err := store.QueryUsageLogs(ctx, q)
		if err != nil {
			t.Fatalf("case %d count(rollup): %v", i, err)
		}
		restore = forceRawPath(store)
		countRaw, _, err := store.QueryUsageLogs(ctx, q)
		restore()
		if err != nil {
			t.Fatalf("case %d count(raw): %v", i, err)
		}
		if countRollup != countRaw {
			t.Fatalf("case %d count rollup=%d raw=%d", i, countRollup, countRaw)
		}
	}
}

// TestRollupHalfHourOffsetFallback：+5:30 这类非整小时 offset 下日分桶不能走
// rollup（小时桶会被日边界切开），规划器回退 raw——结果仍须正确。
func TestRollupHalfHourOffsetFallback(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "rollup-offset.sqlite3"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer store.Close()
	ctx := context.Background()

	now := time.Now()
	seedRollupRecords(t, store, now, 200)
	if err := store.RunRollupBackfill(ctx); err != nil {
		t.Fatalf("backfill: %v", err)
	}

	q := UsageQuery{From: now.Add(-72 * time.Hour), To: now}
	fallback, err := store.UsageDaily(ctx, q, 330)
	if err != nil {
		t.Fatalf("daily(330): %v", err)
	}
	restore := forceRawPath(store)
	raw, err := store.UsageDaily(ctx, q, 330)
	restore()
	if err != nil {
		t.Fatalf("daily raw: %v", err)
	}
	if !reflect.DeepEqual(fallback, raw) {
		t.Fatalf("offset 330 fallback mismatch:\n%+v\n%+v", fallback, raw)
	}
}

// TestRollupBackfillIdempotent：回填重复执行不得双计（DELETE+rebuild 幂等）。
func TestRollupBackfillIdempotent(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "rollup-idem.sqlite3"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer store.Close()
	ctx := context.Background()

	now := time.Now()
	seedRollupRecords(t, store, now, 150)
	if err := store.RunRollupBackfill(ctx); err != nil {
		t.Fatalf("backfill 1: %v", err)
	}
	first, err := store.UsageTotals(ctx, UsageQuery{})
	if err != nil {
		t.Fatalf("totals 1: %v", err)
	}
	// 重置状态模拟重启后续跑（ready 已置位时走对账路径；强制回到回填路径再跑一遍）。
	if err := store.setRollupStateInt(ctx, rollupStateReady, 0); err != nil {
		t.Fatalf("reset ready: %v", err)
	}
	store.rollupReady.Store(false)
	if err := store.RunRollupBackfill(ctx); err != nil {
		t.Fatalf("backfill 2: %v", err)
	}
	second, err := store.UsageTotals(ctx, UsageQuery{})
	if err != nil {
		t.Fatalf("totals 2: %v", err)
	}
	if first["requests"] != second["requests"] || first["totalTokens"] != second["totalTokens"] {
		t.Fatalf("re-running backfill double-counted: first=%v second=%v", first["requests"], second["requests"])
	}
}

// TestRollupBackfillEmptyDBSkipsEpochWalk：空库不得从 Unix 纪元空跑到现在
// （否则启动日志会刷「backfilling 2 万天」并卡在 0% 数分钟）。
func TestRollupBackfillEmptyDBSkipsEpochWalk(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "rollup-empty.sqlite3"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer store.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	started := time.Now()
	if err := store.RunRollupBackfill(ctx); err != nil {
		t.Fatalf("empty backfill: %v", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("empty backfill took %s, likely walked from epoch", elapsed)
	}
	if !store.rollupReady.Load() {
		t.Fatal("empty DB should mark rollup ready immediately")
	}
}

// TestRollupGapSelfHeal：模拟中途降级运行旧版二进制（直接向 usage_records 插行、
// 不更新 rollup），重升级后回填对账应发现缺口并自动补齐。
func TestRollupGapSelfHeal(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "rollup-gap.sqlite3"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer store.Close()
	ctx := context.Background()

	now := time.Now()
	seedRollupRecords(t, store, now, 150)
	if err := store.RunRollupBackfill(ctx); err != nil {
		t.Fatalf("backfill: %v", err)
	}

	// 模拟旧版二进制的写入：绕过 rollup 直插 raw 表。
	for i := 0; i < 30; i++ {
		started := now.Add(-time.Duration(20+i) * time.Hour)
		if _, err := store.db.ExecContext(ctx, `INSERT INTO usage_records(request_id, started_at, started_ms, ended_at, key_name, key_hash, group_name, model_name, platform, source_format, target_format, relay_mode, responses_mode, usage_source, stream, status_code, error, first_byte_ms, duration_ms, input_tokens, output_tokens, total_tokens, cache_hit_tokens, request_truncated, response_truncated, record_json)
			VALUES(?, ?, ?, ?, 'ghost', '', 'prod', 'gpt-x', '', '', '', '', '', '', 0, 200, '', 100, 900, 11, 22, 33, 0, 0, 0, '{}')`,
			"ghost-"+itoa(i), started.UTC().Format(time.RFC3339Nano), started.UnixMilli(), started.Add(time.Second).UTC().Format(time.RFC3339Nano)); err != nil {
			t.Fatalf("ghost insert: %v", err)
		}
	}

	if err := store.RunRollupBackfill(ctx); err != nil {
		t.Fatalf("audit backfill: %v", err)
	}
	ghostTotals, err := store.UsageTotals(ctx, UsageQuery{ModelNames: []string{"gpt-x"}, GroupNames: []string{"prod"}})
	if err != nil {
		t.Fatalf("ghost totals: %v", err)
	}
	restore := forceRawPath(store)
	ghostRaw, err := store.UsageTotals(ctx, UsageQuery{ModelNames: []string{"gpt-x"}, GroupNames: []string{"prod"}})
	restore()
	if err != nil {
		t.Fatalf("ghost raw: %v", err)
	}
	assertTotalsEqual(t, ghostTotals, ghostRaw, "after self-heal")
	if ghostTotals["requests"].(int) < 30 {
		t.Fatalf("ghost records missing after self-heal: %+v", ghostTotals["requests"])
	}
}

// TestRollupClearUsage：清空后 rollup 与状态一并重置，新记录恰好计数一次。
func TestRollupClearUsage(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "rollup-clear.sqlite3"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer store.Close()
	ctx := context.Background()

	now := time.Now()
	seedRollupRecords(t, store, now, 80)
	if err := store.RunRollupBackfill(ctx); err != nil {
		t.Fatalf("backfill: %v", err)
	}
	if err := store.ClearUsage(ctx); err != nil {
		t.Fatalf("clear: %v", err)
	}
	totals, err := store.UsageTotals(ctx, UsageQuery{})
	if err != nil {
		t.Fatalf("totals after clear: %v", err)
	}
	if totals["requests"] != 0 {
		t.Fatalf("after clear requests=%v, want 0", totals["requests"])
	}

	fresh := now.Add(-time.Minute)
	if err := store.SaveUsageRecordJSON(ctx, []byte("{}"), UsageLogItem{
		RequestID: "post-clear", StartedAt: fresh, ModelName: "m", StatusCode: 200, TotalTokens: 7,
	}, fresh.Add(time.Second)); err != nil {
		t.Fatalf("post-clear save: %v", err)
	}
	totals, err = store.UsageTotals(ctx, UsageQuery{})
	if err != nil {
		t.Fatalf("totals post-clear: %v", err)
	}
	if totals["requests"] != 1 || totals["totalTokens"] != 7 {
		t.Fatalf("post-clear totals=%+v, want 1 request / 7 tokens", totals)
	}
}

// TestRollupKeyHashFallsBackToRaw：keyHash 筛选（遗留面板维度）必须回退 raw。
func TestRollupKeyHashFallsBackToRaw(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "rollup-keyhash.sqlite3"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer store.Close()
	ctx := context.Background()

	now := time.Now()
	seedRollupRecords(t, store, now, 60)
	if err := store.RunRollupBackfill(ctx); err != nil {
		t.Fatalf("backfill: %v", err)
	}
	q := UsageQuery{From: now.Add(-72 * time.Hour), To: now, KeyHash: "nope"}
	totals, err := store.UsageTotals(ctx, q)
	if err != nil {
		t.Fatalf("totals: %v", err)
	}
	if totals["requests"] != 0 {
		t.Fatalf("keyHash filter should match nothing, got %v", totals["requests"])
	}
}

// BenchmarkUsageAggregation 对比 raw 与 rollup 路径在 5 万条 / 30 天数据上的
// 聚合耗时（go test -bench BenchmarkUsageAggregation -benchtime=20x）。
// 仅衡量聚合计算本身；raw 路径在大 record_json 真实库上还会额外承受回表放大。
func BenchmarkUsageAggregation(b *testing.B) {
	store, err := Open(filepath.Join(b.TempDir(), "rollup-bench.sqlite3"))
	if err != nil {
		b.Fatalf("open: %v", err)
	}
	defer store.Close()
	ctx := context.Background()

	now := time.Now()
	seedRollupRecords(b, store, now, 50_000)
	if err := store.RunRollupBackfill(ctx); err != nil {
		b.Fatalf("backfill: %v", err)
	}
	q := UsageQuery{From: now.Add(-72 * time.Hour), To: now}

	b.Run("totals-raw", func(b *testing.B) {
		restore := forceRawPath(store)
		defer restore()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if _, err := store.UsageTotals(ctx, q); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("totals-rollup", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if _, err := store.UsageTotals(ctx, q); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("daily-raw", func(b *testing.B) {
		restore := forceRawPath(store)
		defer restore()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if _, err := store.UsageDaily(ctx, q, 480); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("daily-rollup", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if _, err := store.UsageDaily(ctx, q, 480); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("count-raw", func(b *testing.B) {
		restore := forceRawPath(store)
		defer restore()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if _, _, err := store.QueryUsageLogs(ctx, q); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("count-rollup", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if _, _, err := store.QueryUsageLogs(ctx, q); err != nil {
				b.Fatal(err)
			}
		}
	})
}
