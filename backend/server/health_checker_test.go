package server

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elysia-api/backend/config"
	"github.com/elysia-api/backend/protocol"
	"github.com/elysia-api/backend/storage"
)

func newHealthTestServer(t *testing.T) *Server {
	t.Helper()
	server := newTestServer(t, nil)
	server.config.HealthCheck = config.HealthCheckConfig{Enabled: true, FailureThreshold: 2}
	return server
}

func bindHealthModel(t *testing.T, server *Server, platform, endpoint string) storage.Model {
	t.Helper()
	source := storage.ModelSource{ID: "probe-source", Name: "probe-source", Platform: platform, BaseURL: endpoint, APIKey: "probe-key", Enabled: true}
	if err := server.saveSource(t.Context(), source); err != nil {
		t.Fatal(err)
	}
	return storage.Model{ID: "model", Name: "model", SourceID: source.ID, Platform: platform, BaseURL: endpoint, APIKey: source.APIKey}
}

func TestHealthProbeUsesVerifiedWireAndCredentials(t *testing.T) {
	cases := []struct{ id, path, auth, key, request, response string }{
		{"chat-completions-api", "/chat/completions", "Authorization", "Bearer probe-key", `"messages"`, `{"choices":[{"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}]}`},
		{"responses-api", "/responses", "Authorization", "Bearer probe-key", `"input"`, `{"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hi"}]}]}`},
		{"anthropic-api", "/v1/messages", "x-api-key", "probe-key", `"messages"`, `{"role":"assistant","content":[{"type":"text","text":"hi"}],"stop_reason":"end_turn"}`},
		{"gemini-api", "/v1beta/models/model:generateContent", "x-goog-api-key", "probe-key", `"contents"`, `{"candidates":[{"content":{"role":"model","parts":[{"text":"hi"}]},"finishReason":"STOP"}]}`},
	}
	for _, tc := range cases {
		t.Run(tc.id, func(t *testing.T) {
			server := newHealthTestServer(t)
			definition := presetDefinition(t, tc.id)
			definition.ID = "User-" + tc.id
			activateGatewayDefinition(t, server, definition)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
				}
				if r.URL.Path != tc.path || r.Header.Get(tc.auth) != tc.key || !strings.Contains(string(body), tc.request) {
					t.Errorf("unexpected probe: path=%s headers=%v body=%s", r.URL.Path, r.Header, body)
				}
				if tc.id == "anthropic-api" && r.Header.Get("anthropic-version") == "" {
					t.Error("missing wire version header")
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, tc.response)
			}))
			defer upstream.Close()
			model := bindHealthModel(t, server, "custom:"+definition.ID, upstream.URL)
			if result := newHealthChecker(server).probe(t.Context(), model, 5); result != probeHealthy {
				t.Fatalf("valid probe result = %v", result)
			}
		})
	}
}

func TestHealthProbeRequiresSuccessfulContract(t *testing.T) {
	server := newHealthTestServer(t)
	checker := newHealthChecker(server)
	for _, tc := range []struct {
		name     string
		status   int
		body     string
		expected healthProbeResult
	}{
		{"valid", 200, okChatCompletionBody(t), probeHealthy},
		{"empty", 200, "", probeUnhealthy},
		{"business-error", 200, `{"error":{"message":"quota exhausted"}}`, probeUnhealthy},
		{"server-error", 500, `{"error":"failed"}`, probeUnhealthy},
		{"wrong-path", 404, "", probeUnhealthy},
		{"wrong-method", 405, "", probeUnhealthy},
		{"rate-limit", 429, "", probeUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer upstream.Close()
			model := bindHealthModel(t, server, "openai", upstream.URL)
			if result := checker.probe(t.Context(), model, 5); result != tc.expected {
				t.Fatalf("probe = %v, want %v", result, tc.expected)
			}
		})
	}
	if result := checker.probe(t.Context(), storage.Model{ID: "unbound"}, 5); result != probeUnavailable {
		t.Fatal("unbound model must not be disabled by a guessed probe")
	}
}

func TestSlowProbeDoesNotStarveSubsequentModels(t *testing.T) {
	server := newHealthTestServer(t)
	checker := newHealthChecker(server)
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.Copy(io.Discard, r.Body); <-r.Context().Done() }))
	defer slow.Close()
	fast := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, okChatCompletionBody(t)) }))
	defer fast.Close()
	if result := checker.probe(t.Context(), bindHealthModel(t, server, "openai", slow.URL), 1); result != probeUnhealthy {
		t.Fatal("slow probe must fail")
	}
	if result := checker.probe(t.Context(), bindHealthModel(t, server, "openai", fast.URL), 1); result != probeHealthy {
		t.Fatal("fast probe must have its own timeout")
	}
}

func TestHealthProbeUnavailablePreservesFailureEvidence(t *testing.T) {
	server := newHealthTestServer(t)
	checker := newHealthChecker(server)
	model := storage.Model{ID: "missing", SourceID: "missing"}
	checker.failures[probeKey(model.ID, model.SourceID)] = 1
	if checker.probe(t.Context(), model, 1) != probeUnavailable {
		t.Fatal("missing contract must be neutral")
	}
	if checker.failures[probeKey(model.ID, model.SourceID)] != 1 {
		t.Fatal("unobserved health must not reset failures")
	}
	checker.pruneStaleFailureKeys(nil)
	if len(checker.failures) != 0 {
		t.Fatal("removed models must not accumulate failure keys")
	}
}

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
	if changed := hc.recordProbeResult(model, false, 2); changed {
		t.Fatalf("should not disable before threshold")
	}
	// 第 2 次失败：达到阈值，禁用。
	if changed := hc.recordProbeResult(model, false, 2); !changed {
		t.Fatalf("should disable at threshold")
	}
	models, _ := s.store.ListModels(ctx)
	if len(models) != 1 || models[0].Available {
		t.Fatalf("model should be disabled, got %+v", models)
	}

	// 探测恢复：传入 Available=false 的模型，成功一次 → 重新启用。
	disabled := storage.Model{ID: "m1", SourceID: "s1", Name: "m1", Available: false}
	if changed := hc.recordProbeResult(disabled, true, 2); !changed {
		t.Fatalf("should re-enable on recovery")
	}
	models, _ = s.store.ListModels(ctx)
	if !models[0].Available {
		t.Fatalf("model should be re-enabled, got %+v", models[0])
	}
}

func TestHealthCheckerDisabledByDefault(t *testing.T) {
	cfg := &config.Config{} // HealthCheck.Enabled = false
	s := &Server{config: cfg}
	hc := newHealthChecker(s)
	hc.start()
	<-hc.done // 应立即返回，不阻塞
}

func TestProbeCredentialSelectionHonorsModelPermissions(t *testing.T) {
	server := newHealthTestServer(t)
	model := storage.Model{ID: "m", SourceID: "s", APIKey: "stale", BaseURL: "https://stale.invalid", Available: false}
	sources := []storage.ModelSource{{ID: "s", BaseURL: "https://current.invalid", APIKeys: []storage.SourceAPIKey{
		{Value: "wrong-model", AllowedModels: []string{"other"}},
		{Value: "permitted", AllowedModels: []string{"m"}},
	}}}
	ref, ok := server.resolveModelSource(model, collectSourceKeys(sources))
	if !ok || ref.APIKey != "permitted" || ref.BaseURL != sources[0].BaseURL {
		t.Fatalf("probe used stale or unauthorized identity: %+v", ref)
	}
	sources[0].APIKeys = sources[0].APIKeys[:1]
	if _, ok := server.resolveModelSource(model, collectSourceKeys(sources)); ok {
		t.Fatal("probe bypassed model permissions")
	}
}

func TestHealthCheckerShutdownCancelsActiveProbe(t *testing.T) {
	s := newHealthTestServer(t)
	s.config.HealthCheck.TimeoutSeconds = 30
	s.config.HealthCheck.FailureThreshold = 1
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		started <- struct{}{}
		select {
		case <-r.Context().Done():
		case <-release:
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer upstream.Close()

	// feat 起探测要求可解析凭据（resolveModelSource 无 Key 即跳过），
	// 补一个 Key 使探测真正发出，测试意图（关停取消在途探测）不变。
	source := storage.ModelSource{ID: "slow", Name: "slow", BaseURL: upstream.URL, Platform: "openai", Enabled: true, APIKeys: []storage.SourceAPIKey{{Value: "probe-key"}}}
	if err := s.store.UpsertSource(context.Background(), source); err != nil {
		t.Fatal(err)
	}
	if err := s.store.ReplaceSourceModels(context.Background(), source, []storage.Model{{ID: "first", Name: "first"}, {ID: "second", Name: "second"}}); err != nil {
		t.Fatal(err)
	}
	// feat 起探测走协议引擎，要求源有已验证的协议绑定；迁移在测试服务器
	// 构造时已完成，新建的源直接按生产同构方式补存绑定（见 agent_protocol_v2_test）。
	service, err := s.protocolService()
	if err != nil {
		t.Fatal(err)
	}
	compiled, ok := service.Pin("chat-completions-api")
	if !ok {
		t.Fatal("chat-completions-api preset not active")
	}
	if err := s.store.SaveProtocolBinding(context.Background(), storage.ProtocolBinding{Kind: "source", SourceID: source.ID, Binding: protocol.Binding{ProtocolID: compiled.Identity().DefinitionID, RevisionHash: compiled.Hash(), Capabilities: compiled.Definition().Capabilities, Transports: []protocol.Transport{protocol.HTTPJSON}}}); err != nil {
		t.Fatal(err)
	}
	hc := newHealthChecker(s)
	hc.start()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		close(release)
		hc.shutdown()
		t.Fatal("health checker did not start its first probe")
	}

	done := make(chan struct{})
	go func() { hc.shutdown(); close(done) }()
	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Error("shutdown must cancel the active probe instead of waiting for its 30s timeout")
	}
	close(release)
	<-done
	if got := calls.Load(); got != 1 {
		t.Errorf("shutdown must not probe subsequent models: got %d requests", got)
	}
	models, err := s.store.ListModels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, model := range models {
		if !model.Available {
			t.Errorf("shutdown cancellation must not disable model %s", model.ID)
		}
	}
}
