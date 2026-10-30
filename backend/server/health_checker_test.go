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
	"github.com/elysia-api/backend/internal/routing"
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
	platform = fixturePlatform(platform)
	source := storage.ModelSource{ID: "probe-source", Name: "probe-source", Platform: platform, BaseURL: endpoint, APIKey: "probe-key", Enabled: true}
	if err := server.saveSource(t.Context(), source); err != nil {
		t.Fatal(err)
	}
	return storage.Model{ID: "model", Name: "model", SourceID: source.ID, Platform: platform, BaseURL: endpoint, APIKey: source.APIKey}
}

func TestHealthProbeUsesVerifiedWireAndCredentials(t *testing.T) {
	cases := []struct{ id, path, auth, key, request, response string }{
		{"openai-chat-completions", "/chat/completions", "Authorization", "Bearer probe-key", `"messages"`, `{"choices":[{"message":{"role":"assistant","content":"hi"},"finish_reason":"stop","index":0}],"id":"r1","model":"m","created":1,"object":"chat.completion"}`},
		{"openai-responses", "/responses", "Authorization", "Bearer probe-key", `"input"`, `{"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hi","annotations":[]}],"id":"item_0","status":"completed"}],"id":"r1","model":"m","object":"response","created_at":1}`},
		{"anthropic-messages", "/v1/messages", "x-api-key", "probe-key", `"messages"`, `{"role":"assistant","content":[{"type":"text","text":"hi"}],"stop_reason":"end_turn"}`},
		{"google-generate-content", "/v1beta/models/model:generateContent", "x-goog-api-key", "probe-key", `"contents"`, `{"candidates":[{"content":{"role":"model","parts":[{"text":"hi"}]},"finishReason":"STOP"}]}`},
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
				if tc.id == "anthropic-messages" && r.Header.Get("anthropic-version") == "" {
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

func TestHealthProbeUsesTargetCoreVersion(t *testing.T) {
	server := newHealthTestServer(t)
	document := sourceStreamPackage(t)
	document.Spec.Stream = nil
	for direction := range document.Mapping.Directions {
		if direction != protocol.EncodeRequest && direction != protocol.DecodeResponse {
			delete(document.Mapping.Directions, direction)
			delete(document.Spec.WireContracts, direction)
		}
	}
	read := func(path string) protocol.Expression {
		return protocol.Expression{Op: "read", From: "input", Path: path}
	}
	requestMapping := document.Mapping.Directions[protocol.EncodeRequest]
	for _, field := range []struct{ name, path string }{
		{"prompt", "/content/0/children/0/payload"},
		{"limit", "/parameters/max_output_tokens"},
		{"stream", "/parameters/stream"},
	} {
		requestMapping.Program.Fields[field.name] = read(field.path)
		requestMapping.Effects = append(requestMapping.Effects, protocol.ProtocolSemanticEffect{ID: field.name, Kind: "emit", Path: field.path, Target: "/" + field.name})
	}
	document.Mapping.Directions[protocol.EncodeRequest] = requestMapping
	responseMapping := document.Mapping.Directions[protocol.DecodeResponse]
	responseMapping.Program.Fields["status"] = protocol.Expression{Op: "literal", Value: protocol.StringValue("completed")}
	for _, field := range []string{"content", "usage"} {
		responseMapping.Program.Fields[field] = read("/" + field)
		responseMapping.Effects = append(responseMapping.Effects, protocol.ProtocolSemanticEffect{ID: field, Kind: "emit", Path: "/" + field, Target: "/" + field})
	}
	document.Mapping.Directions[protocol.DecodeResponse] = responseMapping
	operation := document.Spec.Operations["generate"]
	operation.Auth = protocol.Credential{Location: "header", Name: "Authorization", Prefix: "Bearer "}
	document.Spec.Operations["generate"] = operation
	compiled := compileSourceStreamPackage(t, document)

	const modelName = "probe-target"
	expected := mustEncodedProtocolValue(t, map[string]any{"model": modelName, "prompt": "ping", "limit": HealthProbeMaxTokens, "stream": false})
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if r.Method != http.MethodPost || r.URL.Path != operation.Path || r.Header.Get("Authorization") != "Bearer probe-key" || string(body) != expected.JSONString() {
			t.Errorf("unexpected core2 probe: method=%s path=%s headers=%v body=%s", r.Method, r.URL.Path, r.Header, body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"content":[{"kind":"text","payload":"pong"}],"usage":{"input":{"count":7,"origin":"observed"},"output":{"count":1,"origin":"observed"},"total":{"count":8,"origin":"observed"}}}`)
	}))
	defer upstream.Close()
	candidate := gatewayCandidate{compiled: compiled, operation: operation, operationName: "generate",
		model:   config.ModelRef{ID: "probe-model", Name: modelName, BaseURL: upstream.URL, APIKey: "probe-key"},
		binding: protocol.Binding{ProtocolID: compiled.Identity().DefinitionID, RevisionHash: compiled.Hash(), Capabilities: document.Mapping.Capabilities, Transports: []protocol.Transport{protocol.HTTPJSON}}}
	// Package activation is a separate migration boundary; exercise the same
	// preparation used after production routing has pinned its candidate.
	request, err := prepareHealthProbeRequest(t.Context(), candidate)
	if err != nil {
		t.Fatal(err)
	}
	if request.SchemaVersion != protocol.ProtocolCoreVersion || !request.Model.IsString(modelName) || calls.Load() != 0 {
		t.Fatal("probe preparation changed its target or sent a request", request, calls.Load())
	}
	response, err := server.collectProtocolGenerationAttempt(t.Context(), candidate, request, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if response.SchemaVersion != protocol.ProtocolCoreVersion || len(response.Content) != 1 || !response.Content[0].Payload.IsString("pong") {
		t.Fatal("core2 probe lost its response", response)
	}
	usage := response.Usage
	if usage == nil || usage.Input == nil || usage.Output == nil || usage.Total == nil || usage.Input.Count != 7 || usage.Output.Count != 1 || usage.Total.Count != 8 || calls.Load() != 1 {
		t.Fatal("core2 probe lost usage or sent more than one request", usage, calls.Load())
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

func TestProbeCredentialSelectionHonorsModelPermissions(t *testing.T) {
	t.Parallel()
	model := storage.Model{ID: "m", SourceID: "s", APIKey: "stale", BaseURL: "https://stale.invalid", Available: false}
	sources := []storage.ModelSource{{ID: "s", BaseURL: "https://current.invalid", APIKeys: []storage.SourceAPIKey{
		{Value: "wrong-model", AllowedModels: []string{"other"}},
		{Value: "permitted", AllowedModels: []string{"m"}},
	}}}
	ref, ok := routing.IndexSources(sources).Resolve(model)
	if !ok || ref.APIKey != "permitted" || ref.BaseURL != sources[0].BaseURL {
		t.Fatalf("probe used stale or unauthorized identity: %+v", ref)
	}
	sources[0].APIKeys = sources[0].APIKeys[:1]
	if _, ok := routing.IndexSources(sources).Resolve(model); ok {
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
	source := storage.ModelSource{ID: "slow", Name: "slow", BaseURL: upstream.URL, Platform: "custom:openai-chat-completions", Enabled: true, APIKeys: []storage.SourceAPIKey{{Value: "probe-key"}}}
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
	compiled, ok := service.Pin("openai-chat-completions")
	if !ok {
		t.Fatal("openai-chat-completions preset not active")
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
