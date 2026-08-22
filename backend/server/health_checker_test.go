package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elysia-api/backend/config"
	"github.com/elysia-api/backend/storage"
)

func newHealthTestServer(t *testing.T) *Server {
	t.Helper()
	store, err := storage.Open(filepath.Join(t.TempDir(), "hc.sqlite3"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	cfg := &config.Config{}
	cfg.HealthCheck = config.HealthCheckConfig{Enabled: true, FailureThreshold: 2}
	return &Server{config: cfg, store: store, skipOutboundValidation: true}
}

func TestProbeEndpoint(t *testing.T) {
	if got := probeEndpoint(storage.Model{BaseURL: "https://api.x.com/v1/", Platform: "openai"}); got != "https://api.x.com/v1/chat/completions" {
		t.Fatalf("openai endpoint wrong: %s", got)
	}
	if got := probeEndpoint(storage.Model{BaseURL: "https://api.anthropic.com", Platform: "claude"}); got != "https://api.anthropic.com/messages" {
		t.Fatalf("claude endpoint wrong: %s", got)
	}
	if got := probeEndpoint(storage.Model{BaseURL: "https://generativelanguage.googleapis.com/", Platform: "gemini", Name: "gemini-2.0-flash"}); got != "https://generativelanguage.googleapis.com/v1beta/models/gemini-2.0-flash:generateContent" {
		t.Fatalf("gemini endpoint wrong: %s", got)
	}
}

// 回归：Gemini 平台探测必须用 generateContent 请求体与 x-goog-api-key 鉴权，
// 否则原生上游必 404/401，模型会被健康检查误禁。
func TestProbeGeminiEndpointAndAuth(t *testing.T) {
	var gotPath, gotAuth, gotBody string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("x-goog-api-key")
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		gotBody = string(buf)
		w.WriteHeader(200)
	}))
	defer ts.Close()

	s := newHealthTestServer(t)
	hc := newHealthChecker(s)
	if !hc.probe(context.Background(), storage.Model{BaseURL: ts.URL, Platform: "gemini", Name: "gemini-2.0-flash", APIKey: "gkey"}, 5) {
		t.Fatalf("gemini probe should succeed against native endpoint")
	}
	if gotPath != "/v1beta/models/gemini-2.0-flash:generateContent" {
		t.Fatalf("gemini probe path wrong: %s", gotPath)
	}
	if gotAuth != "gkey" {
		t.Fatalf("gemini probe must use x-goog-api-key, got %q", gotAuth)
	}
	if !strings.Contains(gotBody, `"contents"`) {
		t.Fatalf("gemini probe body must use contents shape: %s", gotBody)
	}
}

// 回归：单个慢上游不得挤占本轮探测预算，导致后续健康模型被误判失败。
func TestSlowProbeDoesNotStarveSubsequentModels(t *testing.T) {
	s := newHealthTestServer(t)
	hc := newHealthChecker(s)
	ctx := context.Background()

	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
		w.WriteHeader(200)
	}))
	defer slow.Close()
	fast := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	defer fast.Close()

	// 慢上游以 1s 超时探测必然失败；若探测共享整轮 ctx，紧随其后的
	// 健康上游会因 ctx 已耗尽而同样失败。
	if hc.probe(ctx, storage.Model{BaseURL: slow.URL, Platform: "openai", Name: "m"}, 1) {
		t.Fatalf("slow upstream should fail the 1s probe")
	}
	if !hc.probe(ctx, storage.Model{BaseURL: fast.URL, Platform: "openai", Name: "m"}, 1) {
		t.Fatalf("subsequent healthy model must not be starved by earlier slow probe")
	}
}

// 回归：404/405 表示端点不支持探测而非上游故障，不得计入失败。
func TestProbeTreatsEndpointUnsupportedAsHealthy(t *testing.T) {
	s := newHealthTestServer(t)
	hc := newHealthChecker(s)
	for _, status := range []int{404, 405} {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(status) }))
		if !hc.probe(context.Background(), storage.Model{BaseURL: ts.URL, Platform: "openai", Name: "m"}, 5) {
			t.Fatalf("status %d means endpoint unsupported, must not count as failure", status)
		}
		ts.Close()
	}
}

func TestApplyProbeAuth(t *testing.T) {
	req, _ := http.NewRequest(http.MethodPost, "https://x", nil)
	applyProbeAuth(req, storage.Model{APIKey: "k", Platform: "claude"})
	if req.Header.Get("x-api-key") != "k" || req.Header.Get("anthropic-version") == "" {
		t.Fatalf("claude auth headers missing")
	}
	req2, _ := http.NewRequest(http.MethodPost, "https://x", nil)
	applyProbeAuth(req2, storage.Model{APIKey: "k", Platform: "openai"})
	if req2.Header.Get("Authorization") != "Bearer k" {
		t.Fatalf("openai auth header missing")
	}
}

// 自动禁用：连续失败达到阈值后 available 翻转为 false。
func TestHealthCheckerAutoDisableAndRecover(t *testing.T) {
	s := newHealthTestServer(t)
	ctx := context.Background()

	// 先放一个可用模型进库。
	src := storage.ModelSource{ID: "s1", Name: "S1", BaseURL: "https://x", Platform: "openai", Enabled: true}
	if err := s.store.UpsertSource(ctx, src); err != nil {
		t.Fatalf("upsert source: %v", err)
	}
	if err := s.store.ReplaceSourceModels(ctx, src, []storage.Model{{ID: "m1", Name: "m1"}}); err != nil {
		t.Fatalf("replace models: %v", err)
	}

	hc := newHealthChecker(s)
	model := storage.Model{ID: "m1", SourceID: "s1", Name: "m1", Available: true}

	// 第 1 次失败：未达阈值(2)，不禁用。
	if changed := hc.record(model, false, 2); changed {
		t.Fatalf("should not disable before threshold")
	}
	// 第 2 次失败：达到阈值，禁用。
	if changed := hc.record(model, false, 2); !changed {
		t.Fatalf("should disable at threshold")
	}
	models, _ := s.store.ListModels(ctx)
	if len(models) != 1 || models[0].Available {
		t.Fatalf("model should be disabled, got %+v", models)
	}

	// 探测恢复：传入 Available=false 的模型，成功一次 → 重新启用。
	disabled := storage.Model{ID: "m1", SourceID: "s1", Name: "m1", Available: false}
	if changed := hc.record(disabled, true, 2); !changed {
		t.Fatalf("should re-enable on recovery")
	}
	models, _ = s.store.ListModels(ctx)
	if !models[0].Available {
		t.Fatalf("model should be re-enabled, got %+v", models[0])
	}
}

// 探测真实 HTTP：2xx 健康，5xx 不健康，429 视为健康。
func TestHealthCheckerProbe(t *testing.T) {
	s := newHealthTestServer(t)
	hc := newHealthChecker(s)
	ctx := context.Background()

	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	defer good.Close()
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) }))
	defer bad.Close()
	limited := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(429) }))
	defer limited.Close()

	if !hc.probe(ctx, storage.Model{BaseURL: good.URL, Platform: "openai", Name: "m"}, 5) {
		t.Fatalf("2xx should be healthy")
	}
	if hc.probe(ctx, storage.Model{BaseURL: bad.URL, Platform: "openai", Name: "m"}, 5) {
		t.Fatalf("5xx should be unhealthy")
	}
	if !hc.probe(ctx, storage.Model{BaseURL: limited.URL, Platform: "openai", Name: "m"}, 5) {
		t.Fatalf("429 should be treated as healthy (upstream alive)")
	}
}

// 禁用状态：未启用时 start() 不应启动 goroutine（done 立即关闭）。
func TestHealthCheckerDisabledByDefault(t *testing.T) {
	cfg := &config.Config{} // HealthCheck.Enabled = false
	s := &Server{config: cfg}
	hc := newHealthChecker(s)
	hc.start()
	<-hc.done // 应立即返回，不阻塞
}
