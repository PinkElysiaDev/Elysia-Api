package server

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elysia-api/backend/config"
	"github.com/elysia-api/backend/storage"
)

// 回归（核心修复）：同一图片出现在两个请求里，磁盘只存一份；两个记录的
// 占位符都能取到文件；删其中一个记录文件保留，两个都删文件才删除。
// 旧布局 usage-assets/<requestId>/ 只在单请求内去重，多轮对话重发历史
// 图片会线性放大存储。
// referencedAssetFiles 汇总仍被引用的资产文件集合(测试断言用,替代已删的
// Store.ReferencedAssetFiles——生产清扫走 UsageAssets 全量形态)。
func referencedAssetFiles(t *testing.T, store *storage.Store) map[string]bool {
	t.Helper()
	assets, err := store.UsageAssets(t.Context())
	if err != nil {
		t.Fatalf("usage assets: %v", err)
	}
	files := map[string]bool{}
	for _, asset := range assets {
		if asset.Referenced {
			files[asset.File] = true
		}
	}
	return files
}

func TestAssetDedupAcrossRequests(t *testing.T) {
	store, err := storage.Open(filepath.Join(t.TempDir(), "dedup.sqlite3"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer store.Close()
	cfg := &config.Config{}
	cfg.SetDatabasePath(filepath.Join(t.TempDir(), "dedup-config.sqlite3"))
	s := &Server{store: store, config: cfg}
	ctx := context.Background()

	payload := strings.Repeat("Q", 600)
	persist := func(requestID string) *usageRecord {
		record := newExternalizeRecord(requestID)
		record.StatusCode = 200
		record.IncomingBody = record.sanitizeBody([]byte(`{"url":"data:image/png;base64,` + payload + `"}`))
		if record.assets.count() != 1 {
			t.Fatalf("%s: image must be registered", requestID)
		}
		if err := s.saveUsageRecordToStore(record); err != nil {
			t.Fatalf("save %s: %v", requestID, err)
		}
		return record
	}
	first := persist("req-one")
	second := persist("req-two")

	// 两请求占位符指向同一文件名（内容哈希）。
	name := first.assets.items[0].Hash + "." + first.assets.items[0].Ext
	if got := second.assets.items[0].Hash + "." + second.assets.items[0].Ext; got != name {
		t.Fatalf("same content must hash to same file: %s vs %s", name, got)
	}
	root := s.usageAssetsRoot()
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 1 || entries[0].Name() != name {
		t.Fatalf("exactly one flat file expected, got %v (err=%v)", entries, err)
	}

	// 引用计数：两条记录各持一个引用。
	refs := referencedAssetFiles(t, store)
	if !refs[name] {
		t.Fatalf("file must be referenced: %v %v", refs, err)
	}

	// 删一条记录：文件保留（另一条还在引用）。
	r := newUsageRetention(s)
	if _, err := store.PruneLogBatch(ctx, false, storage.LogPrunePolicy{MaxRecords: 1}); err != nil {
		t.Fatal(err)
	}
	if n, err := r.sweepAssets(ctx); err != nil || n != 0 {
		t.Fatalf("file still referenced by req-two must survive, removed %d", n)
	}
	if _, err := os.Stat(filepath.Join(root, name)); err != nil {
		t.Fatalf("file must survive partial deletion: %v", err)
	}
	// 删第二条：零引用 → 文件删除。
	if _, err := store.PruneLogBatch(ctx, false, storage.LogPrunePolicy{CutoffMS: time.Now().Add(time.Hour).UnixMilli()}); err != nil {
		t.Fatal(err)
	}
	if n, err := r.sweepAssets(ctx); err != nil || n != 1 {
		t.Fatalf("last reference removal must delete the file, got %d", n)
	}
	if _, err := os.Stat(filepath.Join(root, name)); !os.IsNotExist(err) {
		t.Fatal("file with zero references must be removed")
	}
}
