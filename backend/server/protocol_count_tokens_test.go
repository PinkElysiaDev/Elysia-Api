package server

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elysia-api/backend/protocol"
	"github.com/elysia-api/backend/storage"
)

const countRequestBody = `{"model":"group","messages":[{"role":"user","content":"hello"}]}`

func countTokenRequest(s *Server, path, body, key string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+key)
	response := httptest.NewRecorder()
	serveTestHTTP(s.engine, response, request)
	return response
}

func setupTokenCounting(t *testing.T, s *Server, compiled *protocol.Compiled, endpoint string) {
	t.Helper()
	setupGatewayModel(t, s, compiled, endpoint)
	canUseInputs := true
	if _, err := s.store.UpdateModel(t.Context(), "upstream-model", "gateway-source", storage.ModelPatch{ToolsCapable: &canUseInputs, VisionCapable: &canUseInputs}); err != nil {
		t.Fatal(err)
	}
	if err := s.store.UpsertGroup(t.Context(), storage.ModelGroup{ID: "gateway-group", Name: "group", Enabled: true, ToolsCapable: true, VisionCapable: true, Models: []string{"gateway-source:upstream-model"}, Strategy: "sequential"}); err != nil {
		t.Fatal(err)
	}
	s.invalidateRouteCache()
	s.engine.POST("/v1/messages/count_tokens", s.authMiddleware(), s.countTokens)
	s.engine.POST("/v1beta/models/*action", s.authMiddleware(), s.chatCompletions)
}

func TestCountTokensUpstreamMatrix(t *testing.T) {
	for _, targetName := range []string{"anthropic", "google-generate-content"} {
		t.Run(targetName, func(t *testing.T) {
			s, _ := newProtocolAdminTestServer(t)
			packages := seedGatewayPackages(t, s)
			target := packages[targetName]
			decoder, _ := target.TokenCountRequestProtocol("count_tokens")
			operation, _ := target.Operation("count_tokens")
			var calls atomic.Int32
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.URL.Path != strings.ReplaceAll(operation.Path, "{model}", "upstream-model") {
					t.Error("counter sent a generation request", r.URL.Path)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
					return
				}
				request, err := decoder.DecodeRequest(r.Context(), body, protocol.EvaluationContext{Scope: protocol.Scope{Model: "upstream-model"}})
				if err != nil {
					t.Error(err, string(body))
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				if !request.Model.IsString("upstream-model") || !request.Parameters["max_output_tokens"].IsZero() || len(request.Content) == 0 {
					t.Error("counting changed model/input or acquired generation settings", string(body))
				}
				response, err := target.EncodeTokenCount(r.Context(), "count_tokens", protocol.TokenCount{InputTokens: 9007199254740993})
				if err != nil {
					t.Error(err)
					return
				}
				w.Header().Set("Content-Type", contentTypeJSON)
				_, _ = w.Write(response.Bytes())
			}))
			defer provider.Close()
			setupTokenCounting(t, s, target, provider.URL)
			for _, sourceName := range []string{"anthropic", "google-generate-content"} {
				source := packages[sourceName]
				document, _ := source.Package()
				operation, _ := source.Operation("count_tokens")
				path := strings.ReplaceAll(operation.Path, "{model}", "group")
				for _, sample := range document.Mapping.TokenCountSamples {
					if sample.Direction != protocol.DecodeRequest {
						continue
					}
					for _, prefix := range []string{"", "/gateway/" + source.Identity().DefinitionID} {
						t.Run(sourceName+"/"+sample.ID+prefix, func(t *testing.T) {
							body := strings.ReplaceAll(sample.Input.JSONString(), `"model":"m"`, `"model":"group"`)
							body = strings.ReplaceAll(body, `"models/m"`, `"models/group"`)
							response := countTokenRequest(s, prefix+path, body, "gateway-test-token")
							if response.Code != http.StatusOK {
								t.Fatal(response.Code, response.Body)
							}
							count, err := source.DecodeTokenCount(t.Context(), "count_tokens", mustProtocolValue(t, response.Body.String()))
							if err != nil || count.InputTokens != 9007199254740993 {
								t.Fatal(count, err)
							}
						})
					}
				}
			}
			if calls.Load() != 12 {
				t.Fatal("counting missed or repeated an upstream call", calls.Load())
			}
			if usage := s.limiter.Usage("gateway-group"); usage.Tokens != 0 || usage.Active != 0 || usage.Requests != 12 {
				t.Fatal("counting changed generation token accounting or leaked concurrency", usage)
			}
			if rows, err := s.store.CountUsageRecords(t.Context()); err != nil || rows != 0 {
				t.Fatal("count result entered generation usage records", rows, err)
			}
		})
	}
}

func TestCountTokensFailuresAndPermissions(t *testing.T) {
	s, _ := newProtocolAdminTestServer(t)
	packages := seedGatewayPackages(t, s)
	var calls atomic.Int32
	var reply atomic.Value
	reply.Store(`{"input_tokens":0}`)
	var status atomic.Int32
	status.Store(http.StatusOK)
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/v1/messages/count_tokens" {
			t.Error("count operation escaped its endpoint", r.URL.Path)
		}
		w.Header().Set("Content-Type", contentTypeJSON)
		w.Header().Set("Location", "/v1/messages")
		w.WriteHeader(int(status.Load()))
		_, _ = io.WriteString(w, reply.Load().(string))
	}))
	defer provider.Close()
	setupTokenCounting(t, s, packages["anthropic"], provider.URL)
	for _, fixture := range []struct {
		name, body string
		status     int
		want       int
	}{
		{"zero", `{"input_tokens":0}`, http.StatusOK, http.StatusOK},
		{"missing", `{}`, http.StatusOK, http.StatusBadGateway},
		{"negative", `{"input_tokens":-1}`, http.StatusOK, http.StatusBadGateway},
		{"fractional", `{"input_tokens":1.5}`, http.StatusOK, http.StatusBadGateway},
		{"overflow", `{"input_tokens":9223372036854775808}`, http.StatusOK, http.StatusBadGateway},
		{"invalid-json", `{`, http.StatusOK, http.StatusBadGateway},
		{"upstream", `{"type":"error","error":{"type":"overloaded_error","message":"count unavailable"}}`, http.StatusServiceUnavailable, http.StatusServiceUnavailable},
		{"redirect", `{}`, http.StatusFound, http.StatusFound},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			reply.Store(fixture.body)
			status.Store(int32(fixture.status))
			before := calls.Load()
			result := countTokenRequest(s, "/v1/messages/count_tokens", countRequestBody, "gateway-test-token")
			if result.Code != fixture.want || calls.Load() != before+1 {
				t.Fatal("count failure retried, changed status or fell back", result.Code, result.Body, calls.Load()-before)
			}
		})
	}
	before := calls.Load()
	if result := countTokenRequest(s, "/v1/messages/count_tokens", countRequestBody, "invalid"); result.Code != http.StatusUnauthorized {
		t.Fatal(result.Code, result.Body)
	}
	if err := s.store.UpsertAPIToken(t.Context(), storage.APIToken{Name: "restricted", Token: "restricted-token", Enabled: true, AllowedGroups: []string{"other"}}); err != nil {
		t.Fatal(err)
	}
	s.invalidateRouteCache()
	if result := countTokenRequest(s, "/v1/messages/count_tokens", countRequestBody, "restricted-token"); result.Code != http.StatusForbidden {
		t.Fatal(result.Code, result.Body)
	}
	if result := countTokenRequest(s, "/v1/messages/count_tokens", `{"model":"group","messages":[],"max_tokens":1}`, "gateway-test-token"); result.Code != http.StatusBadRequest {
		t.Fatal("count request accepted generation budget", result.Code, result.Body)
	}
	if calls.Load() != before {
		t.Fatal("rejected authorization or input reached the provider")
	}
	if rows, err := s.store.CountUsageRecords(t.Context()); err != nil || rows != 0 {
		t.Fatal("count failure entered generation usage records", rows, err)
	}
	if usage := s.limiter.Usage("gateway-group"); usage.Tokens != 0 || usage.Active != 0 {
		t.Fatal("count failure retained a reservation", usage)
	}
}

func TestCountTokensRequiresDeclaredCandidate(t *testing.T) {
	s, _ := newProtocolAdminTestServer(t)
	packages := seedGatewayPackages(t, s)
	var calls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	defer provider.Close()
	setupTokenCounting(t, s, packages["openai-chat"], provider.URL)
	result := countTokenRequest(s, "/v1/messages/count_tokens", countRequestBody, "gateway-test-token")
	if result.Code != http.StatusBadRequest || !strings.Contains(result.Body.String(), "unsupported_capability") || calls.Load() != 0 {
		t.Fatal("generation-only candidate was used as a token counter", result.Code, result.Body, calls.Load())
	}
}

func TestCountTokensRespectsSourceKeysAndWirePermissions(t *testing.T) {
	s, _ := newProtocolAdminTestServer(t)
	packages := seedGatewayPackages(t, s)
	var calls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("x-api-key") != "permitted-count-key" {
			t.Error("counter selected an unauthorized source key")
		}
		_, _ = io.WriteString(w, `{"input_tokens":9}`)
	}))
	defer provider.Close()
	setupTokenCounting(t, s, packages["anthropic"], provider.URL)
	keys := []storage.SourceAPIKey{{Value: "forbidden-count-key", AllowedModels: []string{}}, {Value: "permitted-count-key", AllowedModels: []string{"upstream-model"}}}
	if err := s.store.UpdateSourceAPIKeys(t.Context(), "gateway-source", keys); err != nil {
		t.Fatal(err)
	}
	s.invalidateRouteCache()
	result := countTokenRequest(s, "/v1/messages/count_tokens", countRequestBody, "gateway-test-token")
	if result.Code != http.StatusOK || calls.Load() != 1 {
		t.Fatal(result.Code, result.Body, calls.Load())
	}
	bindings, err := s.store.ListProtocolBindings(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var binding storage.ProtocolBinding
	for _, entry := range bindings {
		if entry.Kind == "source" && entry.SourceID == "gateway-source" {
			binding = entry
		}
	}
	// Model rewrite is checked after wire rules, where the actual upstream
	// input can be compared with the already authorized route.
	binding.Conversion = &protocol.ConversionSelection{Overrides: &protocol.ConversionPolicy{SchemaVersion: protocol.ConversionPolicySchemaVersion, ID: "rewrite-model", Rules: []protocol.ConversionRule{{ID: "rewrite", Order: 1, Enabled: true, Phase: protocol.ConversionWire, Action: "set", Path: "/model", Value: protocol.StringValue("forbidden-model")}}}}
	if err := s.store.SaveProtocolBinding(t.Context(), binding); err != nil {
		t.Fatal(err)
	}
	result = countTokenRequest(s, "/v1/messages/count_tokens", countRequestBody, "gateway-test-token")
	if result.Code != http.StatusBadRequest || !strings.Contains(result.Body.String(), "counting wire changed the authorized model") || calls.Load() != 1 {
		t.Fatal("wire rule bypassed model permissions", result.Code, result.Body, calls.Load())
	}
	keys[1].AllowedModels = []string{}
	if err := s.store.UpdateSourceAPIKeys(t.Context(), "gateway-source", keys); err != nil {
		t.Fatal(err)
	}
	s.invalidateRouteCache()
	result = countTokenRequest(s, "/v1/messages/count_tokens", countRequestBody, "gateway-test-token")
	if result.Code == http.StatusOK || calls.Load() != 1 {
		t.Fatal("explicit empty key permissions became unrestricted", result.Code, result.Body)
	}
}

func TestCountTokensCancellationReleasesState(t *testing.T) {
	s, _ := newProtocolAdminTestServer(t)
	packages := seedGatewayPackages(t, s)
	started, canceled := make(chan struct{}), make(chan error, 1)
	release := make(chan struct{})
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		close(started)
		select {
		case <-r.Context().Done():
		case <-release:
		}
		canceled <- r.Context().Err()
	}))
	defer provider.Close()
	defer close(release)
	setupTokenCounting(t, s, packages["anthropic"], provider.URL)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	request := httptest.NewRequestWithContext(ctx, http.MethodPost, "/v1/messages/count_tokens", strings.NewReader(countRequestBody))
	request.Header.Set("Authorization", "Bearer gateway-test-token")
	response := httptest.NewRecorder()
	done := make(chan struct{})
	go func() { serveTestHTTP(s.engine, response, request); close(done) }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("counter did not reach the simulated provider")
	}
	cancel()
	select {
	case err := <-canceled:
		if !errors.Is(err, context.Canceled) {
			t.Fatal("count cancellation did not reach the provider", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("provider count did not cancel")
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("count handler did not release after cancellation")
	}
	if response.Code != statusClientClosedRequest {
		t.Fatal(response.Code, response.Body)
	}
	if usage := s.limiter.Usage("gateway-group"); usage.Active != 0 || usage.Tokens != 0 {
		t.Fatal("canceled count retained quota", usage)
	}
	if state := s.preparationLimiter().Snapshot(); state.Active != 0 || state.ReservedBodyBytes != 0 {
		t.Fatal("canceled count retained preparation state", state)
	}
	if rows, err := s.store.CountUsageRecords(t.Context()); err != nil || rows != 0 {
		t.Fatal("canceled count created a generation settlement", rows, err)
	}
}

func TestCountTokensRetryPinsConversionSnapshot(t *testing.T) {
	s, _ := newProtocolAdminTestServer(t)
	packages := seedGatewayPackages(t, s)
	started, resume := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			close(started)
			select {
			case <-resume:
			case <-r.Context().Done():
				return
			}
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = io.WriteString(w, `{"type":"error","error":{"type":"overloaded_error","message":"retry"}}`)
			return
		}
		_, _ = io.WriteString(w, `{"input_tokens":7}`)
	}))
	defer provider.Close()
	setupTokenCounting(t, s, packages["anthropic"], provider.URL)
	keys := []storage.SourceAPIKey{{Value: "first", AllowedModels: []string{"upstream-model"}}, {Value: "second", AllowedModels: []string{"upstream-model"}}}
	if err := s.store.UpsertSource(t.Context(), storage.ModelSource{ID: "gateway-source", Name: "gateway-source", BaseURL: provider.URL, Platform: "custom:" + packages["anthropic"].Identity().DefinitionID, Enabled: true, KeyStrategy: storage.KeyStrategyPriority, APIKeys: keys}); err != nil {
		t.Fatal(err)
	}
	if err := s.store.UpsertGroup(t.Context(), storage.ModelGroup{ID: "gateway-group", Name: "group", Enabled: true, Models: []string{"gateway-source:upstream-model"}, Strategy: "sequential", MaxRetries: 1}); err != nil {
		t.Fatal(err)
	}
	s.invalidateRouteCache()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	request := httptest.NewRequestWithContext(ctx, http.MethodPost, "/v1/messages/count_tokens", strings.NewReader(countRequestBody))
	request.Header.Set("Authorization", "Bearer gateway-test-token")
	response := httptest.NewRecorder()
	done := make(chan struct{})
	go func() { serveTestHTTP(s.engine, response, request); close(done) }()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("count did not start", ctx.Err())
	}
	bindings, err := s.store.ListProtocolBindings(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, binding := range bindings {
		if binding.Kind != "source" || binding.SourceID != "gateway-source" {
			continue
		}
		binding.Conversion = &protocol.ConversionSelection{Overrides: &protocol.ConversionPolicy{SchemaVersion: protocol.ConversionPolicySchemaVersion, ID: "new-count-policy", Rules: []protocol.ConversionRule{{ID: "rewrite", Order: 1, Enabled: true, Phase: protocol.ConversionWire, Action: "set", Path: "/model", Value: protocol.StringValue("changed-model")}}}}
		if err := s.store.SaveProtocolBinding(t.Context(), binding); err != nil {
			t.Fatal(err)
		}
	}
	close(resume)
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("count retry did not complete", ctx.Err())
	}
	if response.Code != http.StatusOK || response.Body.String() != `{"input_tokens":7}` || calls.Load() != 2 {
		t.Fatal("retry mixed conversion snapshots", response.Code, response.Body, calls.Load())
	}
	next := countTokenRequest(s, "/v1/messages/count_tokens", countRequestBody, "gateway-test-token")
	if next.Code != http.StatusBadRequest || !strings.Contains(next.Body.String(), "counting wire changed the authorized model") || calls.Load() != 2 {
		t.Fatal("new request missed the updated policy", next.Code, next.Body, calls.Load())
	}
}
