package storage

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// explainPlan 执行 EXPLAIN QUERY PLAN 并拼接全部 detail 行。
func explainPlan(t *testing.T, store *Store, query string, args ...any) string {
	t.Helper()
	rows, err := store.db.QueryContext(context.Background(), "EXPLAIN QUERY PLAN "+query, args...)
	if err != nil {
		t.Fatalf("explain: %v", err)
	}
	defer rows.Close()
	var text string
	for rows.Next() {
		var id, parent, notused int
		var detail string
		if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
			t.Fatalf("scan plan: %v", err)
		}
		text += detail + "\n"
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("plan rows: %v", err)
	}
	return text
}

// logs 页查询必须避免临时 B-tree 排序：ORDER BY 仅 started_ms DESC 可由
// idx_usage_agg_cover 反向游走满足；一旦出现 TEMP B-tree ORDER BY，排序器会
// 把全窗口含 record_json 的胖行读进来（大库切窗慢的根因之一）。
func TestUsageLogsQueryPlanAvoidsTempSort(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "logs-plan.sqlite3"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer store.Close()

	plan := explainPlan(t, store,
		`SELECT request_id, started_at, key_name, group_name, model_name FROM usage_records WHERE started_ms >= ? AND started_ms < ? ORDER BY started_ms DESC LIMIT ? OFFSET ?`,
		time.Now().Add(-24*time.Hour).UnixMilli(), time.Now().UnixMilli(), 20, 0)
	if strings.Contains(plan, "USE TEMP B-TREE FOR ORDER BY") {
		t.Fatalf("logs query must not temp-sort, plan:\n%s", plan)
	}
	if !strings.Contains(plan, "idx_usage_agg_cover") {
		t.Fatalf("logs query should walk idx_usage_agg_cover, plan:\n%s", plan)
	}
}

// UsageTotals 的全部输出列（含 MIN/MAX）必须落在覆盖索引内：不允许为了
// started_at 文本列回表读取 record_json 胖行。
func TestUsageTotalsQueryPlanCovering(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "totals-plan.sqlite3"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer store.Close()

	plan := explainPlan(t, store,
		`SELECT COUNT(*), COALESCE(SUM(CASE WHEN status_code >= 200 AND status_code < 400 THEN 1 ELSE 0 END),0), COALESCE(SUM(input_tokens),0), COALESCE(SUM(output_tokens),0), COALESCE(SUM(total_tokens),0), COALESCE(SUM(cache_hit_tokens),0), COALESCE(SUM(duration_ms),0), COALESCE(SUM(CASE WHEN first_byte_ms > 0 THEN first_byte_ms END),0), COALESCE(COUNT(CASE WHEN first_byte_ms > 0 THEN 1 END),0), COALESCE(MIN(started_ms),0), COALESCE(MAX(started_ms),0) FROM usage_records WHERE started_ms >= ? AND started_ms < ?`,
		time.Now().Add(-30*24*time.Hour).UnixMilli(), time.Now().UnixMilli())
	if !strings.Contains(plan, "USING COVERING INDEX idx_usage_agg_cover") {
		t.Fatalf("totals must use covering index, plan:\n%s", plan)
	}
}

// UsageDaily 的 (日, 模型) 合并扫描允许 GROUP BY 临时结构，但基础扫描必须
// 保持 covering（不回表读胖行）。
func TestUsageDailyScanPlanCovering(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "daily-plan.sqlite3"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer store.Close()

	plan := explainPlan(t, store,
		`SELECT (started_ms + ?) / 86400000, model_name, COUNT(*), COALESCE(SUM(CASE WHEN status_code >= 200 AND status_code < 400 THEN 1 ELSE 0 END),0), COALESCE(SUM(input_tokens),0), COALESCE(SUM(output_tokens),0), COALESCE(SUM(cache_hit_tokens),0), COALESCE(SUM(total_tokens),0) FROM usage_records WHERE started_ms >= ? AND started_ms < ? AND started_ms > 0 GROUP BY 1, 2 ORDER BY 1`,
		int64(8*60*60_000), time.Now().Add(-7*24*time.Hour).UnixMilli(), time.Now().UnixMilli())
	if !strings.Contains(plan, "USING COVERING INDEX idx_usage_agg_cover") {
		t.Fatalf("daily scan must use covering index, plan:\n%s", plan)
	}
}

// 带维度筛选（模型/组/调用方）的查询必须留在覆盖索引上：遗留单列索引
// （idx_usage_model / idx_usage_group）会把规划器引向"单列索引 + 临时 B-tree
// 排序/逐行回表读 record_json 胖行"的陷阱——大库上带筛选查询几十秒的根因。
// 该测试防止索引被重新引入或规划器退路复现。
func TestUsageFilteredQueryPlansStayCovering(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "filtered-plan.sqlite3"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer store.Close()
	ctx := context.Background()

	started := time.Now().Add(-2 * time.Hour)
	if err := store.SaveUsageRecordJSON(ctx, []byte("{}"), UsageLogItem{
		RequestID: "r1", StartedAt: started, ModelName: "m1", GroupName: "g1",
		KeyName: "k1", StatusCode: 200, TotalTokens: 5,
	}, started.Add(time.Second)); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// 遗留单列索引不存在（迁移已删除）。
	var dropped string
	err = store.db.QueryRowContext(ctx,
		`SELECT name FROM sqlite_master WHERE type='index' AND name IN ('idx_usage_model','idx_usage_group','idx_usage_started_at')`).Scan(&dropped)
	if err == nil {
		t.Fatalf("legacy single-column index %q must be dropped", dropped)
	}

	cases := []struct {
		name  string
		query string
		args  []any
	}{
		{
			name:  "logs page with model IN",
			query: `SELECT request_id, started_at, model_name FROM usage_records WHERE started_ms >= ? AND started_ms < ? AND model_name IN (?) ORDER BY started_ms DESC LIMIT 20 OFFSET 0`,
			args:  []any{started.Add(-time.Hour).UnixMilli(), time.Now().UnixMilli(), "m1"},
		},
		{
			name:  "count with model IN",
			query: `SELECT COUNT(*) FROM usage_records WHERE started_ms >= ? AND started_ms < ? AND model_name IN (?)`,
			args:  []any{started.Add(-time.Hour).UnixMilli(), time.Now().UnixMilli(), "m1"},
		},
		{
			name:  "totals with model IN",
			query: `SELECT COUNT(*), COALESCE(SUM(total_tokens),0) FROM usage_records WHERE started_ms >= ? AND started_ms < ? AND model_name IN (?)`,
			args:  []any{started.Add(-time.Hour).UnixMilli(), time.Now().UnixMilli(), "m1"},
		},
		{
			name:  "by-model with group =",
			query: `SELECT model_name, COUNT(*), COALESCE(SUM(total_tokens),0) FROM usage_records WHERE started_ms >= ? AND started_ms < ? AND group_name = ? GROUP BY model_name`,
			args:  []any{started.Add(-time.Hour).UnixMilli(), time.Now().UnixMilli(), "g1"},
		},
		{
			name:  "daily with key IN",
			query: `SELECT (started_ms + ?) / 86400000, model_name, COUNT(*) FROM usage_records WHERE started_ms >= ? AND started_ms < ? AND key_name IN (?) GROUP BY 1, 2`,
			args:  []any{int64(8 * 60 * 60_000), started.Add(-time.Hour).UnixMilli(), time.Now().UnixMilli(), "k1"},
		},
	}
	for _, tc := range cases {
		plan := explainPlan(t, store, tc.query, tc.args...)
		if !strings.Contains(plan, "idx_usage_agg_cover") {
			t.Fatalf("%s: must use covering index, plan:\n%s", tc.name, plan)
		}
		if strings.Contains(plan, "USE TEMP B-TREE FOR ORDER BY") {
			t.Fatalf("%s: must not temp-sort, plan:\n%s", tc.name, plan)
		}
	}
}
