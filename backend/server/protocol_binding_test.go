package server

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/elysia-api/backend/protocol"
	"github.com/elysia-api/backend/storage"
)

func TestProtocolBindingAPIGeneratesCurrentReportsAndRejectsInvalidClaims(t *testing.T) {
	server, _ := newProtocolAdminTestServer(t)
	ingress := activateGatewayDefinition(t, server, loadGatewayDefinition(t, "text-alpha"))
	upstream := activateGatewayDefinition(t, server, loadGatewayDefinition(t, "text-beta"))
	setupGatewayModel(t, server, upstream, "https://example.test")
	server.setupProtocolRevisionRoutes(server.engine.Group("/api/admin"))
	binding := storage.ProtocolBinding{Kind: "model", SourceID: "gateway-source", ModelID: "upstream-model", Binding: protocol.Binding{ProtocolID: "text-beta", RevisionHash: upstream.Hash(), Capabilities: protocol.CapabilitySet{protocol.TextCapability: true}, Transports: []protocol.Transport{protocol.HTTPJSON}}, Combinations: []protocol.CombinationReport{{Passed: true, SourceHash: "forged", TargetHash: "forged"}}}
	raw, _ := json.Marshal(binding)
	result := revisionAdminRequest(t, server.engine, http.MethodPut, "/api/admin/protocols/bindings", raw, "")
	if result.Code != 200 {
		t.Fatalf("binding: %d %s", result.Code, result.Body)
	}
	rows, err := server.store.ListProtocolBindings(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, row := range rows {
		if row.Kind != "model" {
			continue
		}
		for _, report := range row.Combinations {
			if report.SourceHash == "forged" {
				t.Fatal("client report was persisted")
			}
			found = found || report.Passed && report.SourceHash == ingress.Hash() && report.TargetHash == upstream.Hash() && report.CompilerVersion == protocol.CompilerVersion
		}
	}
	if !found {
		t.Fatalf("current paired evidence absent: %+v", rows)
	}
	binding.Binding.Capabilities[protocol.FunctionToolsCapability] = true
	raw, _ = json.Marshal(binding)
	result = revisionAdminRequest(t, server.engine, http.MethodPut, "/api/admin/protocols/bindings", raw, "")
	if result.Code != 400 {
		t.Fatalf("text-only protocol claimed tools: %d %s", result.Code, result.Body)
	}
}
