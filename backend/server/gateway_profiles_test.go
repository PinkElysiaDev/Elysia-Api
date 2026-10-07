package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/elysia-api/backend/protocol"
	"github.com/elysia-api/backend/storage"
)

func TestGatewayProfilesRejectUnsupportedInputAndUnexpectedOutput(t *testing.T) {
	s, _ := newProtocolAdminTestServer(t)
	activateDiscoveryPresets(t, s)
	service, _ := s.protocolService()
	upstream, _ := service.Pin("anthropic-messages")
	calls := 0
	shouldReturnReasoning := false
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		body := `{"id":"r","model":"upstream-model","type":"message","role":"assistant","content":[{"type":"text","text":"hello"}],"stop_reason":"end_turn","usage":{"input_tokens":3,"output_tokens":2}}`
		if shouldReturnReasoning {
			body = `{"id":"r","model":"upstream-model","type":"message","role":"assistant","content":[{"type":"thinking","thinking":"thought","signature":"signed"}],"stop_reason":"end_turn"}`
		}
		_, _ = w.Write([]byte(body))
	}))
	defer provider.Close()
	setupGatewayModel(t, s, upstream, provider.URL)
	request := func(body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/gateway/openai-chat-completions/chat/completions", strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer gateway-test-token")
		response := httptest.NewRecorder()
		s.engine.ServeHTTP(response, r)
		return response
	}
	input := `{"model":"group","max_tokens":10,"messages":[{"role":"user","content":"hello"}]}`
	if response := request(input); response.Code != http.StatusOK || calls != 1 || !strings.Contains(response.Body.String(), "hello") {
		t.Fatal(response.Code, calls, response.Body)
	}
	if response := request(strings.TrimSuffix(input, "}") + `,"prompt_cache_key":"cache"}`); response.Code == http.StatusOK || calls != 1 {
		t.Fatal("unsupported cache sent to provider", response.Code, calls, response.Body)
	}
	shouldReturnReasoning = true
	if response := request(input); response.Code == http.StatusOK || calls != 2 {
		t.Fatal("unexpected signed reasoning was silently stripped", response.Code, calls, response.Body)
	}
	bindings, err := s.store.ListProtocolBindings(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	binding := bindings[0]
	// A profile verified before this edit cannot authorize the edited contract.
	binding.Binding.Capabilities[protocol.FreeTextToolsCapability] = false
	if err := s.store.SaveProtocolBinding(t.Context(), binding); err != nil {
		t.Fatal(err)
	}
	if response := request(input); response.Code == http.StatusOK || calls != 2 {
		t.Fatal("stale evidence accepted", response.Code, calls, response.Body)
	}
}

func TestSourceSavePreservesOperatorBindingAndRejectsStaleRevision(t *testing.T) {
	s, _ := newProtocolAdminTestServer(t)
	definition := loadGatewayDefinition(t, "text-alpha")
	activateGatewayDefinition(t, s, definition)
	source := storage.ModelSource{ID: "new-source", Name: "Original", Platform: "custom:text-alpha", BaseURL: "https://example.invalid", Enabled: true}
	if err := s.saveSource(t.Context(), source); err != nil {
		t.Fatal(err)
	}
	bindings, err := s.store.ListProtocolBindings(t.Context())
	if err != nil || len(bindings) != 1 {
		t.Fatal(bindings, err)
	}
	binding := bindings[0]
	binding.Binding.Operation = "generate"
	if err := s.store.SaveProtocolBinding(t.Context(), binding); err != nil {
		t.Fatal(err)
	}
	source.Name = "Edited"
	if err := s.saveSource(t.Context(), source); err != nil {
		t.Fatal(err)
	}
	bindings, err = s.store.ListProtocolBindings(t.Context())
	if err != nil || bindings[0].Binding.Operation != "generate" {
		t.Fatal("operator contract overwritten", bindings, err)
	}
	definition.Version = "2"
	activateGatewayDefinition(t, s, definition)
	source.Name = "Must not save"
	if err := s.saveSource(t.Context(), source); err == nil {
		t.Fatal("stale revision replaced without binding review")
	}
	sources, err := s.store.ListSources(t.Context())
	if err != nil || len(sources) != 1 || sources[0].Name != "Edited" {
		t.Fatal(sources, err)
	}
}
