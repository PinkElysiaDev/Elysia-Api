package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elysia-api/backend/storage"
	"github.com/gin-gonic/gin"
)

// 回归：模型 ID 常含 "/"（如 org/model），旧实现把 modelId 放在路径段，
// 前端编码出的 %2F 会在 Gin 路由匹配前被解码拆段导致 404。现改走 query
// 参数，这里用真实路由（而非 handler 直调）覆盖编码/解码全链路。
func TestAdminModelEndpointsSupportSlashInModelID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store, err := storage.Open(filepath.Join(t.TempDir(), "model-update.sqlite3"))
	if err != nil {
		t.Fatalf("storage.Open() error = %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	source := storage.ModelSource{ID: "miku", Name: "miku", Enabled: true, AutoFetchModels: false}
	if err := store.UpsertSource(ctx, source); err != nil {
		t.Fatalf("UpsertSource: %v", err)
	}
	model := storage.Model{
		ID: "org/model-x", Name: "model-x",
		BaseURL: "https://api.example.com", Platform: "openai",
		Enabled: true, Available: true, Origin: "manual",
	}
	if err := store.ReplaceSourceModels(ctx, source, []storage.Model{model}); err != nil {
		t.Fatalf("ReplaceSourceModels: %v", err)
	}

	s := &Server{store: store}
	router := gin.New()
	s.setupAdminRoutes(router.Group("/api/admin"))

	findModel := func(t *testing.T) storage.Model {
		t.Helper()
		models, err := store.ListModels(ctx)
		if err != nil {
			t.Fatalf("ListModels: %v", err)
		}
		for _, m := range models {
			if m.ID == "org/model-x" {
				return m
			}
		}
		t.Fatalf("model org/model-x not found")
		return storage.Model{}
	}

	// PATCH 启停：含 "/" 的 modelId 经 query 传递应正常更新。
	req := httptest.NewRequest(http.MethodPatch,
		"/api/admin/models/miku?modelId="+url.QueryEscape("org/model-x"),
		strings.NewReader(`{"enabled":false}`))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"updated":true`) {
		t.Fatalf("PATCH status=%d body=%s", rec.Code, rec.Body.String())
	}
	if got := findModel(t); got.Enabled {
		t.Fatalf("model still enabled after PATCH: %+v", got)
	}

	// DELETE：同样含 "/" 的 modelId。
	req = httptest.NewRequest(http.MethodDelete,
		"/api/admin/models/miku?modelId="+url.QueryEscape("org/model-x"), nil)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"deleted":true`) {
		t.Fatalf("DELETE status=%d body=%s", rec.Code, rec.Body.String())
	}

	// 缺 modelId 参数应显式报 400，而非 404。
	req = httptest.NewRequest(http.MethodPatch, "/api/admin/models/miku", strings.NewReader(`{}`))
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), `"code":"model_id_required"`) {
		t.Fatalf("missing modelId status=%d body=%s", rec.Code, rec.Body.String())
	}
}
