package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/elysia-api/backend/protocol"
	"github.com/elysia-api/backend/storage"
)

func loadGatewayDefinition(t *testing.T, name string) protocol.Definition {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "protocol", "testdata", name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var definition protocol.Definition
	if err := json.Unmarshal(raw, &definition); err != nil {
		t.Fatal(err)
	}
	return definition
}

func activateGatewayDefinition(t *testing.T, server *Server, definition protocol.Definition) *protocol.Compiled {
	t.Helper()
	service, err := server.protocolService()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(definition)
	if err != nil {
		t.Fatal(err)
	}
	expectedDraft, expectedActive := "", ""
	if draft, err := service.ReadDraft(t.Context(), definition.ID); err == nil {
		expectedDraft = draft.Hash
	}
	if active, ok := service.Pin(definition.ID); ok {
		expectedActive = active.Hash()
	}
	draft, issues, err := service.SaveDraft(t.Context(), definition.ID, raw, expectedDraft)
	if err != nil || protocol.IssuesError(issues) != nil {
		t.Fatalf("draft: %v %v", err, issues)
	}
	revision, report, err := service.VerifyDraft(t.Context(), definition.ID, draft.Hash)
	if err != nil || !report.Passed {
		t.Fatalf("verify: %v %+v", err, report.Issues)
	}
	if _, err := service.Activate(t.Context(), definition.ID, revision.Hash, expectedActive); err != nil {
		t.Fatal(err)
	}
	compiled, _ := service.Pin(definition.ID)
	return compiled
}

func setupGatewayModel(t *testing.T, server *Server, upstream *protocol.Compiled, url string) {
	t.Helper()
	source := storage.ModelSource{ID: "gateway-source", Name: "gateway-source", BaseURL: url, Platform: "custom:" + upstream.Identity().DefinitionID, Enabled: true}
	if err := server.store.UpsertSource(t.Context(), source); err != nil {
		t.Fatal(err)
	}
	model := storage.Model{ID: "upstream-model", Name: "upstream-model", Available: true, Enabled: true}
	if err := server.store.ReplaceSourceModels(t.Context(), source, []storage.Model{model}); err != nil {
		t.Fatal(err)
	}
	if err := server.store.UpsertGroup(t.Context(), storage.ModelGroup{ID: "gateway-group", Name: "group", Enabled: true, Models: []string{"gateway-source:upstream-model"}, Strategy: "sequential"}); err != nil {
		t.Fatal(err)
	}
	if err := server.store.ImportLegacyConfig(t.Context(), []storage.APIToken{{Name: "gateway-test", Token: "gateway-test-token", Enabled: true, AllowedGroups: []string{"group"}}}, nil, nil); err != nil {
		t.Fatal(err)
	}
	transports := []protocol.Transport{}
	for _, operation := range upstream.Operations() {
		if operation.Kind == "generate" || operation.Kind == "session" {
			transports = append(transports, operation.Transport)
		}
	}
	binding := storage.ProtocolBinding{Kind: "source", SourceID: source.ID, Binding: protocol.Binding{ProtocolID: upstream.Identity().DefinitionID, RevisionHash: upstream.Hash(), Capabilities: upstream.Definition().Capabilities, Transports: transports}}
	service, err := server.protocolService()
	if err != nil {
		t.Fatal(err)
	}
	binding.Combinations = verifyGatewayBinding(t.Context(), service.View(), upstream, binding.Binding.Capabilities)
	if err := server.store.SaveProtocolBinding(t.Context(), binding); err != nil {
		t.Fatal(err)
	}
	server.invalidateRouteCache()
	server.engine.Any("/gateway/:protocolId/*path", server.authMiddleware(), server.gatewayProtocol)
}

func gatewayTestRequest(server *Server, protocolID, body, token string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, "/gateway/"+protocolID+"/generate", strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	server.engine.ServeHTTP(response, request)
	return response
}

func TestGatewayStandaloneProtocolsBidirectionalHTTP(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		t.Run(map[bool]string{false: "alpha-to-beta", true: "beta-to-alpha"}[reverse], func(t *testing.T) {
			server, _ := newProtocolAdminTestServer(t)
			ingressName, upstreamName := "text-alpha", "text-beta"
			if reverse {
				ingressName, upstreamName = upstreamName, ingressName
			}
			ingress := activateGatewayDefinition(t, server, loadGatewayDefinition(t, ingressName))
			upstream := activateGatewayDefinition(t, server, loadGatewayDefinition(t, upstreamName))
			var calls atomic.Int32
			provider := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				calls.Add(1)
				body, _ := io.ReadAll(request.Body)
				semantic, err := upstream.DecodeRequest(request.Context(), body, protocol.EvaluationContext{})
				if err != nil {
					t.Errorf("upstream received invalid request: %v", err)
					writer.WriteHeader(400)
					return
				}
				if string(semantic.Model.Bytes()) != `"upstream-model"` || string(semantic.Content[0].Children[0].Payload.Bytes()) != `"hello"` {
					t.Errorf("upstream request: %s", body)
				}
				response := &protocol.Response{SchemaVersion: 1, ID: protocol.StringValue("r1"), Content: []protocol.Node{{Kind: protocol.MessageNode, Role: protocol.StringValue("assistant"), Children: []protocol.Node{{Kind: protocol.TextNode, Payload: protocol.StringValue("world")}}}}}
				wire, err := upstream.EncodeResponse(request.Context(), response, protocol.EvaluationContext{})
				if err != nil {
					t.Errorf("response: %v", err)
				}
				writer.Header().Set("Content-Type", "application/json")
				writer.Write(wire)
			}))
			defer provider.Close()
			setupGatewayModel(t, server, upstream, provider.URL)
			input := &protocol.Request{SchemaVersion: 1, Model: protocol.StringValue("group"), Content: []protocol.Node{{Kind: protocol.MessageNode, Role: protocol.StringValue("user"), Children: []protocol.Node{{Kind: protocol.TextNode, Payload: protocol.StringValue("hello")}}}}}
			body, err := ingress.EncodeRequest(t.Context(), input, protocol.EvaluationContext{})
			if err != nil {
				t.Fatal(err)
			}
			result := gatewayTestRequest(server, ingressName, string(body), "gateway-test-token")
			if result.Code != 200 {
				t.Fatalf("forward: %d %s", result.Code, result.Body)
			}
			decoded, err := ingress.DecodeResponse(t.Context(), result.Body.Bytes(), protocol.EvaluationContext{})
			if err != nil || string(decoded.Content[0].Children[0].Payload.Bytes()) != `"world"` {
				t.Fatalf("response: %v %s", err, result.Body)
			}
			if calls.Load() != 1 {
				t.Fatalf("calls = %d", calls.Load())
			}
			denied := gatewayTestRequest(server, ingressName, string(body), "bad-token")
			if denied.Code != http.StatusUnauthorized || calls.Load() != 1 {
				t.Fatalf("auth: %d calls=%d", denied.Code, calls.Load())
			}
			input.Model = protocol.StringValue("forbidden")
			body, _ = ingress.EncodeRequest(t.Context(), input, protocol.EvaluationContext{})
			denied = gatewayTestRequest(server, ingressName, string(body), "gateway-test-token")
			if denied.Code != http.StatusForbidden || calls.Load() != 1 {
				t.Fatalf("group auth: %d %s", denied.Code, denied.Body)
			}
		})
	}
}

func TestGatewayInFlightRevisionAndStaleBinding(t *testing.T) {
	server, _ := newProtocolAdminTestServer(t)
	definition := loadGatewayDefinition(t, "text-alpha")
	old := activateGatewayDefinition(t, server, definition)
	entered, release := make(chan struct{}), make(chan struct{})
	provider := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		close(entered)
		<-release
		io.WriteString(writer, `{"requestId":"r","answer":[{"actor":"assistant","segments":[{"text":"old-version"}]}]}`)
	}))
	defer provider.Close()
	setupGatewayModel(t, server, old, provider.URL)
	const input = `{"deployment":"group","turns":[{"actor":"user","segments":[{"text":"hello"}]}]}`
	result := make(chan *httptest.ResponseRecorder, 1)
	go func() { result <- gatewayTestRequest(server, "text-alpha", input, "gateway-test-token") }()
	<-entered
	definition.Version = "2"
	activateGatewayDefinition(t, server, definition)
	close(release)
	response := <-result
	if response.Code != 200 || !bytes.Contains(response.Body.Bytes(), []byte("old-version")) {
		t.Fatalf("in-flight changed: %d %s", response.Code, response.Body)
	}
	response = gatewayTestRequest(server, "text-alpha", input, "gateway-test-token")
	if response.Code != 400 || !strings.Contains(response.Body.String(), string(protocol.VerificationMismatch)) {
		t.Fatalf("stale binding escaped: %d %s", response.Code, response.Body)
	}
}

func TestGatewayCanceledRequestDoesNotReachUpstream(t *testing.T) {
	server, _ := newProtocolAdminTestServer(t)
	compiled := activateGatewayDefinition(t, server, loadGatewayDefinition(t, "text-alpha"))
	var calls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	defer provider.Close()
	setupGatewayModel(t, server, compiled, provider.URL)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	request := httptest.NewRequest(http.MethodPost, "/gateway/text-alpha/generate", strings.NewReader(`{}`)).WithContext(ctx)
	request.Header.Set("Authorization", "Bearer gateway-test-token")
	result := httptest.NewRecorder()
	server.engine.ServeHTTP(result, request)
	if result.Code != 499 || calls.Load() != 0 {
		t.Fatalf("cancel: %d calls=%d", result.Code, calls.Load())
	}
}
