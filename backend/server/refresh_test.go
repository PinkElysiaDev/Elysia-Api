package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/elysia-api/backend/config"
	"github.com/elysia-api/backend/storage"
)

func newRefreshTestServer(t *testing.T) *Server {
	t.Helper()
	store, err := storage.Open(filepath.Join(t.TempDir(), "refresh.sqlite3"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return &Server{config: &config.Config{}, store: store}
}

// #1: Claude 源走 /v1/models 拉取（OpenAI 风格响应），x-api-key 鉴权命中。
func TestFetchClaudeModelsViaV1Models(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			w.WriteHeader(404)
			return
		}
		gotAuth = r.Header.Get("x-api-key")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"claude-3-5-sonnet"},{"id":"claude-3-opus"}]}`))
	}))
	defer srv.Close()

	s := newRefreshTestServer(t)
	models, err := s.fetchClaudeModels(context.Background(), storage.ModelSource{
		Name: "claude-relay", BaseURL: srv.URL, Platform: "claude", APIKey: "sk-ant-xxx", Enabled: true,
	})
	if err != nil {
		t.Fatalf("fetchClaudeModels error: %v", err)
	}
	if len(models) != 2 {
		t.Fatalf("expected 2 models, got %d", len(models))
	}
	if gotAuth != "sk-ant-xxx" {
		t.Fatalf("expected x-api-key auth, got %q", gotAuth)
	}
	if models[0].Platform != "claude" {
		t.Fatalf("expected platform claude, got %q", models[0].Platform)
	}
}

// #1: Claude 官方鉴权失败时回退 Bearer（模拟只认 Bearer 的中转站）。
func TestFetchClaudeModelsBearerFallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 只接受 Bearer，x-api-key 一律 401。
		if r.Header.Get("Authorization") != "Bearer sk-relay" {
			w.WriteHeader(401)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"claude-3-5-sonnet"}]}`))
	}))
	defer srv.Close()

	s := newRefreshTestServer(t)
	models, err := s.fetchClaudeModels(context.Background(), storage.ModelSource{
		Name: "relay", BaseURL: srv.URL, Platform: "claude", APIKey: "sk-relay", Enabled: true,
	})
	if err != nil {
		t.Fatalf("expected bearer fallback to succeed, got: %v", err)
	}
	if len(models) != 1 {
		t.Fatalf("expected 1 model via fallback, got %d", len(models))
	}
}

// Gemini 源走 /v1beta/models 拉取（与 relay 适配器 baseUrl 不含 /v1beta 的约定一致），
// x-goog-api-key 鉴权，解析 name 前缀与 inputTokenLimit。
func TestFetchGeminiModelsViaV1BetaModels(t *testing.T) {
	var gotAuth string
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("x-goog-api-key")
		if r.URL.Path != "/v1beta/models" {
			w.WriteHeader(404)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"models":[{"name":"models/gemini-1.5-flash","inputTokenLimit":1048576,"outputTokenLimit":8192}]}`))
	}))
	defer srv.Close()

	s := newRefreshTestServer(t)
	models, err := s.fetchGeminiModels(context.Background(), storage.ModelSource{
		Name: "gemini-relay", BaseURL: srv.URL, Platform: "gemini", APIKey: "gem-key", Enabled: true,
	})
	if err != nil {
		t.Fatalf("fetchGeminiModels error: %v", err)
	}
	if gotPath != "/v1beta/models" {
		t.Fatalf("expected request to /v1beta/models, got %q", gotPath)
	}
	if gotAuth != "gem-key" {
		t.Fatalf("expected x-goog-api-key auth, got %q", gotAuth)
	}
	if len(models) != 1 {
		t.Fatalf("expected 1 model, got %d", len(models))
	}
	if models[0].Name != "gemini-1.5-flash" {
		t.Fatalf("expected name gemini-1.5-flash (models/ prefix stripped), got %q", models[0].Name)
	}
	if models[0].Platform != "gemini" {
		t.Fatalf("expected platform gemini, got %q", models[0].Platform)
	}
	if models[0].MaxTokens != 1048576 {
		t.Fatalf("expected MaxTokens from inputTokenLimit, got %d", models[0].MaxTokens)
	}
}

// #1: 全量刷新时单源失败不阻塞其他源，错误被收集返回。
func TestRefreshAllSourcesFaultTolerant(t *testing.T) {
	ctx := context.Background()
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"gpt-4o"},{"id":"gpt-4o-mini"}]}`))
	}))
	defer good.Close()
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer bad.Close()

	s := newRefreshTestServer(t)
	// 一个能拉取的 openai 源 + 一个总是 500 的源。
	if err := s.store.UpsertSource(ctx, storage.ModelSource{ID: "good", Name: "good", BaseURL: good.URL, Platform: "openai", Enabled: true, AutoFetchModels: true}); err != nil {
		t.Fatalf("upsert good: %v", err)
	}
	if err := s.store.UpsertSource(ctx, storage.ModelSource{ID: "bad", Name: "bad", BaseURL: bad.URL, Platform: "openai", Enabled: true, AutoFetchModels: true}); err != nil {
		t.Fatalf("upsert bad: %v", err)
	}

	count, failures, err := s.refreshAllSources(ctx)
	if err != nil {
		t.Fatalf("refreshAllSources should not hard-error: %v", err)
	}
	if count != 2 {
		t.Fatalf("expected 2 models from good source, got %d", count)
	}
	if len(failures) != 1 || failures[0].SourceID != "bad" {
		t.Fatalf("expected exactly the bad source to fail, got %+v", failures)
	}
}

// 回归：上游 200 但返回异常结构（解析出 0 个模型）时，不得清空该源已有的
// 模型列表——ReplaceSourceModels 是先 DELETE 全部再插入，空列表会清库。
func TestRefreshEmptyModelListKeepsExistingModels(t *testing.T) {
	ctx := context.Background()
	// 返回 200 + 无 data 字段的中转站（异常但常见）。
	empty := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"error":{"message":"upstream degraded"}}`))
	}))
	defer empty.Close()

	s := newRefreshTestServer(t)
	source := storage.ModelSource{ID: "s-empty", Name: "empty", BaseURL: empty.URL, Platform: "openai", Enabled: true, AutoFetchModels: true}
	if err := s.store.UpsertSource(ctx, source); err != nil {
		t.Fatalf("upsert source: %v", err)
	}
	if err := s.store.ReplaceSourceModels(ctx, source, []storage.Model{{ID: "keep-me", Name: "keep-me", Available: true}}); err != nil {
		t.Fatalf("seed models: %v", err)
	}

	if _, err := s.refreshSource(ctx, "s-empty"); err == nil {
		t.Fatal("refresh with an empty model list must surface an error")
	}
	models, err := s.store.ListModels(ctx)
	if err != nil {
		t.Fatalf("list models: %v", err)
	}
	if len(models) != 1 || models[0].Name != "keep-me" {
		t.Fatalf("existing models must be kept on empty refresh, got %+v", models)
	}
}
