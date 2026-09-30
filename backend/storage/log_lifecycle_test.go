package storage

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLogDatabaseUsesIncrementalReclamation(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "logs.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var mode int
	if err := s.db.QueryRow(`PRAGMA auto_vacuum`).Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if mode != 2 {
		t.Fatalf("auto_vacuum=%d; deleted pages cannot be incrementally reclaimed", mode)
	}
}

func TestLogContentBudgetAndAtomicAssets(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "logs.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	asset := UsageAsset{File: "0123456789abcdef.png", SizeBytes: 2 * 1024 * 1024}
	for i := 0; i < 3; i++ {
		body := []byte(fmt.Sprintf(`{"text":"中文-%d"}`, i))
		if err := s.SaveUsageRecordJSON(ctx, body, UsageLogItem{RequestID: fmt.Sprint(i), StartedAt: time.Now().Add(time.Duration(i) * time.Second), TotalTokens: 10}, time.Now(), asset); err != nil {
			t.Fatal(err)
		}
	}
	before, err := s.LogContentStats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if before.MediaBytes != asset.SizeBytes || before.UsageRecords != 3 {
		t.Fatalf("dedup: %+v", before)
	}
	if err := s.SaveUsageRecordJSON(ctx, []byte(`{}`), UsageLogItem{RequestID: "0"}, time.Now(), UsageAsset{File: "1111111111111111.png", SizeBytes: 999}); err != nil {
		t.Fatal(err)
	}
	after, _ := s.LogContentStats(ctx)
	if after != before {
		t.Fatalf("duplicate changed content/refs: %+v -> %+v", before, after)
	}
	// A failed reference insertion rolls back both the raw row and its rollup.
	if err := s.SaveUsageRecordJSON(ctx, []byte(`{}`), UsageLogItem{RequestID: "bad"}, time.Now(), UsageAsset{File: "../invalid"}); err == nil {
		t.Fatal("invalid attachment accepted")
	}
	var bad int
	if err := s.db.QueryRow(`SELECT count(*) FROM usage_records WHERE request_id='bad'`).Scan(&bad); err != nil || bad != 0 {
		t.Fatal("failed transaction left row", err)
	}
	deleted, err := s.PruneLogBatch(ctx, false, LogPrunePolicy{MaxRecords: 2})
	if err != nil {
		t.Fatal(err)
	}
	after, _ = s.LogContentStats(ctx)
	if deleted.ByRecords != 1 || after.MediaBytes != asset.SizeBytes || after.PendingMediaBytes != 0 {
		t.Fatalf("shared media released too early: %+v %+v", deleted, after)
	}
	// Content cap counts media, even when only a tiny JSON remains.
	deleted, err = s.PruneLogBatch(ctx, false, LogPrunePolicy{MaxContentBytes: 1024 * 1024})
	if err != nil {
		t.Fatal(err)
	}
	after, _ = s.LogContentStats(ctx)
	if deleted.ByContent != 2 || after.UsageRecords != 0 || after.PendingMediaBytes != asset.SizeBytes {
		t.Fatalf("media quota: %+v %+v", deleted, after)
	}
	var tokens int
	if err := s.db.QueryRow(`SELECT sum(total_tok) FROM usage_rollup_hour`).Scan(&tokens); err != nil || tokens != 30 {
		t.Fatalf("retention changed rollup: %d %v", tokens, err)
	}
}

func TestLogBudgetExcludesBusinessDataAndSystemIsIndependent(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "logs.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	if _, err := s.db.Exec(`INSERT INTO settings(key,value,updated_at) VALUES('large',zeroblob(2*1024*1024),'now')`); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveUsageRecordJSON(ctx, []byte(`{"small":true}`), UsageLogItem{RequestID: "keep"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	deleted, err := s.PruneLogBatch(ctx, false, LogPrunePolicy{MaxContentBytes: 1024})
	if err != nil || deleted.Total() != 0 {
		t.Fatalf("business bytes caused usage deletion: %+v %v", deleted, err)
	}
	for i := 0; i < 4; i++ {
		if err := s.InsertSystemLog(ctx, "info", "系统日志", map[string]int{"i": i}); err != nil {
			t.Fatal(err)
		}
	}
	var expected int64
	if err := s.db.QueryRow(`SELECT sum(length(CAST(created_at||level||message||fields_json AS BLOB))) FROM system_logs`).Scan(&expected); err != nil {
		t.Fatal(err)
	}
	stats, _ := s.LogContentStats(ctx)
	if stats.SystemBytes != expected {
		t.Fatalf("UTF-8 accounting: %+v expected=%d", stats, expected)
	}
	deleted, err = s.PruneLogBatch(ctx, true, LogPrunePolicy{MaxRecords: 2})
	if err != nil || deleted.ByRecords != 2 {
		t.Fatalf("system prune: %+v %v", deleted, err)
	}
	stats, _ = s.LogContentStats(ctx)
	if stats.SystemRecords != 2 || stats.UsageRecords != 1 {
		t.Fatalf("independent retention: %+v", stats)
	}
	if _, err := s.LogStorageStats(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestIncrementalReclamationAndBusyCheckpoint(t *testing.T) {
	p := filepath.Join(t.TempDir(), "logs.sqlite3")
	s, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	if _, err := s.db.Exec(`INSERT INTO usage_records(request_id,started_at,ended_at,record_json,content_bytes) VALUES('large','now','now',zeroblob(4*1024*1024),4194304)`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Checkpoint(ctx, true); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := sql.Open("sqlite", p)
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
	if err := s.ClearUsage(ctx); err != nil {
		t.Fatal(err)
	}
	pages, _ := s.UsageDBPageStats(ctx)
	if err := s.ReclaimLogPages(ctx); err != nil {
		t.Fatal(err)
	}
	after, _ := s.UsageDBPageStats(ctx)
	if pages.FreePages-after.FreePages != 256 {
		t.Fatalf("batch did not consume all PRAGMA rows: %d -> %d", pages.FreePages, after.FreePages)
	}
	cp, err := s.Checkpoint(ctx, true)
	if err != nil || !cp.Busy {
		t.Fatalf("must report busy: %+v %v", cp, err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	for after.FreePages > 0 {
		if err := s.ReclaimLogPages(ctx); err != nil {
			t.Fatal(err)
		}
		after, err = s.UsageDBPageStats(ctx)
		if err != nil {
			t.Fatal(err)
		}
	}
	cp, err = s.Checkpoint(ctx, true)
	if err != nil || cp.Busy {
		t.Fatalf("checkpoint retry: %+v %v", cp, err)
	}
	size, _ := os.Stat(p)
	wal, _ := os.Stat(p + "-wal")
	if size.Size() >= before.Size()/2 || (wal != nil && wal.Size() != 0) {
		t.Fatalf("physical size not reclaimed: %d -> %d", before.Size(), size.Size())
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := s.ReclaimLogPages(canceled); err == nil {
		t.Fatal("canceled maintenance executed")
	}
}

func TestLogMigrationPreservesDataAndResumesAfterFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "logs.sqlite3")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	body := []byte(`{"requestId":"old","incomingBody":{"content":"__ELYSIA_ASSET__:old/0123456789abcdef.png"}}`)
	if err := s.SaveUsageRecordJSON(ctx, body, UsageLogItem{RequestID: "old", TotalTokens: 123}, time.Now()); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(filepath.Dir(path), "usage-assets")
	if err := os.MkdirAll(filepath.Join(root, "old"), 0700); err != nil {
		t.Fatal(err)
	}
	original := []byte("original image bytes")
	if err := os.WriteFile(filepath.Join(root, "old", "0123456789abcdef.png"), original, 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.InsertSystemLog(ctx, "warn", "keep message", nil); err != nil {
		t.Fatal(err)
	}
	// Downgrade the fixture to the old schema/format (no production migrations touched).
	if _, err := s.db.Exec(`DELETE FROM schema_migrations WHERE version=2026092901;
 DROP TABLE usage_asset_refs; DROP TABLE usage_assets;
 CREATE TABLE usage_asset_refs(asset_file TEXT,request_id TEXT,PRIMARY KEY(asset_file,request_id));
 UPDATE usage_records SET content_bytes=0; UPDATE system_logs SET content_bytes=0; PRAGMA auto_vacuum=NONE; VACUUM`); err != nil {
		t.Fatal(err)
	}
	s.Close()
	// Force a deterministic migration failure before its final marker is committed.
	backupRoot := root + ".pre-log-lifecycle"
	if err := os.WriteFile(backupRoot, []byte("block directory"), 0600); err != nil {
		t.Fatal(err)
	}
	if broken, err := Open(path); err == nil {
		broken.Close()
		t.Fatal("partial migration was accepted")
	}
	if err := os.Remove(backupRoot); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	actual, found, err := s.GetUsageRecordJSON(ctx, "old")
	if err != nil || !found || string(actual) != string(body) {
		t.Fatalf("payload changed: found=%v err=%v", found, err)
	}
	stats, err := s.LogContentStats(ctx)
	if err != nil || stats.UsageBytes != int64(len(body)) || stats.MediaBytes != int64(len(original)) || stats.SystemRecords != 1 {
		t.Fatalf("migrated accounting %+v %v", stats, err)
	}
	var tokens int
	if err := s.db.QueryRow(`SELECT sum(total_tok) FROM usage_rollup_hour`).Scan(&tokens); err != nil || tokens != 123 {
		t.Fatalf("rollup=%d %v", tokens, err)
	}
	saved, err := os.ReadFile(filepath.Join(root, "0123456789abcdef.png"))
	if err != nil || string(saved) != string(original) {
		t.Fatal("media changed", err)
	}
	if _, err := os.Stat(path + ".pre-log-lifecycle"); err != nil {
		t.Fatal("missing database backup", err)
	}
	if err := s.db.QueryRow(`PRAGMA integrity_check`).Scan(&actual); err != nil || string(actual) != "ok" {
		t.Fatal("integrity check", string(actual), err)
	}
	s.Close()
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	repeated, _ := s.LogContentStats(ctx)
	if repeated != stats {
		t.Fatalf("repeat migration changed data: %+v -> %+v", stats, repeated)
	}
}
