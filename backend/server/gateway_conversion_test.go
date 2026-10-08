package server

import (
	"encoding/json"
	"fmt"
	"github.com/elysia-api/backend/storage"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elysia-api/backend/protocol"
)

func TestGatewayGeminiClientStreamUsagePreferences(t *testing.T) {
	for _, test := range []struct {
		name, option string
		want         bool
	}{
		{"true", `,"stream_options":{"include_usage":true}`, true},
		{"false", `,"stream_options":{"include_usage":false}`, false},
		{"missing", "", false},
		{"null", `,"stream_options":null`, false},
		{"unknown", `,"stream_options":{"include_usage":true,"future_flag":true}`, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			s, _ := newProtocolAdminTestServer(t)
			activateDiscoveryPresets(t, s)
			service, _ := s.protocolService()
			upstream, _ := service.Pin(protocol.PresetGeminiID)
			calls := 0
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				raw, _ := io.ReadAll(r.Body)
				if strings.Contains(string(raw), "stream_options") {
					t.Error("client options reached Gemini", string(raw))
				}
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, "data: "+`{"candidates":[{"index":0,"content":{"role":"model","parts":[{"text":"hello","thoughtSignature":"test-signature"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":3,"candidatesTokenCount":2,"totalTokenCount":5}}`+"\n\n")
			}))
			defer provider.Close()
			setupGatewayModel(t, s, upstream, provider.URL)
			body := `{"model":"group","stream":true,"messages":[{"role":"user","content":"hi"}]` + test.option + "}"
			req := httptest.NewRequest(http.MethodPost, "/gateway/openai-chat-completions/chat/completions", strings.NewReader(body))
			req.Header.Set("Authorization", "Bearer gateway-test-token")
			rec := httptest.NewRecorder()
			s.engine.ServeHTTP(rec, req)
			if rec.Code != 200 || calls != 1 || !strings.Contains(rec.Body.String(), "hello") || strings.Contains(rec.Body.String(), "conversion_rejected") {
				t.Fatal(rec.Code, calls, rec.Body)
			}
			hasUsage := strings.Contains(rec.Body.String(), `"choices":[],"usage"`)
			// JSON fields are sorted: inspect frames rather than depend on order.
			hasUsage = false
			for _, line := range strings.Split(rec.Body.String(), "\n") {
				if !strings.HasPrefix(line, "data: {") {
					continue
				}
				var chunk struct {
					Choices []any           `json:"choices"`
					Usage   json.RawMessage `json:"usage"`
				}
				if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &chunk) == nil && len(chunk.Choices) == 0 && len(chunk.Usage) > 0 && string(chunk.Usage) != "null" {
					hasUsage = true
				}
			}
			if hasUsage != test.want {
				t.Fatal("usage preference ignored", test.want, rec.Body)
			}
		})
	}
}

func TestGatewayMessagesGeminiContinuationRoundTrip(t *testing.T) {
	s, _ := newProtocolAdminTestServer(t)
	activateDiscoveryPresets(t, s)
	service, _ := s.protocolService()
	upstream, _ := service.Pin(protocol.PresetGeminiID)
	calls := 0
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		raw, _ := io.ReadAll(r.Body)
		if calls == 2 && !strings.Contains(string(raw), `"thoughtSignature":"test-signature"`) {
			t.Error("signature was not restored", string(raw))
		}
		if strings.Contains(string(raw), protocol.ContinuationPrefix) {
			t.Error("gateway envelope leaked upstream")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"candidates":[{"index":0,"content":{"role":"model","parts":[{"text":"hello","thoughtSignature":"test-signature"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":3,"candidatesTokenCount":2,"totalTokenCount":5}}`)
	}))
	defer provider.Close()
	setupGatewayModel(t, s, upstream, provider.URL)
	call := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/gateway/anthropic-messages/v1/messages", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer gateway-test-token")
		req.Header.Set("x-elysia-session-id", "session-one")
		rec := httptest.NewRecorder()
		s.engine.ServeHTTP(rec, req)
		return rec
	}
	first := call(`{"model":"group","max_tokens":100,"messages":[{"role":"user","content":"hi"}]}`)
	if first.Code != 200 {
		t.Fatal(first.Code, first.Body)
	}
	var response struct {
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(first.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(response.Content), protocol.ContinuationPrefix) {
		t.Fatal("missing carrier", first.Body)
	}
	second := call(`{"model":"group","max_tokens":100,"messages":[{"role":"user","content":"hi"},{"role":"assistant","content":` + string(response.Content) + `},{"role":"user","content":"continue"}]}`)
	if second.Code != 200 || calls != 2 {
		t.Fatal(second.Code, calls, second.Body)
	}
}

// The second request deliberately omits every carrier and reloads SQLite first.
// This proves recovery does not depend on a process-local map.
func TestGatewayContinuationPersistenceAndIsolation(t *testing.T) {
	s, cfgPath := newProtocolAdminTestServer(t)
	s.store.Close()
	dbPath := filepath.Join(filepath.Dir(cfgPath), "encrypted.sqlite3")
	var err error
	s.store, err = storage.OpenWithKey(dbPath, s.config.GetDBEncryptionKey())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.store.Close() })
	activateDiscoveryPresets(t, s)
	service, _ := s.protocolService()
	upstream, _ := service.Pin(protocol.PresetGeminiID)
	wantSignature := false
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		if strings.Contains(string(raw), `"thoughtSignature":"signed-text"`) != wantSignature {
			t.Errorf("unexpected recovery; want signature %v, body %s", wantSignature, raw)
		}
		if strings.Contains(string(raw), protocol.ContinuationPrefix) {
			t.Error("carrier sent upstream")
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"candidates":[{"content":{"role":"model","parts":[{"text":"hello","thoughtSignature":"signed-text"}]},"finishReason":"STOP"}]}`)
	}))
	defer provider.Close()
	setupGatewayModel(t, s, upstream, provider.URL)
	call := func(session, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/gateway/anthropic-messages/v1/messages", strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer gateway-test-token")
		r.Header.Set("x-elysia-session-id", session)
		rec := httptest.NewRecorder()
		s.engine.ServeHTTP(rec, r)
		if rec.Code != 200 {
			t.Fatalf("HTTP %d %s", rec.Code, rec.Body)
		}
		return rec
	}
	first := call("stable", `{"model":"group","max_tokens":100,"messages":[{"role":"user","content":"hi"}]}`)
	stats, err := s.store.ContinuationStats(t.Context())
	if err != nil || stats["records"] != 1 {
		t.Fatal(stats, err)
	}
	if err = s.store.Close(); err != nil {
		t.Fatal(err)
	}
	s.store, err = storage.OpenWithKey(dbPath, s.config.GetDBEncryptionKey())
	if err != nil {
		t.Fatal(err)
	}
	body := `{"model":"group","max_tokens":100,"messages":[{"role":"user","content":"hi"},{"role":"assistant","content":[{"type":"text","text":"hello"}]},{"role":"user","content":"continue"}]}`
	wantSignature = true
	call("stable", body)
	wantSignature = false
	call("different-session", body)
	call("stable", strings.Replace(body, `"text":"hello"`, `"text":"modified"`, 1))
	var response struct {
		Content json.RawMessage `json:"content"`
	}
	if err = json.Unmarshal(first.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	bodyWithCarrier := strings.Replace(body, `[{"type":"text","text":"hello"}]`, string(response.Content), 1)
	call("another-session", bodyWithCarrier)
}

func TestGatewayParallelGeminiToolSignatures(t *testing.T) {
	s, _ := newProtocolAdminTestServer(t)
	activateDiscoveryPresets(t, s)
	service, _ := s.protocolService()
	upstream, _ := service.Pin(protocol.PresetGeminiID)
	calls := 0
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		raw, _ := io.ReadAll(r.Body)
		if calls == 2 {
			for _, expected := range []string{`"thoughtSignature":"signature-a"`, `"thoughtSignature":"signature-b"`, `"functionResponse"`, `answer-a`, `answer-b`} {
				if !strings.Contains(string(raw), expected) {
					t.Errorf("missing %s in %s", expected, raw)
				}
			}
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"id":"call-a","name":"lookup","args":{"key":"a"}},"thoughtSignature":"signature-a"},{"functionCall":{"id":"call-b","name":"lookup","args":{"key":"b"}},"thoughtSignature":"signature-b"}]},"finishReason":"STOP"}]}`)
	}))
	defer provider.Close()
	setupGatewayModel(t, s, upstream, provider.URL)
	yes := true
	if _, err := s.store.UpdateModel(t.Context(), "upstream-model", "gateway-source", storage.ModelPatch{ToolsCapable: &yes}); err != nil {
		t.Fatal(err)
	}
	if err := s.store.UpsertGroup(t.Context(), storage.ModelGroup{ID: "gateway-group", Name: "group", Enabled: true, ToolsCapable: true, Models: []string{"gateway-source:upstream-model"}, Strategy: "sequential"}); err != nil {
		t.Fatal(err)
	}
	s.invalidateRouteCache()

	call := func(body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/gateway/anthropic-messages/v1/messages", strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer gateway-test-token")
		r.Header.Set("x-elysia-session-id", "tools")
		rec := httptest.NewRecorder()
		s.engine.ServeHTTP(rec, r)
		if rec.Code != 200 {
			t.Fatalf("HTTP %d: %s", rec.Code, rec.Body)
		}
		return rec
	}
	first := call(`{"model":"group","max_tokens":100,"messages":[{"role":"user","content":"hi"}]}`)
	var result struct {
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(first.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	call(`{"model":"group","max_tokens":100,"messages":[{"role":"user","content":"hi"},{"role":"assistant","content":` + string(result.Content) + `},{"role":"user","content":[{"type":"tool_result","tool_use_id":"call-a","content":"answer-a"},{"type":"tool_result","tool_use_id":"call-b","content":"answer-b"}]}]}`)
	if calls != 2 {
		t.Fatal(calls)
	}
}

func TestGatewayStrictLateGeminiSignature(t *testing.T) {
	s, cfgPath := newProtocolAdminTestServer(t)
	s.store.Close()
	var err error
	s.store, err = storage.OpenWithKey(filepath.Join(filepath.Dir(cfgPath), "strict.db"), s.config.GetDBEncryptionKey())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.store.Close() })
	activateDiscoveryPresets(t, s)
	service, _ := s.protocolService()
	upstream, _ := service.Pin(protocol.PresetGeminiID)
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, frame := range []string{
			`{"candidates":[{"content":{"role":"model","parts":[{"text":"hel"}]}}]}`,
			`{"candidates":[{"content":{"role":"model","parts":[{"text":"lo"}]}}]}`,
			`{"candidates":[{"content":{"role":"model","parts":[{"thoughtSignature":"late-signature"}]}}]}`,
			`{"candidates":[{"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":2}}`,
		} {
			io.WriteString(w, "data: "+frame+"\n\n")
		}
	}))
	defer provider.Close()
	setupGatewayModel(t, s, upstream, provider.URL)
	bindings, _ := s.store.ListProtocolBindings(t.Context())
	bindings[0].Conversion = &protocol.ConversionSelection{Overrides: &protocol.ConversionPolicy{SchemaVersion: 1, ID: "strict", Name: "strict", Mode: "strict", Rules: []protocol.ConversionRule{}}}
	verified, err := s.verifyConversionBindings(t.Context(), service.View(), nil, bindings)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.store.SaveProtocolBinding(t.Context(), verified[0]); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "/gateway/anthropic-messages/v1/messages", strings.NewReader(`{"model":"group","max_tokens":100,"stream":true,"messages":[{"role":"user","content":"hi"}]}`))
	r.Header.Set("Authorization", "Bearer gateway-test-token")
	r.Header.Set("x-elysia-session-id", "strict-session")
	rec := httptest.NewRecorder()
	s.engine.ServeHTTP(rec, r)
	if rec.Code != 200 || rec.Result().Trailer.Get(gatewayStreamErrorTrailer) != "" || !strings.Contains(rec.Body.String(), protocol.ContinuationPrefix) || !strings.Contains(rec.Body.String(), `"type":"message_stop"`) {
		t.Fatal(rec.Code, rec.Body)
	}
}

func TestProviderSignatureProbeRequiresAcceptedScopedEvidence(t *testing.T) {
	s, _ := newProtocolAdminTestServer(t)
	activateDiscoveryPresets(t, s)
	service, _ := s.protocolService()
	upstream, _ := service.Pin(protocol.PresetGeminiID)
	ingress, _ := service.Pin(protocol.PresetAnthropicID)
	accept := false
	calls := 0
	wantKey := ""
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("x-goog-api-key") != wantKey {
			t.Error("probe used wrong account")
		}
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), `"thoughtSignature":"probe-only-value"`) {
			t.Error("configured probe value missing", string(body))
		}
		if !accept {
			w.WriteHeader(400)
			return
		}
		io.WriteString(w, `{"candidates":[{"content":{"role":"model","parts":[{"text":"OK"}]},"finishReason":"STOP"}]}`)
	}))
	defer provider.Close()
	setupGatewayModel(t, s, upstream, provider.URL)
	yes := true
	if _, err := s.store.UpdateModel(t.Context(), "upstream-model", "gateway-source", storage.ModelPatch{ToolsCapable: &yes}); err != nil {
		t.Fatal(err)
	}
	policy := protocol.ConversionPolicy{SchemaVersion: 1, ID: "probe-policy", Name: "probe", Mode: "compatible", Rules: []protocol.ConversionRule{{ID: "configured-fallback", Order: 900, Enabled: true, Phase: protocol.ConversionRequest, Match: protocol.ConversionMatch{TargetFamily: "gemini", NodeKind: protocol.ToolCallNode}, Action: "provider_signature", Value: protocol.StringValue("probe-only-value")}}}
	draft, err := s.store.SaveConversionDraft(t.Context(), policy, "")
	if err != nil {
		t.Fatal(err)
	}
	call := func() *httptest.ResponseRecorder {
		c, rec := adminProtocolContext("POST", "/probe-signature", fmt.Sprintf(`{"hash":%q,"ruleId":"configured-fallback","ingressId":"anthropic-messages","sourceId":"gateway-source","modelId":"upstream-model"}`, draft.Hash))
		c.AddParam("policyId", policy.ID)
		s.adminConversionSignatureProbe(c)
		return rec
	}
	if rec := call(); rec.Code != 400 {
		t.Fatal(rec.Code, rec.Body)
	}
	models, _ := s.store.ListModels(t.Context())
	ref := modelReference(models[0])
	scope, _ := s.providerEvidenceScope(ref)
	conversion, err := protocol.ResolveConversion(protocol.DefaultConversionPolicy(ingress.Identity(), upstream.Identity()), policy)
	if err != nil {
		t.Fatal(err)
	}
	proof, err := s.store.ConversionProviderEvidence(t.Context(), conversion.Hash, scope, upstream.Hash(), time.Now().Unix())
	if err != nil || len(proof) != 0 {
		t.Fatal(proof, err)
	}
	accept = true
	if rec := call(); rec.Code != 200 {
		t.Fatal(rec.Code, rec.Body)
	}
	if calls != 2 {
		t.Fatal(calls)
	}
	proof, err = s.store.ConversionProviderEvidence(t.Context(), conversion.Hash, scope, upstream.Hash(), time.Now().Unix())
	if err != nil || !proof["configured-fallback"] {
		t.Fatal(proof, err)
	}
	proof, err = s.store.ConversionProviderEvidence(t.Context(), conversion.Hash, "different-account", upstream.Hash(), time.Now().Unix())
	if err != nil || len(proof) > 0 {
		t.Fatal(proof, err)
	}
	keys := []storage.SourceAPIKey{{Value: "first-test-key"}, {Value: "second-test-key"}, {Value: "disabled-test-key", Disabled: true}}
	if err := s.store.UpdateSourceAPIKeys(t.Context(), "gateway-source", keys); err != nil {
		t.Fatal(err)
	}
	keyCall := func(index int) *httptest.ResponseRecorder {
		c, rec := adminProtocolContext("POST", "/probe-signature", fmt.Sprintf(`{"hash":%q,"ruleId":"configured-fallback","ingressId":"anthropic-messages","sourceId":"gateway-source","modelId":"upstream-model","keyIndex":%d}`, draft.Hash, index))
		c.AddParam("policyId", policy.ID)
		s.adminConversionSignatureProbe(c)
		return rec
	}
	for _, index := range []int{-1, 2, 3} {
		if rec := keyCall(index); rec.Code != 400 {
			t.Fatal(rec.Code, rec.Body)
		}
	}
	if calls != 2 {
		t.Fatal("invalid key selection reached upstream")
	}
	wantKey = keys[1].Value
	if rec := keyCall(1); rec.Code != 200 {
		t.Fatal(rec.Code, rec.Body)
	}
	for i, key := range keys[:2] {
		ref.APIKey = key.Value
		scope, _ := s.providerEvidenceScope(ref)
		proof, err := s.store.ConversionProviderEvidence(t.Context(), conversion.Hash, scope, upstream.Hash(), time.Now().Unix())
		if err != nil || proof["configured-fallback"] != (i == 1) {
			t.Fatal("probe proof escaped selected account", i, proof, err)
		}
	}
}
