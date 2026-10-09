package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/elysia-api/backend/protocol"
	"github.com/elysia-api/backend/storage"
)

func recoveryDB(t *testing.T, configPath string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(filepath.Dir(configPath), "test.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func recoveryExec(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatal(err)
	}
}

func recoveryCounts(t *testing.T, db *sql.DB) map[string]int {
	t.Helper()
	counts := map[string]int{}
	for _, table := range []string{"protocol_revisions", "protocol_drafts", "protocol_activations", "protocol_verification_reports", "protocol_history", "protocol_bindings"} {
		var count int
		if err := db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		counts[table] = count
	}
	return counts
}

func assertRecoveredPresets(t *testing.T, s *Server) {
	t.Helper()
	service, err := s.protocolService()
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range requiredPresetIDs {
		compiled, ok := service.Pin(id)
		if !ok {
			t.Fatalf("missing runtime preset %s", id)
		}
		expected := compileFixtureDefinition(t, presetDefinition(t, id))
		if compiled.Hash() != expected.Hash() || compiled.Identity().DefinitionID != id {
			t.Fatalf("invalid preset identity: %s", id)
		}
		report, err := s.store.ReadProtocolReport(t.Context(), id, compiled.Hash())
		if err != nil || protocol.IssuesError(protocol.CanActivate(compiled, report)) != nil {
			t.Fatalf("invalid evidence for %s: %v", id, err)
		}
	}
}

func TestPresetRecoveryDefectsAndIdempotence(t *testing.T) {
	for _, defect := range []string{"all-inactive", "one-inactive", "report-missing", "report-malformed", "report-stale", "report-rejected", "revision-missing", "revision-malformed", "revision-wrong-id", "draft-malformed", "activation-invalid"} {
		t.Run(defect, func(t *testing.T) {
			s, path := newProtocolAdminTestServer(t)
			if err := s.reloadProtocolRuntime(t.Context()); err != nil {
				t.Fatal(err)
			}
			db := recoveryDB(t, path)
			seedUpgradeReceipt(t, filepath.Join(filepath.Dir(path), "test.sqlite3"))
			beforeCounts := recoveryCounts(t, db)
			beforeActive, _ := s.store.ListProtocolActivations(t.Context())
			id := protocol.PresetAnthropicID
			service, _ := s.protocolService()
			previous, _ := service.Pin(id)
			raw := "{damaged raw definition"
			switch defect {
			case "all-inactive":
				recoveryExec(t, db, `DELETE FROM protocol_activations`)
			case "one-inactive":
				recoveryExec(t, db, `DELETE FROM protocol_activations WHERE protocol_id=?`, id)
			case "report-missing":
				recoveryExec(t, db, `DELETE FROM protocol_verification_reports WHERE protocol_id=?`, id)
			case "report-malformed":
				recoveryExec(t, db, `UPDATE protocol_verification_reports SET report='{' WHERE protocol_id=?`, id)
			case "report-stale":
				recoveryExec(t, db, `UPDATE protocol_verification_reports SET compiler_version='old' WHERE protocol_id=?`, id)
			case "report-rejected":
				recoveryExec(t, db, `UPDATE protocol_verification_reports SET report=json_set(report,'$.passed',json('false')) WHERE protocol_id=?`, id)
			case "revision-missing":
				recoveryExec(t, db, `DELETE FROM protocol_revisions WHERE protocol_id=?`, id)
			case "revision-malformed":
				recoveryExec(t, db, `UPDATE protocol_revisions SET definition=? WHERE protocol_id=?`, raw, id)
			case "revision-wrong-id":
				recoveryExec(t, db, `UPDATE protocol_revisions SET definition=json_set(definition,'$.id','other') WHERE protocol_id=?`, id)
				if err := db.QueryRow(`SELECT definition FROM protocol_revisions WHERE protocol_id=?`, id).Scan(&raw); err != nil {
					t.Fatal(err)
				}
			case "draft-malformed":
				recoveryExec(t, db, `UPDATE protocol_drafts SET definition=? WHERE protocol_id=?`, raw, id)
			case "activation-invalid":
				recoveryExec(t, db, `UPDATE protocol_activations SET generation=0 WHERE protocol_id=?`, id)
			}
			if err := s.initializeProtocolRuntime(t.Context()); err != nil {
				t.Fatal(err)
			}
			assertRecoveredPresets(t, s)
			if s.protocolRuntimeError() != nil {
				t.Fatal(s.protocolRuntimeError())
			}
			afterActive, _ := s.store.ListProtocolActivations(t.Context())
			for _, a := range afterActive {
				for _, old := range beforeActive {
					if a.ProtocolID == old.ProtocolID && defect != "all-inactive" && (a.ProtocolID != id || (defect != "one-inactive" && defect != "activation-invalid")) && !reflect.DeepEqual(a, old) {
						t.Fatal("unrelated activation changed", a, old)
					}
				}
			}
			if strings.HasSuffix(defect, "inactive") && recoveryCounts(t, db)["protocol_verification_reports"] != beforeCounts["protocol_verification_reports"] {
				t.Fatal("activation repair duplicated valid reports")
			}
			if defect == "revision-malformed" || defect == "revision-wrong-id" || defect == "draft-malformed" {
				history, err := s.store.ListProtocolHistory(t.Context())
				if err != nil || len(history) != 1 || history[0].RawDefinition != raw || history[0].Hash != previous.Hash() {
					t.Fatal("raw corruption not retained", history, err)
				}
				s.setupProtocolRevisionRoutes(s.engine.Group("/api/admin"))
				r := revisionAdminRequest(t, s.engine, http.MethodGet, "/api/admin/protocols/history/"+history[0].ID, nil, "")
				if r.Code != http.StatusOK || !strings.Contains(r.Body.String(), "rawDefinition") {
					t.Fatal("damaged history not inspectable", r.Code, r.Body)
				}
			}
			baseline, _ := s.store.ProtocolUpgradeBaseline(t.Context())
			counts := recoveryCounts(t, db)
			backups, _ := filepath.Glob(filepath.Join(filepath.Dir(path), "*.pre-protocol-v2-*"))
			if err := s.initializeProtocolRuntime(t.Context()); err != nil {
				t.Fatal(err)
			}
			after, _ := s.store.ProtocolUpgradeBaseline(t.Context())
			afterBackups, _ := filepath.Glob(filepath.Join(filepath.Dir(path), "*.pre-protocol-v2-*"))
			if after != baseline || !reflect.DeepEqual(counts, recoveryCounts(t, db)) || !reflect.DeepEqual(backups, afterBackups) {
				t.Fatal("healthy restart wrote protocol state")
			}
		})
	}
}

func TestPresetRecoveryIndependentOfLegacyMigration(t *testing.T) {
	s, path := newProtocolAdminTestServer(t)
	if err := s.store.UpsertCustomProtocol(t.Context(), storage.CustomProtocol{ID: "broken-custom", Config: "{bad"}); err != nil {
		t.Fatal(err)
	}
	if err := s.initializeProtocolRuntime(t.Context()); err == nil {
		t.Fatal("legacy failure should still block generation")
	}
	assertRecoveredPresets(t, s)
	if receipt, err := s.store.ProtocolUpgradeStatus(t.Context()); err != nil || receipt != nil {
		t.Fatal("preset recovery fabricated legacy receipt", receipt, err)
	}
	if s.protocolRuntimeError() == nil {
		t.Fatal("unfinished migration marked ready")
	}
	rows, _ := s.store.ListCustomProtocols(t.Context())
	if len(rows) != 1 || rows[0].Config != "{bad" {
		t.Fatal("custom data overwritten")
	}
	db := recoveryDB(t, path)
	counts := recoveryCounts(t, db)
	if err := s.initializeProtocolRuntime(t.Context()); err == nil {
		t.Fatal("legacy failure disappeared")
	}
	if !reflect.DeepEqual(counts, recoveryCounts(t, db)) {
		t.Fatal("retry repeated preset writes")
	}
}

func TestPresetRecoveryFailureIsAtomic(t *testing.T) {
	for _, failure := range []string{"verification", "transaction", "disk-write", "concurrent", "concurrent-report", "backup", "canceled", "missing-table"} {
		t.Run(failure, func(t *testing.T) {
			s, path := newProtocolAdminTestServer(t)
			db := recoveryDB(t, path)
			service, _ := s.protocolService()
			plan, _, _, err := s.prepareProtocolRefresh(t.Context(), service, true)
			if err != nil {
				t.Fatal(err)
			}
			plan.Baseline, _ = s.store.ProtocolUpgradeBaseline(t.Context())
			plan.EvidenceBaseline, _ = s.store.ProtocolRefreshEvidenceBaseline(t.Context())
			ctx := t.Context()
			switch failure {
			case "verification":
				plan.Revisions[0].Report.Passed = false
			case "transaction":
				recoveryExec(t, db, `CREATE TRIGGER fail_recovery BEFORE INSERT ON protocol_activations WHEN NEW.protocol_id='openai-responses' BEGIN SELECT RAISE(ABORT,'transaction failure'); END`)
			case "disk-write":
				recoveryExec(t, db, `CREATE TRIGGER fail_recovery BEFORE INSERT ON protocol_verification_reports BEGIN SELECT RAISE(ABORT,'database or disk is full'); END`)
			case "concurrent":
				recoveryExec(t, db, `INSERT INTO protocol_drafts VALUES('concurrent','hash','{}','now')`)
			case "concurrent-report":
				recoveryExec(t, db, `INSERT INTO protocol_verification_reports(protocol_id,revision_hash,compiler_version,samples_hash,kind,report,verified_at) VALUES('concurrent','hash','old','hash','offline','{}','now')`)
			case "backup":
				if err := os.Mkdir(filepath.Join(filepath.Dir(path), "test.sqlite3.pre-protocol-v2-"+plan.Baseline), 0700); err != nil {
					t.Fatal(err)
				}
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "missing-table":
				recoveryExec(t, db, `DROP TABLE protocol_history`)
			}
			err = s.store.RefreshProtocolRuntime(ctx, plan)
			if err == nil {
				t.Fatal("injected failure accepted")
			}
			if strings.HasPrefix(failure, "concurrent") && !errors.Is(err, protocol.ErrRevisionConflict) {
				t.Fatal("missing CAS conflict", err)
			}
			active, err := s.store.ListProtocolActivations(t.Context())
			if err != nil || len(active) != 0 {
				t.Fatal("partial activation", active, err)
			}
			var count int
			if err := db.QueryRow(`SELECT COUNT(*) FROM protocol_revisions`).Scan(&count); err != nil || count != 0 {
				t.Fatal("partial revision", count, err)
			}
			if receipt, _ := s.store.ProtocolUpgradeStatus(t.Context()); receipt != nil {
				t.Fatal("partial receipt")
			}
			if len(service.View().IDs()) != 0 || s.protocolRuntimeReady.Load() {
				t.Fatal("failed recovery published runtime")
			}
		})
	}
}

func TestRecoveredPresetsDiscoverModelsWithoutBindingUnboundSources(t *testing.T) {
	s, _ := newProtocolAdminTestServer(t)
	if err := s.initializeProtocolRuntime(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, id := range requiredPresetIDs {
		t.Run(id, func(t *testing.T) {
			var calls atomic.Int32
			expectedPath, basePath := "/v1/models", "/v1"
			if id == protocol.PresetAnthropicID {
				basePath = ""
			}
			if id == protocol.PresetGeminiID {
				basePath, expectedPath = "", "/v1beta/models"
			}
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.URL.Path != expectedPath {
					t.Errorf("unexpected discovery path: %s", r.URL.Path)
				}
				w.Header().Set("Content-Type", "application/json")
				if id == protocol.PresetGeminiID {
					_, _ = w.Write([]byte(`{"models":[{"name":"models/test-model","displayName":"Test","supportedGenerationMethods":["generateContent"]}]}`))
				} else {
					_, _ = w.Write([]byte(`{"data":[{"id":"test-model","type":"model","display_name":"Test"}],"has_more":false}`))
				}
			}))
			defer upstream.Close()
			source := storage.ModelSource{ID: id, Name: id, Platform: "custom:" + id, BaseURL: upstream.URL + basePath, Enabled: true}
			if err := s.store.UpsertSource(t.Context(), source); err != nil {
				t.Fatal(err)
			}
			models, err := s.fetchModelsFromSource(t.Context(), source, "key")
			if err != nil || len(models) != 1 || models[0].ID != "test-model" || calls.Load() != 1 {
				t.Fatal("discovery failed", models, err, calls.Load())
			}
			if err := s.store.SaveManagedProtocolBinding(t.Context(), storage.ProtocolBinding{Kind: "source", SourceID: id, Unbound: true}); err != nil {
				t.Fatal(err)
			}
			if err := s.initializeProtocolRuntime(t.Context()); err != nil {
				t.Fatal(err)
			}
			sources, err := s.store.ListSources(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			for _, stored := range sources {
				if stored.ID == id {
					source = stored
				}
			}
			_, err = s.fetchModelsFromSource(t.Context(), source, "key")
			if err == nil || calls.Load() != 1 {
				t.Fatal("explicitly unbound source fell back", err, calls.Load())
			}
		})
	}
}

func TestPresetRecoveryTenRestartsPreserveEvidenceAndCustomData(t *testing.T) {
	s, path := newProtocolAdminTestServer(t)
	d := presetDefinition(t, protocol.PresetResponsesID)
	d.ID = "my-copy"
	d.Name = "Anthropic Messages"
	persistPreviousRevision(t, s, mustEncodedProtocolValue(t, d))
	if err := s.initializeProtocolRuntime(t.Context()); err != nil {
		t.Fatal(err)
	}
	db := recoveryDB(t, path)
	baseline, _ := s.store.ProtocolUpgradeBaseline(t.Context())
	counts := recoveryCounts(t, db)
	backups, _ := filepath.Glob(filepath.Join(filepath.Dir(path), "*.pre-protocol-v2-*"))
	beforeConfig, _ := os.ReadFile(path)
	for i := 0; i < 10; i++ {
		if err := s.store.Close(); err != nil {
			t.Fatal(err)
		}
		next := &Server{config: s.config}
		var err error
		next.store, err = storage.Open(filepath.Join(filepath.Dir(path), "test.sqlite3"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { next.store.Close() })
		s = next
		if err := s.initializeProtocolRuntime(t.Context()); err != nil {
			t.Fatal(err)
		}
		assertRecoveredPresets(t, s)
		after, _ := s.store.ProtocolUpgradeBaseline(t.Context())
		afterBackups, _ := filepath.Glob(filepath.Join(filepath.Dir(path), "*.pre-protocol-v2-*"))
		afterConfig, _ := os.ReadFile(path)
		if after != baseline || !reflect.DeepEqual(counts, recoveryCounts(t, db)) || !reflect.DeepEqual(backups, afterBackups) || string(beforeConfig) != string(afterConfig) {
			t.Fatal("restart changed persisted state", i)
		}
	}
	service, _ := s.protocolService()
	custom, ok := service.Pin(d.ID)
	if !ok {
		t.Fatal("custom disappeared")
	}
	actual, _ := json.Marshal(custom.Definition())
	expected, _ := json.Marshal(d)
	if string(actual) != string(expected) {
		t.Fatal("same-name custom was overwritten")
	}
}

func TestPresetRecoveryPublicationAndRollbackProtectExistingRuntime(t *testing.T) {
	s, path := newProtocolAdminTestServer(t)
	if err := s.initializeProtocolRuntime(t.Context()); err != nil {
		t.Fatal(err)
	}
	service, _ := s.protocolService()
	pinned := service.View()
	db := recoveryDB(t, path)
	id := protocol.PresetAnthropicID
	recoveryExec(t, db, `DELETE FROM protocol_activations WHERE protocol_id=?`, id)
	if err := service.ReloadAvailable(t.Context(), requiredPresetIDs...); err == nil {
		t.Fatal("partial candidate snapshot was published")
	}
	if _, ok := service.Pin(id); !ok {
		t.Fatal("failed reload replaced original snapshot")
	}
	recoveryExec(t, db, `UPDATE protocol_revisions SET definition='{damaged' WHERE protocol_id=?`, id)
	recoveryExec(t, db, `CREATE TRIGGER reject_repair BEFORE INSERT ON protocol_verification_reports BEGIN SELECT RAISE(ABORT,'repair interrupted'); END`)
	if err := s.reloadProtocolRuntime(t.Context()); err == nil || s.protocolRuntimeError() == nil {
		t.Fatal("failed reload retained core readiness", err)
	}
	var raw string
	if err := db.QueryRow(`SELECT definition FROM protocol_revisions WHERE protocol_id=?`, id).Scan(&raw); err != nil || raw != "{damaged" {
		t.Fatal("repair escaped rollback", raw, err)
	}
	var archives int
	if err := db.QueryRow(`SELECT COUNT(*) FROM protocol_history WHERE reason='preset_repaired'`).Scan(&archives); err != nil || archives != 0 {
		t.Fatal("partial repair archive", archives, err)
	}
	if _, ok := pinned.Pin(id); !ok {
		t.Fatal("in-flight snapshot invalidated")
	}
	s.setupProtocolRevisionRoutes(s.engine.Group("/api/admin"))
	status := revisionAdminRequest(t, s.engine, http.MethodGet, "/api/admin/protocols", nil, "")
	if status.Code != http.StatusOK || !strings.Contains(status.Body.String(), "runtimeError") || !strings.Contains(status.Body.String(), "repair interrupted") {
		t.Fatal("missing management failure reason", status.Body)
	}
	recoveryExec(t, db, `DROP TRIGGER reject_repair`)
	if err := s.reloadProtocolRuntime(t.Context()); err != nil {
		t.Fatal(err)
	}
	assertRecoveredPresets(t, s)
	if s.protocolRuntimeError() != nil || s.protocolRuntimeFailure.Load() != nil {
		t.Fatal("successful repair retained failure")
	}
}

func TestPresetRecoveryPreservesManualBindingsAndHistoricalRevision(t *testing.T) {
	s, _ := newProtocolAdminTestServer(t)
	raw, err := os.ReadFile(filepath.Join("..", "protocol", "builtin", "testdata", "previous", protocol.PresetAnthropicID+".json"))
	if err != nil {
		t.Fatal(err)
	}
	old := persistPreviousRevision(t, s, mustProtocolValue(t, string(raw)))
	b := storage.ProtocolBinding{Kind: "source", SourceID: "manual", Binding: protocol.Binding{
		ProtocolID: protocol.PresetAnthropicID, RevisionHash: old.Hash(), Operation: "operator-submit", Transports: []protocol.Transport{protocol.HTTPJSON},
		Capabilities: protocol.CapabilitySet{protocol.TextCapability: true, protocol.FunctionToolsCapability: false}, Wait: &protocol.JobWait{TimeoutMillis: 2345, OnTimeout: "continue"},
	}}
	if err := s.store.SaveProtocolBinding(t.Context(), b); err != nil {
		t.Fatal(err)
	}
	unbound := storage.ProtocolBinding{Kind: "model", SourceID: "manual", ModelID: "stay-unbound", Unbound: true}
	if err := s.store.SaveProtocolBinding(t.Context(), unbound); err != nil {
		t.Fatal(err)
	}
	before, err := s.store.ReadProtocolRevision(t.Context(), protocol.PresetAnthropicID, old.Hash())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.recoverRequiredPresets(t.Context()); err != nil {
		t.Fatal(err)
	}
	bindings, err := s.store.ListProtocolBindings(t.Context())
	if err != nil || len(bindings) != 2 {
		t.Fatal(bindings, err)
	}
	for _, current := range bindings {
		if current.Kind == "model" {
			if !reflect.DeepEqual(current, unbound) {
				t.Fatal("model unbound intent changed")
			}
		} else {
			current.Binding.RevisionHash = b.Binding.RevisionHash
			if !reflect.DeepEqual(current.Binding, b.Binding) {
				t.Fatal("manual contract expanded or rewritten")
			}
			if len(current.Combinations) != 1 || current.Combinations[0].Fidelity != "rejected" {
				t.Fatal("incompatible manual operation should remain rejected")
			}
		}
	}
	after, err := s.store.ReadProtocolRevision(t.Context(), protocol.PresetAnthropicID, old.Hash())
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("historical task revision changed", err)
	}
}
