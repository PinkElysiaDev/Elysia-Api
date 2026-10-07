package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/elysia-api/backend/protocol"
	"github.com/elysia-api/backend/storage"
)

func TestProtocolHistoryAdminArchiveRestoreAndDelete(t *testing.T) {
	s, _ := newProtocolAdminTestServer(t)
	s.setupProtocolRevisionRoutes(s.engine.Group("/api/admin"))
	compiled := activateGatewayDefinition(t, s, loadGatewayDefinition(t, "text-alpha"))
	oldView := s.protocolServiceInst.View()
	source := storage.ModelSource{ID: "history-source", Name: "History", BaseURL: "https://example.invalid", Platform: "custom:text-alpha", Enabled: true}
	if err := s.store.UpsertSource(t.Context(), source); err != nil {
		t.Fatal(err)
	}
	if err := s.store.ReplaceSourceModels(t.Context(), source, []storage.Model{{ID: "m", Name: "m", Enabled: true}}); err != nil {
		t.Fatal(err)
	}
	if err := s.store.SaveProtocolBinding(t.Context(), makeProtocolBinding(upgradeBindingKey{kind: "source", source: source.ID}, compiled)); err != nil {
		t.Fatal(err)
	}
	refs := revisionAdminRequest(t, s.engine, "GET", "/api/admin/protocols/text-alpha/references", nil, "")
	data := decodeAdminData(t, refs)
	baseline := data["baseline"].(string)
	if len(data["references"].([]any)) == 0 {
		t.Fatal("references missing")
	}
	archive := func(mode, target, baseline string) int {
		body, _ := json.Marshal(map[string]string{"mode": mode, "targetProtocolId": target, "baseline": baseline})
		return revisionAdminRequest(t, s.engine, "POST", "/api/admin/protocols/text-alpha/archive", body, "").Code
	}
	if archive("block", "", baseline) != http.StatusConflict {
		t.Fatal("referenced deletion allowed")
	}
	if archive("replace", "missing", baseline) != 400 {
		t.Fatal("invalid replacement accepted")
	}
	if _, ok := s.protocolServiceInst.Pin("text-alpha"); !ok {
		t.Fatal("failed archive removed active revision")
	}
	if archive("unbind", "", "stale") != 409 {
		t.Fatal("stale archive accepted")
	}
	if archive("unbind", "", baseline) != 200 {
		t.Fatal("unbind archive failed")
	}
	if _, ok := oldView.Pin("text-alpha"); !ok {
		t.Fatal("in-flight view mutated")
	}
	if _, ok := s.protocolServiceInst.Pin("text-alpha"); ok {
		t.Fatal("still active")
	}
	bindings, _ := s.store.ListProtocolBindings(t.Context())
	models, _ := s.store.ListModelsFiltered(t.Context(), storage.ModelListFilter{})
	if _, failure := makeGatewayCandidate(s.protocolServiceInst.View(), bindings, modelReference(models[0]), protocol.HTTPJSON, "generate"); failure == nil {
		t.Fatal("unbound model inherited a binding")
	}
	sources, _ := s.store.ListSources(t.Context())
	if sources[0].Platform != "" {
		t.Fatal("source platform not cleared")
	}
	if err := s.reloadProtocolRuntime(t.Context()); err != nil {
		t.Fatal(err)
	}
	items, _ := s.store.ListProtocolHistory(t.Context())
	if len(items) == 0 {
		t.Fatal("no archive")
	}
	item := items[0]
	restorePath := "/api/admin/protocols/history/" + item.ID + "/restore"
	restored := revisionAdminRequest(t, s.engine, "POST", restorePath, []byte(`{"id":"restored-alpha","name":"恢复"}`), "")
	if restored.Code != 200 || decodeAdminData(t, restored)["activated"] != true {
		t.Fatal(restored.Code, restored.Body)
	}
	if revisionAdminRequest(t, s.engine, "POST", restorePath, []byte(`{"id":"restored-alpha"}`), "").Code != 409 {
		t.Fatal("duplicate restore overwrote draft")
	}
	if _, err := s.store.ReadProtocolHistory(t.Context(), item.ID); err != nil {
		t.Fatal("restore consumed archive", err)
	}
	release := s.protocolUses.acquire(item.ProtocolID, item.Hash)
	deletePath := "/api/admin/protocols/history/" + item.ID
	if revisionAdminRequest(t, s.engine, "DELETE", deletePath, nil, "").Code != 409 {
		t.Fatal("purged live session revision")
	}
	release()
	if response := revisionAdminRequest(t, s.engine, "DELETE", deletePath, nil, ""); response.Code != 200 {
		t.Fatal(response.Code, response.Body)
	}
	if _, err := s.store.ReadProtocolHistory(t.Context(), item.ID); !errors.Is(err, protocol.ErrNotFound) {
		t.Fatal(err)
	}
}

func TestProtocolHistoryRestoreInvalidDraftPreservesEditableCopy(t *testing.T) {
	s, _ := newProtocolAdminTestServer(t)
	s.setupProtocolRevisionRoutes(s.engine.Group("/api/admin"))
	// Saved drafts are allowed to be invalid; restoration must retain them for repair.
	value, err := protocol.ParseValue([]byte(`{"schemaVersion":2,"id":"broken","name":"Broken","version":"1","family":"custom","wireVersion":"1","capabilities":{"text":true}}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.store.SaveProtocolDraft(t.Context(), protocol.Draft{ProtocolID: "broken", Hash: "broken-hash", Definition: value, UpdatedAt: time.Now()}, ""); err != nil {
		t.Fatal(err)
	}
	baseline, _ := s.store.ProtocolUpgradeBaseline(t.Context())
	if err := s.store.ArchiveProtocol(t.Context(), "broken", baseline, "", nil); err != nil {
		t.Fatal(err)
	}
	response := revisionAdminRequest(t, s.engine, "POST", "/api/admin/protocols/history/broken~broken-hash/restore", []byte(`{"id":"repair-copy"}`), "")
	if response.Code != 200 || decodeAdminData(t, response)["activated"] != false {
		t.Fatal(response.Code, response.Body)
	}
	if _, err := s.store.ReadProtocolDraft(t.Context(), "repair-copy"); err != nil {
		t.Fatal("editable draft missing", err)
	}
	if _, ok := s.protocolServiceInst.Pin("repair-copy"); ok {
		t.Fatal("invalid restoration activated")
	}
	if len(decodeAdminData(t, response)["issues"].([]any)) == 0 {
		t.Fatal("missing repair diagnostics")
	}
}

func TestProtocolHistoryReplacementFailureRollsBackAllReferences(t *testing.T) {
	s, _ := newProtocolAdminTestServer(t)
	s.setupProtocolRevisionRoutes(s.engine.Group("/api/admin"))
	old := activateGatewayDefinition(t, s, presetDefinition(t, "anthropic-messages"))
	definition := old.Definition()
	definition.ID = "tools-custom"
	old = activateGatewayDefinition(t, s, definition)
	next := activateGatewayDefinition(t, s, loadGatewayDefinition(t, "text-alpha"))
	for _, id := range []string{"source-a", "source-b"} {
		if err := s.store.UpsertSource(t.Context(), storage.ModelSource{ID: id, Name: id, Platform: "custom:tools-custom", Enabled: true}); err != nil {
			t.Fatal(err)
		}
		binding := makeProtocolBinding(upgradeBindingKey{kind: "source", source: id}, old)
		if err := s.store.SaveProtocolBinding(t.Context(), binding); err != nil {
			t.Fatal(err)
		}
	}
	before, _ := s.store.ProtocolUpgradeBaseline(t.Context())
	body, _ := json.Marshal(map[string]string{"mode": "replace", "targetProtocolId": next.Identity().DefinitionID, "baseline": before})
	response := revisionAdminRequest(t, s.engine, "POST", "/api/admin/protocols/tools-custom/archive", body, "")
	if response.Code != 400 {
		t.Fatal(response.Code, response.Body)
	}
	after, _ := s.store.ProtocolUpgradeBaseline(t.Context())
	if before != after {
		t.Fatal("failed replacement changed persisted state")
	}
	if _, ok := s.protocolServiceInst.Pin("tools-custom"); !ok {
		t.Fatal("failed replacement changed runtime")
	}
}

func TestProtocolHistoryReplacementPreservesReferences(t *testing.T) {
	s, _ := newProtocolAdminTestServer(t)
	s.setupProtocolRevisionRoutes(s.engine.Group("/api/admin"))
	old := activateGatewayDefinition(t, s, loadGatewayDefinition(t, "text-alpha"))
	definition := old.Definition()
	definition.ID = "replacement-alpha"
	next := activateGatewayDefinition(t, s, definition)
	source := storage.ModelSource{ID: "s", Name: "s", BaseURL: "https://example.invalid", Platform: "custom:text-alpha", Enabled: true}
	if err := s.store.UpsertSource(t.Context(), source); err != nil {
		t.Fatal(err)
	}
	if err := s.store.SaveProtocolBinding(t.Context(), makeProtocolBinding(upgradeBindingKey{kind: "source", source: "s"}, old)); err != nil {
		t.Fatal(err)
	}
	baseline, _ := s.store.ProtocolUpgradeBaseline(t.Context())
	body, _ := json.Marshal(map[string]string{"mode": "replace", "targetProtocolId": next.Identity().DefinitionID, "baseline": baseline})
	response := revisionAdminRequest(t, s.engine, "POST", "/api/admin/protocols/text-alpha/archive", body, "")
	if response.Code != 200 {
		t.Fatal(response.Code, response.Body)
	}
	bindings, _ := s.store.ListProtocolBindings(t.Context())
	if len(bindings) != 1 || bindings[0].Binding.ProtocolID != next.Identity().DefinitionID {
		t.Fatal(bindings)
	}
	sources, _ := s.store.ListSources(t.Context())
	if sources[0].Platform != "custom:replacement-alpha" {
		t.Fatal(sources)
	}
}

func TestProtocolHistoryModelUnbindAndExplicitRebind(t *testing.T) {
	s, _ := newProtocolAdminTestServer(t)
	s.setupProtocolRevisionRoutes(s.engine.Group("/api/admin"))
	compiled := activateGatewayDefinition(t, s, loadGatewayDefinition(t, "text-alpha"))
	source := storage.ModelSource{ID: "s", Name: "s", Platform: "custom:text-alpha", Enabled: true}
	if err := s.store.UpsertSource(t.Context(), source); err != nil {
		t.Fatal(err)
	}
	if err := s.store.ReplaceSourceModels(t.Context(), source, []storage.Model{{ID: "m", Name: "m", Enabled: true}}); err != nil {
		t.Fatal(err)
	}
	if err := s.store.SaveProtocolBinding(t.Context(), makeProtocolBinding(upgradeBindingKey{kind: "source", source: "s"}, compiled)); err != nil {
		t.Fatal(err)
	}
	save := func(entry storage.ProtocolBinding) {
		t.Helper()
		body, _ := json.Marshal(entry)
		response := revisionAdminRequest(t, s.engine, "PUT", "/api/admin/protocols/bindings", body, "")
		if response.Code != 200 {
			t.Fatal(response.Code, response.Body)
		}
	}
	save(storage.ProtocolBinding{Kind: "model", SourceID: "s", ModelID: "m", Unbound: true})
	bindings, _ := s.store.ListProtocolBindings(t.Context())
	models, _ := s.store.ListModelsFiltered(t.Context(), storage.ModelListFilter{})
	if models[0].Platform != "" {
		t.Fatal("unbound model label not cleared")
	}
	if _, failure := makeGatewayCandidate(s.protocolServiceInst.View(), bindings, modelReference(models[0]), protocol.HTTPJSON, "generate"); failure == nil {
		t.Fatal("model fell back to source")
	}
	save(makeProtocolBinding(upgradeBindingKey{kind: "model", source: "s", model: "m"}, compiled))
	bindings, _ = s.store.ListProtocolBindings(t.Context())
	models, _ = s.store.ListModelsFiltered(t.Context(), storage.ModelListFilter{})
	if models[0].Platform != "custom:text-alpha" {
		t.Fatal("rebound model label not updated")
	}
	if _, failure := makeGatewayCandidate(s.protocolServiceInst.View(), bindings, modelReference(models[0]), protocol.HTTPJSON, "generate"); failure != nil {
		t.Fatal(failure)
	}
}
