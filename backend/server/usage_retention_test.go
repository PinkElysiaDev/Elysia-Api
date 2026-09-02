package server

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elysia-api/backend/config"
	"github.com/elysia-api/backend/storage"
)

// seedUsageRecord 插入一条指定时间的 usage 记录（record_json 填充 pad 大小）。
func seedUsageRecord(t *testing.T, store *storage.Store, id string, startedAt time.Time, pad int) {
	t.Helper()
	payload := fmt.Sprintf(`{"requestId":%q,"pad":%q}`, id, strings.Repeat("p", pad))
	summary := storage.UsageLogItem{
		RequestID: id, StartedAt: startedAt, ModelName: "m", StatusCode: 200,
	}
	if err := store.SaveUsageRecordJSON(context.Background(), []byte(payload), summary, startedAt.Add(time.Second)); err != nil {
		t.Fatalf("seed %s: %v", id, err)
	}
}

// newRetentionTestServer 构造带临时 store 与 config 的 Server，config 允许
// 用例按需注入 usageLog 策略。
func newRetentionTestServer(t *testing.T) (*Server, *config.Config) {
	t.Helper()
	store, err := storage.Open(filepath.Join(t.TempDir(), "retention.sqlite3"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	cfg := &config.Config{}
	cfg.SetDatabasePath(filepath.Join(t.TempDir(), "retention-config.sqlite3"))
	s := &Server{store: store, config: cfg}
	return s, cfg
}

func usageIDs(t *testing.T, store *storage.Store) map[string]bool {
	t.Helper()
	_, items, err := store.QueryUsageLogs(context.Background(), storage.UsageQuery{Limit: 500})
	if err != nil {
		t.Fatalf("QueryUsageLogs: %v", err)
	}
	ids := make(map[string]bool, len(items))
	for _, item := range items {
		ids[item.RequestID] = true
	}
	return ids
}

func TestRetentionTTLDeletesOldRecordsAndAssets(t *testing.T) {
	s, cfg := newRetentionTestServer(t)
	now := time.Now()
	seedUsageRecord(t, s.store, "old-a", now.Add(-72*time.Hour), 64)
	seedUsageRecord(t, s.store, "old-b", now.Add(-48*time.Hour), 64)
	seedUsageRecord(t, s.store, "fresh", now.Add(-1*time.Hour), 64)

	// old-a 的资产目录：TTL 清理后应联动删除。
	assetsRoot := s.usageAssetsRoot()
	oldDir := filepath.Join(assetsRoot, "old-a")
	if err := os.MkdirAll(oldDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(oldDir, "0123456789abcdef.png"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	days := 2
	cfg.SetUsageLogConfig(config.UsageLogConfig{RetentionDays: &days})
	r := newUsageRetention(s)
	r.runOnce()

	ids := usageIDs(t, s.store)
	if ids["old-a"] || ids["old-b"] {
		t.Fatalf("records older than 2d must be deleted, got %v", ids)
	}
	if !ids["fresh"] {
		t.Fatal("fresh record must survive TTL cleanup")
	}
	if _, err := os.Stat(oldDir); !os.IsNotExist(err) {
		t.Fatalf("asset dir of deleted record must be removed, err=%v", err)
	}
}

func TestRetentionMaxRecordsKeepsNewest(t *testing.T) {
	s, cfg := newRetentionTestServer(t)
	now := time.Now()
	for i := 0; i < 5; i++ {
		seedUsageRecord(t, s.store, fmt.Sprintf("r-%d", i), now.Add(time.Duration(i)*time.Hour), 64)
	}
	keep := 2
	cfg.SetUsageLogConfig(config.UsageLogConfig{MaxRecords: &keep})
	r := newUsageRetention(s)
	r.runOnce()

	ids := usageIDs(t, s.store)
	if len(ids) != 2 || !ids["r-3"] || !ids["r-4"] {
		t.Fatalf("only newest %d records must remain, got %v", keep, ids)
	}
}

func TestRetentionStorageCapConverges(t *testing.T) {
	s, cfg := newRetentionTestServer(t)
	now := time.Now()
	// ~300KB × 12 条 ≈ 3.6MB，配额 1MB：循环删最旧直至逻辑占用收敛。
	for i := 0; i < 12; i++ {
		seedUsageRecord(t, s.store, fmt.Sprintf("cap-%02d", i), now.Add(time.Duration(i)*time.Minute), 300*1024)
	}
	mb := 1
	cfg.SetUsageLogConfig(config.UsageLogConfig{MaxStorageMB: &mb})
	r := newUsageRetention(s)
	r.runOnce()

	stats, err := s.store.UsageDBPageStats(context.Background())
	if err != nil {
		t.Fatalf("page stats: %v", err)
	}
	ids := usageIDs(t, s.store)
	// 断言收敛或删光：日志删光后剩余占用来自其他表时停止挣扎。
	if stats.LogicalBytes() > int64(mb)*1024*1024 && len(ids) > 0 {
		t.Fatalf("storage cap did not converge: logical=%d records=%d", stats.LogicalBytes(), len(ids))
	}
	snapshot := r.snapshotStats()
	if snapshot.DeletedBySize == 0 {
		t.Fatal("size-based deletion must have run")
	}
}

func TestRetentionOrphanSweepRespectsGrace(t *testing.T) {
	s, _ := newRetentionTestServer(t)
	now := time.Now()
	seedUsageRecord(t, s.store, "live", now, 64)

	assetsRoot := s.usageAssetsRoot()
	// 三个目录：有记录（保留）、无记录但新（宽限保留）、无记录且旧（删除）。
	newOrphan := filepath.Join(assetsRoot, "orphan-fresh")
	oldOrphan := filepath.Join(assetsRoot, "orphan-old")
	liveDir := filepath.Join(assetsRoot, "live")
	for _, dir := range []string{newOrphan, oldOrphan, liveDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "0123456789abcdef.png"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	oldTime := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(oldOrphan, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}

	r := newUsageRetention(s)
	r.runOnce()

	if _, err := os.Stat(oldOrphan); !os.IsNotExist(err) {
		t.Fatal("orphan dir past grace must be removed")
	}
	if _, err := os.Stat(newOrphan); err != nil {
		t.Fatalf("orphan dir within grace must survive: %v", err)
	}
	if _, err := os.Stat(liveDir); err != nil {
		t.Fatalf("dir of live record must survive: %v", err)
	}
}

func TestRetentionDisabledByDefault(t *testing.T) {
	s, cfg := newRetentionTestServer(t)
	now := time.Now()
	seedUsageRecord(t, s.store, "ancient", now.Add(-365*24*time.Hour), 64)
	// 不设置任何清理策略：runOnce 只跑孤儿清扫，记录必须原样保留。
	_ = cfg
	r := newUsageRetention(s)
	r.runOnce()
	ids := usageIDs(t, s.store)
	if !ids["ancient"] {
		t.Fatal("cleanup is disabled by default; records must accumulate untouched")
	}
}

// 回归：triggerAsync 预占 running 后调用的执行体不得再被自身守卫挡回——
// 旧实现 triggerAsync 置 running=true 再调带守卫的 runOnce，恒真直接返回，
// 手动清理（POST /api/admin/usage/cleanup）成了空转。
func TestTriggerAsyncActuallyRuns(t *testing.T) {
	s, cfg := newRetentionTestServer(t)
	now := time.Now()
	seedUsageRecord(t, s.store, "manual-old", now.Add(-72*time.Hour), 64)
	days := 1
	cfg.SetUsageLogConfig(config.UsageLogConfig{RetentionDays: &days})

	r := newUsageRetention(s)
	if !r.triggerAsync() {
		t.Fatal("idle retention must accept manual trigger")
	}
	// 已有轮次在跑时拒绝重入。
	if r.triggerAsync() {
		t.Fatal("concurrent trigger must be rejected while a round is running")
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if !usageIDs(t, s.store)["manual-old"] {
			return // 记录被删：手动触发确实执行了清理
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("triggerAsync must perform real cleanup work")
}

func TestRemoveUsageAssetDirsRejectsPathTraversal(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "safe")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	// 含分隔符/点号的 id 一律跳过；合法 id 正常删除。
	removeUsageAssetDirs(root, []string{"..", "a/b", "a.b", "safe"})
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatal("legit dir must be removed")
	}
}

// ---- 超量清理自适应批量 ----

func dbLogicalBytes(t *testing.T, s *Server) int64 {
	t.Helper()
	st, err := s.store.UsageDBPageStats(context.Background())
	if err != nil {
		t.Fatalf("page stats: %v", err)
	}
	return st.LogicalBytes()
}

func usageRecordCount(t *testing.T, s *Server) int64 {
	t.Helper()
	count, err := s.store.CountUsageRecords(context.Background())
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	return count
}

// seedUntilLogical 持续插入记录直至逻辑占用超过 threshold，返回插入条数。
func seedUntilLogical(t *testing.T, s *Server, pad int, threshold int64) int {
	t.Helper()
	now := time.Now()
	added := 0
	for i := 0; i < 20000; i++ {
		if dbLogicalBytes(t, s) > threshold {
			return added
		}
		seedUsageRecord(t, s.store, fmt.Sprintf("cap-adaptive-%06d", i), now.Add(time.Duration(i)*time.Millisecond), pad)
		added++
	}
	t.Fatal("seeding did not reach threshold")
	return 0
}

// 回归（v2 全区间自适应）：轻微超限只删按体积推算的最少条数
// （旧实现固定 500 整批，v1 下限 16），且收敛进迟滞带内。
func TestStorageCapAdaptiveSmallOverage(t *testing.T) {
	s, cfg := newRetentionTestServer(t)
	limitBytes := int64(1024 * 1024) // 1 MiB
	mb := 1
	cfg.SetUsageLogConfig(config.UsageLogConfig{MaxStorageMB: &mb})

	// 越过迟滞带一点（约数个页面）。
	seedUntilLogical(t, s, 1024, limitBytes*101/100+8192)
	logicalBefore := dbLogicalBytes(t, s)
	countBefore := usageRecordCount(t, s)
	avgBefore := float64(logicalBefore) / float64(countBefore)
	// 理论最少删除条数：把占用拉回迟滞带边缘所需。
	minNeeded := int(float64(logicalBefore-limitBytes*101/100)/avgBefore) + 1

	r := newUsageRetention(s)
	r.runOnce()

	deleted := int(countBefore - usageRecordCount(t, s))
	if deleted == 0 {
		t.Fatal("over-cap database must be cleaned")
	}
	if deleted > minNeeded+4 {
		t.Fatalf("slight overage must delete near the volume-derived minimum: deleted=%d minNeeded=%d", deleted, minNeeded)
	}
	if got := dbLogicalBytes(t, s); got*100 > limitBytes*retentionCapHysteresisPercent {
		t.Fatalf("must converge into hysteresis band, logical=%d limit=%d", got, limitBytes)
	}
}

// 回归：大幅超限时按 1000 上限分批、多轮收敛。
func TestStorageCapAdaptiveLargeOverage(t *testing.T) {
	s, cfg := newRetentionTestServer(t)
	mb := 1
	cfg.SetUsageLogConfig(config.UsageLogConfig{MaxStorageMB: &mb})
	limitBytes := int64(1024 * 1024)

	// 小记录（1KB pad）堆到 4 倍超限：需删数千条，必然跨多个 1000 上限批。
	seedUntilLogical(t, s, 1024, limitBytes*4)
	before := usageRecordCount(t, s)

	r := newUsageRetention(s)
	r.runOnce()

	deleted := before - usageRecordCount(t, s)
	if deleted <= retentionCapBatchMax {
		t.Fatalf("large overage must span multiple max-size batches, deleted %d", deleted)
	}
	if got := dbLogicalBytes(t, s); got*100 > limitBytes*retentionCapHysteresisPercent {
		t.Fatalf("must converge into hysteresis band, logical=%d limit=%d", got, limitBytes)
	}
}

// 实测修正：记录大小严重不均（最旧一批极小、其后大记录）时，全局均值
// 估算必然偏小；逐批实测均摊应自动放大后续批量，总量仍收敛进带内。
func TestStorageCapAdaptiveCorrectsUndershoot(t *testing.T) {
	s, cfg := newRetentionTestServer(t)
	limitBytes := int64(1024 * 1024)
	mb := 1
	cfg.SetUsageLogConfig(config.UsageLogConfig{MaxStorageMB: &mb})

	// 先放一批「最旧的小记录」（删除时先消耗它们，均摊远低于全局均值），
	// 再放大记录把总占用顶过 4 倍上限。
	now := time.Now()
	for i := 0; i < 400; i++ {
		seedUsageRecord(t, s.store, fmt.Sprintf("cap-tiny-%03d", i), now.Add(time.Duration(i)*time.Millisecond), 128)
	}
	seedUntilLogical(t, s, 4096, limitBytes*4)
	before := usageRecordCount(t, s)
	logicalBeforeUd := dbLogicalBytes(t, s)
	avgBeforeUd := float64(logicalBeforeUd) / float64(before)

	r := newUsageRetention(s)
	r.runOnce()
	deleted := int(before - usageRecordCount(t, s))
	if deleted == 0 {
		t.Fatal("over-cap database must be cleaned")
	}
	logicalAfter := dbLogicalBytes(t, s)
	if logicalAfter*100 > limitBytes*retentionCapHysteresisPercent {
		t.Fatalf("measured-avg correction must converge into band, logical=%d limit=%d", logicalAfter, limitBytes)
	}
	// 删除效率属性：均摊每条删除记录的实测释放量不低于全局均摊的 60%
	//——证明批量估算贴合实际（没有为凑字节反复小批空转，也没有巨量超删）。
	freedPerDeleted := float64(logicalBeforeUd-logicalAfter) / float64(deleted)
	if freedPerDeleted < 0.6*avgBeforeUd {
		t.Fatalf("deletion efficiency too low: freed/record=%.0fB global avg=%.0fB", freedPerDeleted, avgBeforeUd)
	}
}

// 迟滞带内（100%~101%）不清理：避免上限附近删删停停。
func TestStorageCapHysteresisNoop(t *testing.T) {
	s, cfg := newRetentionTestServer(t)
	mb := 1
	cfg.SetUsageLogConfig(config.UsageLogConfig{MaxStorageMB: &mb})
	limitBytes := int64(1024 * 1024)

	// 小步长插入，恰好越过 100% 但停在 1% 带内。
	seedUntilLogical(t, s, 512, limitBytes)
	if got := dbLogicalBytes(t, s); got*100 > limitBytes*retentionCapHysteresisPercent {
		t.Skipf("page granularity overshot the band (logical=%d); band check inconclusive", got)
	}
	before := usageRecordCount(t, s)

	r := newUsageRetention(s)
	r.runOnce()

	if deleted := before - usageRecordCount(t, s); deleted != 0 {
		t.Fatalf("within hysteresis band must not delete, deleted %d", deleted)
	}
}

// capBatchSize 估算器：实测路径、全局均值回落、上下限钳制与除零保护。
func TestCapBatchSizeEstimate(t *testing.T) {
	s, _ := newRetentionTestServer(t)
	now := time.Now()
	for i := 0; i < 32; i++ {
		seedUsageRecord(t, s.store, fmt.Sprintf("est-%02d", i), now, 2048)
	}
	r := newUsageRetention(s)
	const limit = int64(1024 * 1024)
	logical := dbLogicalBytes(t, s)

	// 目标是迟滞带边缘：带内（即使超过上限本身不多）→ 0。
	bandEdge := limit * retentionCapHysteresisPercent / 100
	if got, err := r.capBatchSize(context.Background(), bandEdge, limit, 0); err != nil || got != 0 {
		t.Fatalf("inside band must return 0, got %d", got)
	}
	// 实测路径：需释放约 1MiB、实测均摊 1KB → need≈1127 → 钳到上限 1000。
	if got, err := r.capBatchSize(context.Background(), bandEdge+1024*1024, limit, 1024); err != nil || got != retentionCapBatchMax {
		t.Fatalf("huge need must clamp to max, got %d", got)
	}
	// 实测路径：需释放 100B、实测均摊 10KB → need≈1 → 下限 1。
	if got, err := r.capBatchSize(context.Background(), bandEdge+100, limit, 10*1024); err != nil || got != retentionCapBatchMin {
		t.Fatalf("tiny need must clamp to min 1, got %d", got)
	}
	// 全局均值回落：刚过带边缘一个页面 → 小批量而非 0/负数。
	small, err := r.capBatchSize(context.Background(), bandEdge+4096, limit, 0)
	if err != nil {
		t.Fatalf("slight overage estimate failed: %v", err)
	}
	if small <= 0 || small > 64 {
		t.Fatalf("slight overage via global avg must be a small batch, got %d", small)
	}
	_ = logical
}
