package server

import (
	"net/http"
	"testing"

	"github.com/bytedance/sonic"
	"github.com/elysia-api/backend/protocol"
	"github.com/elysia-api/backend/storage"
)

func TestConversionActivationKeepsPinnedRevision(t *testing.T) {
	s, _ := newProtocolAdminTestServer(t)
	activateGatewayDefinition(t, s, loadGatewayDefinition(t, "text-alpha"))
	upstream := activateGatewayDefinition(t, s, loadGatewayDefinition(t, "text-beta"))
	setupGatewayModel(t, s, upstream, "https://example.test")
	s.setupProtocolRevisionRoutes(s.engine.Group("/api/admin"))
	var previous string
	var revisions []storage.ConversionPolicyRecord
	for _, name := range []string{"first", "second"} {
		p := protocol.ConversionPolicy{SchemaVersion: 1, ID: "review-pinned", Name: name, Rules: []protocol.ConversionRule{}}
		r, err := s.store.SaveConversionDraft(t.Context(), p, previous)
		if err != nil {
			t.Fatal(err)
		}
		previous = r.Hash
		raw, _ := sonic.ConfigStd.Marshal(map[string]string{"hash": r.Hash})
		rr := revisionAdminRequest(t, s.engine, http.MethodPost, "/api/admin/protocols/conversion-policies/review-pinned/verify", raw, "")
		if rr.Code != 200 {
			t.Fatalf("verify=%d %s", rr.Code, rr.Body)
		}
		revisions = append(revisions, r)
	}
	bindings, _ := s.store.ListProtocolBindings(t.Context())
	for _, b := range bindings {
		if b.Kind == "source" && b.SourceID == "gateway-source" {
			b.Conversion = &protocol.ConversionSelection{PolicyID: revisions[0].ID, RevisionHash: revisions[0].Hash}
			raw, _ := sonic.ConfigStd.Marshal(b)
			r := revisionAdminRequest(t, s.engine, http.MethodPut, "/api/admin/protocols/bindings", raw, "")
			if r.Code != 200 {
				t.Fatalf("bind=%d %s", r.Code, r.Body)
			}
		}
	}
	raw, _ := sonic.ConfigStd.Marshal(map[string]any{"hash": revisions[1].Hash, "expectedActive": "", "selector": map[string]string{}})
	r := revisionAdminRequest(t, s.engine, http.MethodPost, "/api/admin/protocols/conversion-policies/review-pinned/activate", raw, "")
	if r.Code != 200 {
		t.Fatalf("activation=%d %s", r.Code, r.Body)
	}
	policies, bindings, _, err := s.store.ConversionSnapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(policies) != 2 {
		t.Fatalf("revision directory lost the active or pinned revision: %d", len(policies))
	}
	for _, policy := range policies {
		if (policy.ActiveHash != "") != (policy.Hash == revisions[1].Hash) {
			t.Fatalf("wrong active selection: %+v", policy.Key())
		}
	}
	for _, binding := range bindings {
		if binding.Kind == "source" && binding.SourceID == "gateway-source" {
			if binding.Conversion.RevisionHash != revisions[0].Hash || !hasPassingGatewayCombination(binding.Combinations) {
				t.Fatal("activation changed the explicit pin or lost its evidence")
			}
		}
	}
}
