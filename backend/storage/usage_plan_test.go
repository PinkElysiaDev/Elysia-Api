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
		`SELECT (started_ms + ?) / 86400000, model_name, COUNT(*), COALESCE(SUM(CASE WHEN status_code >= 200 AND status_code < 400 THEN 1 ELSE 0 END),0), COALESCE(SUM(input_tokens),0), COALESCE(SUM(output_tokens),0), COALESCE(SUM(cache_hit_tokens),0), COALESCE(SUM(total_tokens),0) FROM usage_records WHERE started_ms >= ? AND started_ms < ? GROUP BY 1, 2 ORDER BY 1`,
		int64(8*60*60_000), time.Now().Add(-7*24*time.Hour).UnixMilli(), time.Now().UnixMilli())
	if !strings.Contains(plan, "USING COVERING INDEX idx_usage_agg_cover") {
		t.Fatalf("daily scan must use covering index, plan:\n%s", plan)
	}
}
