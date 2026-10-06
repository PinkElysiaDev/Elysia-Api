package server

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/elysia-api/backend/protocol"
	"github.com/elysia-api/backend/storage"
)

func persistPreviousRevision(t *testing.T, server *Server, definition protocol.Value) *protocol.Compiled {
	t.Helper()
	service, err := server.protocolService()
	if err != nil {
		t.Fatal(err)
	}
	compiled, issues := service.Validate(definition.Bytes())
	if err := protocol.IssuesError(issues); err != nil {
		t.Fatal(err)
	}
	id, hash := compiled.Identity().DefinitionID, compiled.Hash()
	revision := protocol.Revision{ProtocolID: id, Hash: hash, Definition: definition, CreatedAt: time.Now().UTC()}
	if err := server.store.SaveProtocolRevision(t.Context(), revision); err != nil {
		t.Fatal(err)
	}
	report := protocol.VerificationReport{DefinitionHash: hash, CompilerVersion: "2.0.0-dev.7", SamplesHash: compiled.SamplesHash(), Kind: protocol.OfflineVerification, Passed: true, VerifiedAt: time.Now().UTC()}
	if err := server.store.SaveProtocolReport(t.Context(), id, hash, report); err != nil {
		t.Fatal(err)
	}
	if _, err := server.store.SetProtocolActivation(t.Context(), id, hash, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := server.store.SaveProtocolDraft(t.Context(), protocol.Draft{ProtocolID: id, Hash: hash, Definition: definition, UpdatedAt: time.Now().UTC()}, ""); err != nil {
		t.Fatal(err)
	}
	return compiled
}

func TestRuntimeRefreshUpgradesOnlyKnownPresetsAndPreservesDrafts(t *testing.T) {
	server, _ := newProtocolAdminTestServer(t)
	service, err := server.protocolService()
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"chat-completions-api", "anthropic-api", "responses-api", "gemini-api"} {
		raw, err := os.ReadFile(filepath.Join("..", "protocol", "builtin", "testdata", "previous", id+".json"))
		if err != nil {
			t.Fatal(err)
		}
		old := persistPreviousRevision(t, server, mustProtocolValue(t, string(raw)))
		if id == "responses-api" {
			edited := old.Definition()
			edited.Name = "unfinished operator draft"
			value := mustEncodedProtocolValue(t, edited)
			if _, _, err := service.SaveDraft(t.Context(), id, value.Bytes(), old.Hash()); err != nil {
				t.Fatal(err)
			}
		}
	}
	bindings := []storage.ProtocolBinding{{Kind: "source", SourceID: "model-source", Binding: protocol.Binding{ProtocolID: "chat-completions-api", Capabilities: protocol.CapabilitySet{protocol.TextCapability: true, protocol.UsageCapability: true}, Transports: []protocol.Transport{protocol.HTTPJSON}}}}
	if err := server.store.SaveProtocolBinding(t.Context(), bindings[0]); err != nil {
		t.Fatal(err)
	}
	draftBefore, err := server.store.ReadProtocolDraft(t.Context(), "responses-api")
	if err != nil {
		t.Fatal(err)
	}
	if err := server.reloadProtocolRuntime(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, id := range service.View().IDs() {
		compiled, _ := service.Pin(id)
		// Derive the expected revision from the shipped definition instead of a
		// hardcoded string: built-in presets version independently, so only the
		// ones whose content changed this cycle carry a new revision.
		if compiled.Identity().Revision != presetDefinition(t, id).Version {
			t.Fatalf("preset not upgraded: %v", compiled.Identity())
		}
		report, err := server.store.ReadProtocolReport(t.Context(), id, compiled.Hash())
		if err != nil || protocol.IssuesError(protocol.CanActivate(compiled, report)) != nil {
			t.Fatal("missing current-engine evidence", err, report)
		}
	}
	draftAfter, err := server.store.ReadProtocolDraft(t.Context(), "responses-api")
	if err != nil || !reflect.DeepEqual(draftBefore, draftAfter) {
		t.Fatal("operator draft changed", err)
	}
	bindings, err = server.store.ListProtocolBindings(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	active, _ := service.Pin("chat-completions-api")
	if bindings[0].Binding.RevisionHash != active.Hash() || !hasPassingGatewayCombination(bindings[0].Combinations) {
		t.Fatal("binding not refreshed atomically")
	}
	before, err := server.store.ListProtocolActivations(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := server.reloadProtocolRuntime(t.Context()); err != nil {
		t.Fatal(err)
	}
	after, err := server.store.ListProtocolActivations(t.Context())
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("restart repeated content upgrade", err)
	}
}

func TestRuntimeRefreshPreservesCustomRevisionsAndBlocksInvalidEvidence(t *testing.T) {
	for _, isInvalid := range []bool{false, true} {
		t.Run(map[bool]string{false: "preserve", true: "block"}[isInvalid], func(t *testing.T) {
			server, _ := newProtocolAdminTestServer(t)
			definition := loadGatewayDefinition(t, "text-alpha")
			definition.ID = "chat-completions-api"
			definition.Name = "operator-owned protocol"
			if isInvalid {
				definition.Samples[0].Expected = mustProtocolValue(t, `{"schemaVersion":1,"content":[]}`)
			}
			old := persistPreviousRevision(t, server, mustEncodedProtocolValue(t, definition))
			before, err := server.store.ListProtocolActivations(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			err = server.reloadProtocolRuntime(t.Context())
			if isInvalid {
				if err == nil {
					t.Fatal("old report authorized invalid definition")
				}
				after, readErr := server.store.ListProtocolActivations(t.Context())
				if readErr != nil || !reflect.DeepEqual(before, after) {
					t.Fatal("failed refresh changed persistence", readErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			service, _ := server.protocolService()
			actual, exists := service.Pin(definition.ID)
			if !exists || actual.Hash() != old.Hash() || len(service.View().IDs()) != 1 {
				t.Fatal("custom content replaced or inactive presets enabled")
			}
		})
	}
}
