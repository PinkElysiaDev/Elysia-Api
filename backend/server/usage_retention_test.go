package server

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

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
	// 距边界留 1 分钟：播种与清理同毫秒执行时 started_ms == cutoff 会被
	// 严格 < 语义保留（Windows 时钟粒度下可复现的偶发失败）。
	seedUsageRecord(t, s.store, "old-b", now.Add(-48*time.Hour-time.Minute), 64)
	seedUsageRecord(t, s.store, "fresh", now.Add(-1*time.Hour), 64)

	// old-a 的资产文件（扁平布局）+ 引用：TTL 清理删记录后应联动删引用与文件。
	assetsRoot := s.usageAssetsRoot()
	if err := os.MkdirAll(assetsRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(assetsRoot, "0123456789abcdef.png"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := registerTestAsset(s.store, "old-a", "0123456789abcdef.png", 1); err != nil {
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
	if _, err := os.Stat(filepath.Join(assetsRoot, "0123456789abcdef.png")); !os.IsNotExist(err) {
		t.Fatalf("asset file of deleted record must be removed, err=%v", err)
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
	cfg.SetUsageLogConfig(config.UsageLogConfig{MaxContentMB: &mb})
	r := newUsageRetention(s)
	r.runOnce()

	stats, err := s.store.LogContentStats(context.Background())
	if err != nil {
		t.Fatalf("page stats: %v", err)
	}
	ids := usageIDs(t, s.store)
	// 断言收敛或删光：日志删光后剩余占用来自其他表时停止挣扎。
	if (stats.UsageBytes+stats.MediaBytes) > int64(mb)*1024*1024 && len(ids) > 0 {
		t.Fatalf("storage cap did not converge: logical=%d records=%d", (stats.UsageBytes + stats.MediaBytes), len(ids))
	}
	snapshot := r.snapshotStats()
	if snapshot.UsageDeleted.ByContent == 0 {
		t.Fatal("size-based deletion must have run")
	}
}

func TestRetentionOrphanSweepRespectsGrace(t *testing.T) {
	s, _ := newRetentionTestServer(t)
	now := time.Now()
	seedUsageRecord(t, s.store, "live", now, 64)

	assetsRoot := s.usageAssetsRoot()
	// 三个文件：被 live 引用（保留）、无引用但新（宽限保留）、无引用且旧（删除）。
	if err := os.MkdirAll(assetsRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	newOrphan := filepath.Join(assetsRoot, "1111111111111111.png")
	oldOrphan := filepath.Join(assetsRoot, "2222222222222222.png")
	referenced := filepath.Join(assetsRoot, "3333333333333333.png")
	for path, content := range map[string][]byte{
		newOrphan:  []byte("n"),
		oldOrphan:  []byte("o"),
		referenced: []byte("r"),
	} {
		if err := os.WriteFile(path, content, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := registerTestAsset(s.store, "live", "3333333333333333.png", 1); err != nil {
		t.Fatal(err)
	}
	oldTime := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(oldOrphan, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}

	r := newUsageRetention(s)
	r.runOnce()

	if _, err := os.Stat(oldOrphan); !os.IsNotExist(err) {
		t.Fatal("unreferenced file past grace must be removed")
	}
	if _, err := os.Stat(newOrphan); err != nil {
		t.Fatalf("orphan file within grace must survive: %v", err)
	}
	if _, err := os.Stat(referenced); err != nil {
		t.Fatalf("referenced file must survive: %v", err)
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
	defer r.shutdown()
	if !r.triggerAsync() {
		t.Fatal("idle retention must accept manual trigger")
	}
	// Maintenance wakeups coalesce instead of being dropped.
	if !r.triggerAsync() {
		t.Fatal("concurrent trigger must be queued")
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

func registerTestAsset(store *storage.Store, id, file string, size int64) error {
	return store.ExecRaw(context.Background(), fmt.Sprintf("INSERT INTO usage_assets(asset_file,size_bytes) VALUES('%s',%d) ON CONFLICT DO NOTHING; INSERT INTO usage_asset_refs(asset_file,request_id) VALUES('%s','%s')", file, size, file, id))
}

func TestMaintenanceReclaimsDeletedPagesWithoutRetention(t *testing.T) {
	s, _ := newRetentionTestServer(t)
	ctx := context.Background()
	for i := 0; i < 12; i++ {
		seedUsageRecord(t, s.store, fmt.Sprint(i), time.Now(), 256*1024)
	}
	if _, err := s.store.Checkpoint(ctx, true); err != nil {
		t.Fatal(err)
	}
	before, err := s.store.LogStorageStats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.store.ClearUsage(ctx); err != nil {
		t.Fatal(err)
	}
	r := newUsageRetention(s)
	r.runOnce()
	state := r.snapshotStats()
	after, err := s.store.LogStorageStats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if state.State != "completed" || after.FreePages != 0 || after.FileBytes >= before.FileBytes/2 || after.WALBytes != 0 {
		t.Fatalf("no-policy reclaim: %+v before=%+v after=%+v err=%v", state, before, after, err)
	}
}

func TestMaintenanceQueuesWakeDuringRunAndShutdownWaits(t *testing.T) {
	s, _ := newRetentionTestServer(t)
	r := newUsageRetention(s)
	s.usagePersistMu.Lock()
	r.start()
	waitMaintenance(t, r, func(st retentionStats) bool { return st.State == "running" })
	initial := r.snapshotStats().LastRunAt
	for i := 0; i < 20; i++ {
		if !r.triggerAsync() {
			t.Fatal("wakeup dropped")
		}
	}
	s.usagePersistMu.Unlock()
	waitMaintenance(t, r, func(st retentionStats) bool {
		return st.State == "completed" && st.LastRunAt.After(initial) && !st.Pending
	})
	r.shutdown()
	select {
	case <-r.done:
	default:
		t.Fatal("shutdown did not wait")
	}
	if r.triggerAsync() {
		t.Fatal("stopped worker accepted maintenance")
	}
}

func TestMaintenanceRetriesAttachmentFailure(t *testing.T) {
	s, _ := newRetentionTestServer(t)
	ctx := context.Background()
	root := s.usageAssetsRoot()
	file := "0123456789abcdef.png"
	// A non-empty directory guarantees unlink failure even under elevated test users.
	if err := os.MkdirAll(filepath.Join(root, file), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, file, "blocked"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	seedUsageRecord(t, s.store, "old", time.Now(), 100)
	if err := registerTestAsset(s.store, "old", file, 1); err != nil {
		t.Fatal(err)
	}
	if err := s.store.ClearUsage(ctx); err != nil {
		t.Fatal(err)
	}
	r := newUsageRetention(s)
	r.runOnce()
	stats, _ := s.store.LogContentStats(ctx)
	if r.snapshotStats().State != "failed" || stats.PendingMediaBytes != 1 {
		t.Fatalf("failed file deletion lost pending metadata: %+v %+v", r.snapshotStats(), stats)
	}
	if err := os.Remove(filepath.Join(root, file, "blocked")); err != nil {
		t.Fatal(err)
	}
	r.runOnce()
	stats, _ = s.store.LogContentStats(ctx)
	if r.snapshotStats().State != "completed" || stats.PendingMediaBytes != 0 {
		t.Fatalf("retry failed: %+v %+v", r.snapshotStats(), stats)
	}
}

func TestMaintenanceLongReaderWaitsThenRecovers(t *testing.T) {
	s, cfg := newRetentionTestServer(t)
	ctx := context.Background()
	seedUsageRecord(t, s.store, "old", time.Now().Add(-48*time.Hour), 1024*1024)
	reader, err := sql.Open("sqlite", filepath.Join(filepath.Dir(s.usageAssetsRoot()), "retention.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	tx, err := reader.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var count int
	if err := tx.QueryRow(`SELECT count(*) FROM usage_records`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	day := 1
	cfg.SetUsageLogConfig(config.UsageLogConfig{RetentionDays: &day})
	r := newUsageRetention(s)
	r.runOnce()
	if st := r.snapshotStats(); st.State != "waiting" || !st.CheckpointBlocked {
		t.Fatalf("reader blockage hidden: %+v", st)
	}
	tx.Rollback()
	r.runOnce()
	if st := r.snapshotStats(); st.State != "completed" || st.CheckpointBlocked {
		t.Fatalf("retry failed: %+v", st)
	}
	records, _ := s.store.CountUsageRecords(ctx)
	if records != 0 {
		t.Fatal("retention did not delete")
	}
}

func TestResetSchedulesReclaimAndPreservesNewRequests(t *testing.T) {
	s, _ := newRetentionTestServer(t)
	seedUsageRecord(t, s.store, "old", time.Now(), 1024*1024)
	s.usageRetention = newUsageRetention(s)
	defer s.usageRetention.shutdown()
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/reset", nil)
	s.resetUsage(c)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"reclaimQueued":true`) {
		t.Fatalf("reset response: %d %s", rec.Code, rec.Body)
	}
	s.recordUsage(&usageRecord{RequestID: "after", StartedAt: time.Now(), StatusCode: 200})
	waitMaintenance(t, s.usageRetention, func(st retentionStats) bool { return st.State == "completed" && !st.Pending })
	ids := usageIDs(t, s.store)
	if len(ids) != 1 || !ids["after"] {
		t.Fatalf("reset/new record boundary: %v", ids)
	}
}

func TestConcurrentAssetPersistenceAndMaintenance(t *testing.T) {
	s, cfg := newRetentionTestServer(t)
	keep := 2
	cfg.SetUsageLogConfig(config.UsageLogConfig{MaxRecords: &keep})
	r := newUsageRetention(s)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 10; i++ {
			r.runOnce()
		}
	}()
	for i := 0; i < 30; i++ {
		record := newExternalizeRecord(fmt.Sprintf("shared-%02d", i))
		record.StartedAt = time.Now()
		record.StatusCode = 200
		record.IncomingBody = record.sanitizeBody([]byte(`{"url":"data:image/png;base64,` + strings.Repeat("A", 600) + `"}`))
		s.persistUsageRecord(record)
	}
	wg.Wait()
	r.runOnce()
	refs := referencedAssetFiles(t, s.store)
	if len(refs) != 1 {
		t.Fatalf("refs: %v", refs)
	}
	for file := range refs {
		if _, err := os.Stat(filepath.Join(s.usageAssetsRoot(), file)); err != nil {
			t.Fatal("live shared file deleted", err)
		}
	}
	if len(usageIDs(t, s.store)) != 2 {
		t.Fatal("retention did not converge")
	}
}

func waitMaintenance(t *testing.T, r *usageRetention, done func(retentionStats) bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if done(r.snapshotStats()) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("maintenance timeout: %+v", r.snapshotStats())
}
