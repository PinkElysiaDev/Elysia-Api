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
	for _, id := range []string{"openai-chat-completions", "anthropic-messages", "openai-responses", "google-generate-content"} {
		raw, err := os.ReadFile(filepath.Join("..", "protocol", "builtin", "testdata", "previous", id+".json"))
		if err != nil {
			t.Fatal(err)
		}
		old := persistPreviousRevision(t, server, mustProtocolValue(t, string(raw)))
		if id == "openai-responses" {
			edited := old.Definition()
			edited.Name = "unfinished operator draft"
			edited.ID = "openai-responses-operator"
			value := mustEncodedProtocolValue(t, edited)
			if _, _, err := service.SaveDraft(t.Context(), "openai-responses-operator", value.Bytes(), ""); err != nil {
				t.Fatal(err)
			}
		}
	}
	bindings := []storage.ProtocolBinding{{Kind: "source", SourceID: "model-source", Binding: protocol.Binding{ProtocolID: "openai-chat-completions", Capabilities: protocol.CapabilitySet{protocol.TextCapability: true, protocol.UsageCapability: true}, Transports: []protocol.Transport{protocol.HTTPJSON}}}}
	if err := server.store.SaveProtocolBinding(t.Context(), bindings[0]); err != nil {
		t.Fatal(err)
	}
	draftBefore, err := server.store.ReadProtocolDraft(t.Context(), "openai-responses-operator")
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
	draftAfter, err := server.store.ReadProtocolDraft(t.Context(), "openai-responses-operator")
	if err != nil || !reflect.DeepEqual(draftBefore, draftAfter) {
		t.Fatal("operator draft changed", err)
	}
	bindings, err = server.store.ListProtocolBindings(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	active, _ := service.Pin("openai-chat-completions")
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
				if err != nil {
					t.Fatal("custom failure blocked healthy presets", err)
				}
				service, _ := server.protocolService()
				if _, ok := service.Pin(definition.ID); ok {
					t.Fatal("invalid custom revision loaded")
				}
				if service.View().Failures()[definition.ID] == "" {
					t.Fatal("missing isolation reason")
				}
				after, readErr := server.store.ListProtocolActivations(t.Context())
				if readErr != nil {
					t.Fatal(readErr)
				}
				for _, activation := range after {
					if activation.ProtocolID == definition.ID && !reflect.DeepEqual(activation, before[0]) {
						t.Fatal("custom activation intent changed")
					}
				}
				if len(service.View().IDs()) != 4 {
					t.Fatal("healthy presets were not recovered")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			service, _ := server.protocolService()
			actual, exists := service.Pin(definition.ID)
			if !exists || actual.Hash() != old.Hash() {
				t.Fatal("custom content replaced")
			}
			// 预置属引擎所有：即便此前从未激活（测试服务器未走完整升级链），
			// 刷新也会自愈补激活（现实里对应改名半途等混合状态）。
			for _, id := range []string{"openai-chat-completions", "openai-responses", "anthropic-messages", "google-generate-content"} {
				if _, ok := service.Pin(id); !ok {
					t.Fatalf("preset %s not self-healed to active", id)
				}
			}
		})
	}
}

func TestRuntimeRefreshResetsPresetRevisionsToShipped(t *testing.T) {
	server, _ := newProtocolAdminTestServer(t)
	// 在预置 ID 下持久化一份「运营者持有」修订（带无效样例）：预置只读策略
	// 下，启动刷新无条件回归 shipped 版本并重建证据，而不是被旧修订阻断。
	definition := loadGatewayDefinition(t, "text-alpha")
	definition.ID = "openai-chat-completions"
	definition.Name = "operator-owned protocol"
	definition.Samples[0].Expected = mustProtocolValue(t, `{"schemaVersion":1,"content":[]}`)
	persistPreviousRevision(t, server, mustEncodedProtocolValue(t, definition))
	if err := server.reloadProtocolRuntime(t.Context()); err != nil {
		t.Fatal(err)
	}
	service, err := server.protocolService()
	if err != nil {
		t.Fatal(err)
	}
	active, exists := service.Pin("openai-chat-completions")
	if !exists {
		t.Fatal("preset not active after refresh")
	}
	shipped := presetDefinition(t, "openai-chat-completions")
	if active.Definition().Name == "operator-owned protocol" || active.Identity().Revision != shipped.Version {
		t.Fatal("operator revision under a preset ID survived refresh", active.Identity())
	}
	report, err := server.store.ReadProtocolReport(t.Context(), "openai-chat-completions", active.Hash())
	if err != nil || !report.Passed {
		t.Fatal("shipped replacement lacks current-engine evidence", err, report)
	}
}

func TestRuntimeProjectionUpgradeTenRestartsPreserveCustomAndFailedEvidence(t *testing.T) {
	s, path := newProtocolAdminTestServer(t)
	for _, id := range []string{"old-copy", "broken-copy"} {
		d := presetDefinition(t, protocol.PresetResponsesID)
		d.ID = id
		d.Requires = nil
		if id == "broken-copy" {
			d.Samples[0].Expected = mustProtocolValue(t, `{"schemaVersion":1,"content":[]}`)
		}
		persistPreviousRevision(t, s, mustEncodedProtocolValue(t, d))
	}
	if err := s.reloadProtocolRuntime(t.Context()); err != nil {
		t.Fatal(err)
	}
	before, err := s.store.ProtocolUpgradeBaseline(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	evidenceBefore, err := s.store.ProtocolRefreshEvidenceBaseline(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	backups, err := filepath.Glob(filepath.Join(filepath.Dir(path), "*.protocol-snapshot-v2-*"))
	if err != nil {
		t.Fatal(err)
	}
	// These current definitions only need new evidence and missing presets.
	// No existing draft, activation, revision or binding is overwritten.
	if len(backups) != 0 {
		t.Fatal("append-only refresh created an unnecessary snapshot", backups)
	}
	for i := 0; i < 10; i++ {
		if err := s.reloadProtocolRuntime(t.Context()); err != nil {
			t.Fatal(err)
		}
		after, err := s.store.ProtocolUpgradeBaseline(t.Context())
		if err != nil || before != after {
			t.Fatal("restart changed protocol state", i, err)
		}
		evidenceAfter, err := s.store.ProtocolRefreshEvidenceBaseline(t.Context())
		if err != nil || evidenceBefore != evidenceAfter {
			t.Fatal("restart changed verification evidence", i, err)
		}
		current, err := filepath.Glob(filepath.Join(filepath.Dir(path), "*.protocol-snapshot-v2-*"))
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(backups, current) {
			t.Fatal("restart added backup", i)
		}
	}
	service, _ := s.protocolService()
	if _, ok := service.Pin("old-copy"); !ok {
		t.Fatal("old custom was not automatically reverified")
	}
	if _, ok := service.Pin("broken-copy"); ok {
		t.Fatal("invalid custom became executable")
	}
	if service.View().Failures()["broken-copy"] == "" {
		t.Fatal("missing repair diagnosis")
	}
}
