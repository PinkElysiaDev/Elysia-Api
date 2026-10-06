package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elysia-api/backend/protocol"
)

func TestCacheAliasEditorAgentAndForwardingShareContract(t *testing.T) {
	definition := cacheAliasExampleDefinition(t)
	raw, err := json.Marshal(definition)
	if err != nil {
		t.Fatal(err)
	}
	s := newAgentIntegrationServer(t)
	author := &protocolAuthorContext{draft: raw}
	for _, action := range []string{"verify", "save"} {
		result := (&protocolV2Tool{server: s, action: action}).Execute(t.Context(), author, nil)
		if !result.OK {
			t.Fatal(action, result.Summary, result.MarshalData())
		}
	}
	service, err := s.protocolService()
	if err != nil {
		t.Fatal(err)
	}
	compiled, issues := service.Validate(raw)
	if compiled == nil {
		t.Fatal(issues)
	}
	activation, _ := json.Marshal(map[string]string{"id": definition.ID, "hash": compiled.Hash()})
	result := (&protocolV2Tool{server: s, action: "activate"}).Execute(t.Context(), author, activation)
	if !result.OK {
		t.Fatal(result.Summary, result.MarshalData())
	}
	if active, exists := service.Pin(definition.ID); !exists || active.Hash() != compiled.Hash() {
		t.Fatal("Agent activation lost the verified cache mapping")
	}
	fixture := cacheWireFixtures()[0]
	usage := `"cached_tokens":70`
	alias := `"cached_tokens":70},"provider_metrics":{"write_tokens":20,"keep":"extension"`
	body := strings.ReplaceAll(fixture.response, usage, alias)
	input := mustProtocolValue(t, body)
	preview, _ := json.Marshal(protocol.PreviewInput{Definition: mustEncodedProtocolValue(t, definition), Direction: protocol.DecodeResponse, Input: input})
	ctx, recorder := adminProtocolContext("POST", "/api/admin/protocols/preview", string(preview))
	s.adminProtocolPreviewV2(ctx)
	if recorder.Code != http.StatusOK {
		t.Fatal(recorder.Body)
	}
	var editor struct {
		Data protocol.PreviewResult `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &editor); err != nil {
		t.Fatal(err)
	}
	command, _ := json.Marshal(protocolCommandInput{Direction: protocol.DecodeResponse, SampleRequest: input})
	agent := (&protocolV2Tool{server: s, action: "preview"}).Execute(t.Context(), author, command)
	if !agent.OK || string(agent.Data.(protocol.PreviewResult).Semantic.Bytes()) != string(editor.Data.Semantic.Bytes()) {
		t.Fatal("editor and Agent cache previews differ", agent.MarshalData())
	}
	var semantic protocol.Response
	if err := editor.Data.Semantic.Decode(&semantic); err != nil || semantic.Usage.CacheCreation.Count != 20 {
		t.Fatal("preview did not extract the declared alias", err)
	}
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		request, _ := io.ReadAll(r.Body)
		wire := body
		if strings.Contains(string(request), `"stream":true`) {
			w.Header().Set("Content-Type", "text/event-stream")
			wire = strings.ReplaceAll(fixture.stream, usage, alias)
		}
		_, _ = io.WriteString(w, wire)
	}))
	t.Cleanup(provider.Close)
	directory := t.TempDir()
	suite := &liveSuite{Model: "m", Origin: provider.URL, key: "synthetic-secret", StartedAt: time.Now(), client: provider.Client(), budget: &liveBudget{path: filepath.Join(directory, "budget.json")}, path: filepath.Join(directory, "live.json")}
	gateway := newLiveGateway(t, suite, compiled)
	gatewayService, err := gateway.server.protocolService()
	if err != nil {
		t.Fatal(err)
	}
	for _, isStream := range []bool{false, true} {
		request, err := compiled.EncodeRequest(t.Context(), liveRequest(t, "grp", isStream), protocol.EvaluationContext{Scope: gateway.scope})
		if err != nil {
			t.Fatal(err)
		}
		ctx, downstream := adminProtocolContext("POST", "/chat/completions", string(request))
		gateway.server.serveProtocolRequest(ctx, gatewayService.View(), compiled, "/chat/completions", request)
		if downstream.Code != http.StatusOK || downstream.Result().Trailer.Get(gatewayStreamErrorTrailer) != "" {
			t.Fatal(downstream.Body)
		}
		saved := gateway.stored(t)
		<-gateway.completed
		response, err := decodeLiveResponse(compiled, downstream.Body.Bytes(), isStream, gateway.scope)
		if err != nil {
			t.Fatal(err)
		}
		for _, observed := range []*protocol.Usage{response.Usage, saved.ProtocolUsage} {
			if observed == nil || observed.Input.Count != 100 || observed.CacheRead.Count != 70 || observed.CacheCreation.Count != 20 {
				t.Fatal("declared cache counters did not reach client and SQLite", observed)
			}
		}
		if !strings.Contains(downstream.Body.String(), `"keep":"extension"`) {
			t.Fatal("unrelated native extension was removed")
		}
	}
}
