package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elysia-api/backend/config"
	"github.com/elysia-api/backend/storage"
	"github.com/gin-gonic/gin"
)

// 回归（核心修复）：同一图片出现在两个请求里，磁盘只存一份；两个记录的
// 占位符都能取到文件；删其中一个记录文件保留，两个都删文件才删除。
// 旧布局 usage-assets/<requestId>/ 只在单请求内去重，多轮对话重发历史
// 图片会线性放大存储。
func TestAssetDedupAcrossRequests(t *testing.T) {
	gin.SetMode(gin.TestMode)
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
	refs, err := store.ReferencedAssetFiles(ctx)
	if err != nil || !refs[name] {
		t.Fatalf("file must be referenced: %v %v", refs, err)
	}

	// 删一条记录：文件保留（另一条还在引用）。
	r := newUsageRetention(s)
	if n := r.releaseAssets(ctx, root, []string{"req-one"}); n != 0 {
		t.Fatalf("file still referenced by req-two must survive, removed %d", n)
	}
	if _, err := os.Stat(filepath.Join(root, name)); err != nil {
		t.Fatalf("file must survive partial deletion: %v", err)
	}
	// 删第二条：零引用 → 文件删除。
	if n := r.releaseAssets(ctx, root, []string{"req-two"}); n != 1 {
		t.Fatalf("last reference removal must delete the file, got %d", n)
	}
	if _, err := os.Stat(filepath.Join(root, name)); !os.IsNotExist(err) {
		t.Fatal("file with zero references must be removed")
	}
}

// 迁移：旧布局（请求子目录）→ 扁平 + 引用从 record_json 重建；
// 记录已删除的遗留文件不误删（交孤儿清扫宽限回收）。
func TestMigrateUsageAssetsLayout(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store, err := storage.Open(filepath.Join(t.TempDir(), "migrate.sqlite3"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer store.Close()
	cfg := &config.Config{}
	cfg.SetDatabasePath(filepath.Join(t.TempDir(), "migrate-config.sqlite3"))
	s := &Server{store: store, config: cfg}
	ctx := context.Background()

	// 记录 A 引用 abc.png；记录 B 已删（其目录里的 def.png 无引用）。
	if err := store.SaveUsageRecordJSON(ctx,
		[]byte(`{"requestId":"req-a","incomingBody":{"content":"__ELYSIA_ASSET__:req-a/0123456789abcdef.png"}}`),
		storage.UsageLogItem{RequestID: "req-a"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	root := s.usageAssetsRoot()
	oldA := filepath.Join(root, "req-a")
	oldB := filepath.Join(root, "req-b")
	for dir, file := range map[string]string{oldA: "0123456789abcdef.png", oldB: "1111111111111111.png"} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, file), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	s.migrateUsageAssetsLayout()

	// 子目录消失，文件上移到根。
	if _, err := os.Stat(oldA); !os.IsNotExist(err) {
		t.Fatal("legacy request dirs must be removed")
	}
	for _, file := range []string{"0123456789abcdef.png", "1111111111111111.png"} {
		if _, err := os.Stat(filepath.Join(root, file)); err != nil {
			t.Fatalf("flattened file %s must exist: %v", file, err)
		}
	}
	// 引用重建：仅 req-a 的引用存在。
	refs, err := store.ReferencedAssetFiles(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !refs["0123456789abcdef.png"] {
		t.Fatal("referenced file must have its ref rebuilt")
	}
	if refs["1111111111111111.png"] {
		t.Fatal("file of deleted record must stay unreferenced (orphan sweep handles it)")
	}

	// 幂等：再次运行无子目录可迁移、引用不重复。
	s.migrateUsageAssetsLayout()
	// 端点按扁平路径可取。
	router := gin.New()
	s.setupAdminRoutes(router.Group("/api/admin"))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/admin/usage/assets/req-a/0123456789abcdef.png", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("asset endpoint after migration: %d", w.Code)
	}
}
