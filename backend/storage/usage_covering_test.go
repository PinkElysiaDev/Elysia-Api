package storage

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// 聚合覆盖索引（idx_usage_agg_cover）存在且被聚合查询以 covering 方式使用：
// 时间过滤 + SUM 的统计不回表读取 record_json 胖行（月级数据聚合慢的根因）。
func TestUsageAggregateCoveringIndex(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "agg-cover.sqlite3"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer store.Close()
	ctx := context.Background()

	// 索引存在（迁移幂等创建）。
	var name string
	err = store.db.QueryRowContext(ctx,
		`SELECT name FROM sqlite_master WHERE type='index' AND name='idx_usage_agg_cover'`).Scan(&name)
	if err != nil {
		t.Fatalf("covering index missing: %v", err)
	}
	// 旧的单列时间索引应被删除（前缀冗余）。
	var dropped string
	err = store.db.QueryRowContext(ctx,
		`SELECT name FROM sqlite_master WHERE type='index' AND name='idx_usage_started_ms'`).Scan(&dropped)
	if err == nil {
		t.Fatalf("redundant idx_usage_started_ms should be dropped")
	}

	// 写入一条带大 record_json 的记录，验证统计查询走 covering（USING COVERING INDEX）。
	bigBody := strings.Repeat("x", 64*1024)
	started := time.Now().Add(-time.Hour)
	ended := started.Add(2 * time.Second)
	if err := store.SaveUsageRecordJSON(ctx, []byte(bigBody), UsageLogItem{
		RequestID: "r1", StartedAt: started, GroupName: "g", ModelName: "m",
		StatusCode: 200, InputTokens: 10, OutputTokens: 20, TotalTokens: 30,
	}, ended); err != nil {
		t.Fatalf("save record: %v", err)
	}

	plan, err := store.db.QueryContext(ctx, `EXPLAIN QUERY PLAN SELECT COUNT(*), COALESCE(SUM(input_tokens),0), COALESCE(SUM(total_tokens),0) FROM usage_records WHERE started_ms >= ? AND started_ms < ?`, started.Add(-time.Minute).UnixMilli(), time.Now().UnixMilli())
	if err != nil {
		t.Fatalf("explain: %v", err)
	}
	defer plan.Close()
	var planText string
	for plan.Next() {
		var id, parent, notused int
		var detail string
		if err := plan.Scan(&id, &parent, &notused, &detail); err != nil {
			t.Fatalf("scan plan: %v", err)
		}
		planText += detail + "\n"
	}
	if !strings.Contains(planText, "COVERING INDEX idx_usage_agg_cover") {
		t.Fatalf("aggregate must use covering index, plan:\n%s", planText)
	}

	// 聚合结果正确性回归。
	totals, err := store.UsageTotals(ctx, UsageQuery{From: started.Add(-time.Minute), To: time.Now()})
	if err != nil {
		t.Fatalf("totals: %v", err)
	}
	if totals["requests"].(int) != 1 || totals["inputTokens"].(int) != 10 {
		t.Fatalf("totals wrong: %+v", totals)
	}
}
