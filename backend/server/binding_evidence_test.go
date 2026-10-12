package server

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/elysia-api/backend/config"
	"github.com/elysia-api/backend/protocol"
	"github.com/elysia-api/backend/storage"
)

func TestSourceBindingUpdatePersistsInheritedEvidence(t *testing.T) {
	s, _ := newProtocolAdminTestServer(t)
	ingress := activateGatewayDefinition(t, s, loadGatewayDefinition(t, "text-alpha"))
	upstream := activateGatewayDefinition(t, s, loadGatewayDefinition(t, "text-beta"))
	setupGatewayModel(t, s, upstream, "https://example.test")
	s.setupProtocolRevisionRoutes(s.engine.Group("/api/admin"))
	bindings, err := s.store.ListProtocolBindings(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var parent storage.ProtocolBinding
	for _, binding := range bindings {
		if binding.Kind == "source" && binding.SourceID == "gateway-source" {
			parent = binding
		}
	}
	child := parent
	child.Kind, child.ModelID = "model", "upstream-model"
	save := func(binding storage.ProtocolBinding) {
		t.Helper()
		raw, err := json.Marshal(binding)
		if err != nil {
			t.Fatal(err)
		}
		response := revisionAdminRequest(t, s.engine, http.MethodPut, "/api/admin/protocols/bindings", raw, "")
		if response.Code != http.StatusOK {
			t.Fatalf("save=%d %s", response.Code, response.Body)
		}
	}
	assertEvidence := func() {
		t.Helper()
		policies, bindings, _, err := s.store.ConversionSnapshot(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		effective, err := resolveGatewayConversion(policies, bindings, ingress, upstream, config.ModelRef{SourceID: parent.SourceID, ID: child.ModelID}, "generate", protocol.HTTPJSON)
		if err != nil {
			t.Fatal(err)
		}
		for _, binding := range bindings {
			if binding.Key() == child.Key() {
				if binding.Unbound || binding.Binding.RevisionHash != child.Binding.RevisionHash {
					t.Fatal("child contract changed")
				}
				for _, report := range binding.Combinations {
					if report.Passed && report.SourceHash == ingress.Hash() && report.PolicyHash == effective.Hash {
						return
					}
				}
			}
		}
		t.Fatal("child has no evidence matching its inherited policy")
	}
	save(child)
	parent.Conversion = &protocol.ConversionSelection{Overrides: &protocol.ConversionPolicy{SchemaVersion: 1, ID: "override", Rules: []protocol.ConversionRule{{ID: "warning", Order: 2000000, Enabled: true, Phase: protocol.ConversionRequest, Action: "warn", Reason: "test"}}}}
	save(parent)
	assertEvidence()

	// Clearing a source also removes its policy layer. An explicitly bound model
	// stays bound, with evidence for the new inheritance rather than a stale hash.
	if err := s.saveSource(t.Context(), storage.ModelSource{ID: parent.SourceID, Name: "Unbound", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	assertEvidence()
}

func TestRuntimeRefreshRepairsStalePolicyEvidence(t *testing.T) {
	s, _ := newProtocolAdminTestServer(t)
	service, err := s.protocolService()
	if err != nil {
		t.Fatal(err)
	}
	activateGatewayDefinition(t, s, loadGatewayDefinition(t, "text-alpha"))
	upstream := activateGatewayDefinition(t, s, loadGatewayDefinition(t, "text-beta"))
	setupGatewayModel(t, s, upstream, "https://example.test")
	bindings, err := s.store.ListProtocolBindings(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var key storage.BindingKey
	for _, binding := range bindings {
		if binding.Kind == "source" && binding.SourceID == "gateway-source" {
			key = binding.Key()
			for i := range binding.Combinations {
				binding.Combinations[i].PolicyHash = "stale-inherited-policy"
			}
			if err := s.store.SaveProtocolBinding(t.Context(), binding); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := s.refreshProtocolRuntime(t.Context(), service); err != nil {
		t.Fatal(err)
	}
	_, after, generation, err := s.store.ConversionSnapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, binding := range after {
		if binding.Key() == key {
			if !hasPassingGatewayCombination(binding.Combinations) {
				t.Fatal("refresh left source unusable")
			}
			for _, report := range binding.Combinations {
				if report.PolicyHash == "stale-inherited-policy" {
					t.Fatal("refresh ignored policy hash")
				}
			}
		}
	}
	if err := s.refreshProtocolRuntime(t.Context(), service); err != nil {
		t.Fatal(err)
	}
	_, _, next, err := s.store.ConversionSnapshot(t.Context())
	if err != nil || next != generation {
		t.Fatalf("unchanged refresh rewrote evidence: %d -> %d, %v", generation, next, err)
	}
}
