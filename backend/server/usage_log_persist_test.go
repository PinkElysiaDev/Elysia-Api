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

// newPersistTestServer 带 store + 可写 config 的 Server（免登录直调 handler）。
func newPersistTestServer(t *testing.T) (*Server, *config.Config, *storage.Store) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	store, err := storage.Open(filepath.Join(t.TempDir(), "persist.sqlite3"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	cfg := &config.Config{}
	cfg.SetDatabasePath(filepath.Join(t.TempDir(), "persist-config.sqlite3"))
	return &Server{store: store, config: cfg}, cfg, store
}

func storedRecordJSON(t *testing.T, store *storage.Store, id string) string {
	t.Helper()
	payload, found, err := store.GetUsageRecordJSON(context.Background(), id)
	if err != nil || !found {
		t.Fatalf("record %s not found (err=%v)", id, err)
	}
	return string(payload)
}

func TestBodyOnErrorOnlyDropsSuccessBodies(t *testing.T) {
	s, cfg, store := newPersistTestServer(t)
	on := true
	cfg.SetUsageLogConfig(config.UsageLogConfig{BodyOnErrorOnly: &on})

	success := newExternalizeRecord("onerr-success")
	success.StatusCode = 200
	success.IncomingBody = usageBody{Content: `{"big":"payload"}`}
	if err := s.saveUsageRecordToStore(success); err != nil {
		t.Fatalf("save: %v", err)
	}
	stored := storedRecordJSON(t, store, "onerr-success")
	if strings.Contains(stored, "big") {
		t.Fatalf("success body must be dropped under bodyOnErrorOnly, got %s", stored)
	}
	if success.IncomingBody.Content != "" {
		t.Fatal("in-memory record bodies must be cleared before marshal")
	}
}

func TestBodyOnErrorOnlyKeepsFailedBodiesAndWritesAssets(t *testing.T) {
	s, cfg, store := newPersistTestServer(t)
	on := true
	cfg.SetUsageLogConfig(config.UsageLogConfig{BodyOnErrorOnly: &on})

	failed := newExternalizeRecord("onerr-failed")
	failed.StatusCode = 400
	failed.Error = "boom"
	failed.ErrorKind = ErrorKindConversion
	payload := strings.Repeat("A", 600)
	failed.IncomingBody = failed.sanitizeBody([]byte(`{"url":"data:image/png;base64,` + payload + `"}`))
	if failed.assets.count() != 1 {
		t.Fatal("precondition: image must be registered at capture time")
	}
	if err := s.saveUsageRecordToStore(failed); err != nil {
		t.Fatalf("save: %v", err)
	}

	stored := storedRecordJSON(t, store, "onerr-failed")
	if !strings.Contains(stored, AssetPlaceholderPrefix) {
		t.Fatalf("failed record must keep body with placeholder, got %s", stored)
	}
	if !strings.Contains(stored, `"errorKind":"conversion"`) {
		t.Fatalf("errorKind must be persisted, got %s", stored)
	}
	// 资产已写盘。
	item := failed.assets.items[0]
	path := filepath.Join(s.usageAssetsRoot(), "onerr-failed", item.Hash+"."+item.Ext)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("asset file must be written for kept body: %v", err)
	}
	if !containsWarning(failed, "externalized") {
		t.Fatalf("RequestWarnings must note externalization: %v", failed.RequestWarnings)
	}
}

func containsWarning(record *usageRecord, substr string) bool {
	for _, w := range record.RequestWarnings {
		if strings.Contains(w, substr) {
			return true
		}
	}
	return false
}

func TestPersistDisabledDropsRecord(t *testing.T) {
	s, cfg, _ := newPersistTestServer(t)
	off := false
	cfg.SetUsageLogConfig(config.UsageLogConfig{PersistEnabled: &off})

	called := false
	// recordUsage 在入口即返回，不触达 store。
	s.recordUsage(&usageRecord{RequestID: "dropped", StartedAt: time.Now(), EndedAt: time.Now()})
	deadline := time.Now().Add(300 * time.Millisecond)
	for time.Now().Before(deadline) {
		if _, items, _ := s.store.QueryUsageLogs(context.Background(), storage.UsageQuery{Limit: 10}); len(items) == 0 {
			called = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !called {
		t.Fatal("persistEnabled=false must stop recording usage")
	}
}

// 回归：/v1/chat/completions 请求转换失败必须落 usage 记录（bodyOnErrorOnly
// 模式下这是唯一保留请求体的排查样本），而不是像旧版那样静默 400。
func TestConversionFailureIsRecorded(t *testing.T) {
	store, err := storage.Open(filepath.Join(t.TempDir(), "convert-fail.sqlite3"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer store.Close()
	cfg := &config.Config{}
	cfg.SetDatabasePath(filepath.Join(t.TempDir(), "convert-fail-config.sqlite3"))
	s := &Server{store: store, config: cfg}
	s.startUsageWriter()
	defer s.stopUsageWriter()

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{invalid json`))
	start := time.Now()
	record := s.initUsageRecord(c, start, []byte(`{invalid json`), "openai")

	// 与 chatCompletions 内一致的失败路径。
	s.failRequestKind(c, record, start, http.StatusBadRequest, ErrorKindConversion, "Failed to convert request to Maheshvara: bad json")

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d", rec.Code)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		payload, found, err := store.GetUsageRecordJSON(context.Background(), record.RequestID)
		if err == nil && found {
			if !strings.Contains(string(payload), `"errorKind":"conversion"`) {
				t.Fatalf("errorKind=conversion must be persisted: %s", payload)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("conversion failure record must be persisted")
}

// 管理端资产路由：鉴权由 admin 组中间件负责，这里覆盖文件名校验与正常下发。
func TestAdminUsageAssetServesFileAndRejectsTraversal(t *testing.T) {
	s, cfg, store := newPersistTestServer(t)

	// 准备一条记录 + 一个真实资产文件。
	record := newExternalizeRecord("req_asset")
	payload := strings.Repeat("B", 600)
	record.sanitizeBody([]byte(`{"url":"data:image/png;base64,` + payload + `"}`))
	if err := s.saveUsageRecordToStore(record); err != nil {
		t.Fatalf("save: %v", err)
	}

	router := gin.New()
	s.setupAdminRoutes(router.Group("/api/admin"))

	// 正常获取：占位符里的文件名可下发，Content-Type 按 ext。
	fileName := record.assets.items[0].Hash + "." + record.assets.items[0].Ext
	req := httptest.NewRequest(http.MethodGet, "/api/admin/usage/assets/req_asset/"+fileName, nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("asset status=%d body=%s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); ct != "image/png" {
		t.Fatalf("content-type=%s", ct)
	}

	// 穿越与非法名一律 400/404。
	for _, path := range []string{
		"/api/admin/usage/assets/req_asset/..%2F..%2Fetc%2Fpasswd",
		"/api/admin/usage/assets/req_asset/short.png",
		"/api/admin/usage/assets/req_a.b/0123456789abcdef.png",
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code == http.StatusOK {
			t.Fatalf("path %s must not return 200", path)
		}
	}
	_ = cfg
	_ = store
}
