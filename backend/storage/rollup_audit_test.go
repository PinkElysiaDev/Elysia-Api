package storage

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

// rollupHourCount 读取某小时的 rollup 总计数（cnt 求和）。
func rollupHourCount(t *testing.T, s *Store, hourMs int64) int64 {
	t.Helper()
	var count int64
	if err := s.db.QueryRowContext(context.Background(),
		`SELECT COALESCE(SUM(cnt), 0) FROM usage_rollup_hour WHERE hour_ms = ?`, hourMs).Scan(&count); err != nil {
		t.Fatalf("rollup count: %v", err)
	}
	return count
}

// 回归：留存清理删掉某小时的部分 raw 后（raw < rollup），启动审计不得从
// raw 重建该小时——否则统计被静默削掉，违背「清理不影响统计」的设计承诺。
// 漏计方向（rollup < raw）仍必须重建修复。
func TestAuditSkipsRetentionCleanedHoursButRepairsMissedWrites(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "rollup-audit.sqlite3"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()
	ctx := context.Background()

	// 锚定在当前小时的中点，避免用例跨小时边界翻车。
	nowMs := time.Now().UnixMilli()
	hourMs := nowMs / 3_600_000 * 3_600_000
	base := time.UnixMilli(hourMs + 1_800_000)

	seed := func(id string, at time.Time) {
		t.Helper()
		summary := UsageLogItem{RequestID: id, StartedAt: at, ModelName: "m", StatusCode: 200}
		if err := s.SaveUsageRecordJSON(ctx, []byte(`{"requestId":"`+id+`"}`), summary, at.Add(time.Second)); err != nil {
			t.Fatalf("seed %s: %v", id, err)
		}
	}
	seed("a", base.Add(-20*time.Minute))
	seed("b", base.Add(-10*time.Minute))
	seed("c", base)

	if got := rollupHourCount(t, s, hourMs); got != 3 {
		t.Fatalf("precondition: rollup cnt = %d, want 3", got)
	}

	// 留存清理删掉最旧的一条（同小时部分删除）。
	if ids, err := s.DeleteUsageOlderThan(ctx, base.Add(-15*time.Minute).UnixMilli(), 500); err != nil || len(ids) != 1 {
		t.Fatalf("DeleteUsageOlderThan ids=%v err=%v", ids, err)
	}

	// 审计：rollup(3) > raw(2) → 视为留存漂移，跳过重建，计数保持。
	rebuilt, err := s.auditRollupHours(ctx)
	if err != nil {
		t.Fatalf("audit: %v", err)
	}
	if rebuilt != 0 {
		t.Fatalf("retention-cleaned hour must not be rebuilt, rebuilt=%d", rebuilt)
	}
	if got := rollupHourCount(t, s, hourMs); got != 3 {
		t.Fatalf("rollup cnt after audit = %d, want 3 (stats must survive retention)", got)
	}

	// 漏计方向（rollup 行丢失 → rollup < raw）仍必须触发重建。
	if _, err := s.db.ExecContext(ctx, `DELETE FROM usage_rollup_hour WHERE hour_ms = ?`, hourMs); err != nil {
		t.Fatalf("drop rollup rows: %v", err)
	}
	rebuilt, err = s.auditRollupHours(ctx)
	if err != nil {
		t.Fatalf("audit repair: %v", err)
	}
	if rebuilt == 0 {
		t.Fatal("missed-write direction (rollup < raw) must be rebuilt")
	}
	if got := rollupHourCount(t, s, hourMs); got != 2 {
		t.Fatalf("rollup cnt after repair = %d, want 2 (raw count)", got)
	}
}

// 回归：回填持锁期间 ClearUsage 必须立即失败而不是排队——resetUsage 的调用
// 方已持有 usage writer/persist 锁，排队等回填（大库可达数分钟）会卡住全部
// 请求的 usage 落库。
func TestClearUsageFailsFastWhileBackfillHoldsLock(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "clear-backfill.sqlite3"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	s.rollupMu.Lock()
	defer s.rollupMu.Unlock()
	if err := s.ClearUsage(context.Background()); !errors.Is(err, ErrRollupBackfillInProgress) {
		t.Fatalf("ClearUsage during backfill = %v, want ErrRollupBackfillInProgress", err)
	}
}
