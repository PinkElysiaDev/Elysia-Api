package server

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/elysia-api/backend/config"
	"github.com/elysia-api/backend/protocol"
	"github.com/elysia-api/backend/relay"
	"github.com/elysia-api/backend/storage"
)

// Reproduce the pre-history fingerprint format, not merely an empty new DB.
func legacyStartupBackup(t *testing.T, db *sql.DB, path string) string {
	t.Helper()
	recoveryExec(t, db, `DROP TABLE protocol_history`)
	digest := sha256.New()
	encoder := json.NewEncoder(digest)
	for _, query := range []string{
		`SELECT * FROM custom_protocols ORDER BY id`, `SELECT * FROM model_sources ORDER BY id`,
		`SELECT * FROM models ORDER BY source_id,id`, `SELECT * FROM model_groups ORDER BY id`,
		`SELECT * FROM model_group_models ORDER BY group_id,position,source_id,model_id`,
		`SELECT * FROM protocol_drafts ORDER BY protocol_id`, `SELECT * FROM protocol_activations ORDER BY protocol_id`,
		`SELECT * FROM protocol_bindings ORDER BY binding_key`,
	} {
		rows, err := db.Query(query)
		if err != nil {
			t.Fatal(err)
		}
		columns, err := rows.Columns()
		if err != nil {
			t.Fatal(err)
		}
		if err := encoder.Encode([]any{query, columns}); err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			values := make([]any, len(columns))
			targets := make([]any, len(columns))
			for i := range values {
				targets[i] = &values[i]
			}
			if err := rows.Scan(targets...); err != nil {
				t.Fatal(err)
			}
			if err := encoder.Encode(values); err != nil {
				t.Fatal(err)
			}
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
	}
	recoveryExec(t, db, `VACUUM INTO ?`, path)
	raw, err := json.Marshal(map[string]string{"path": path, "baseline": hex.EncodeToString(digest.Sum(nil))})
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func startupFixtureConfig(t *testing.T, directory string) *config.Config {
	t.Helper()
	path := filepath.Join(directory, "config.json")
	raw, _ := json.Marshal(map[string]any{"databasePath": filepath.Join(directory, "test.sqlite3"), "modelCatalog": map[string]any{"enabled": false}, "openBrowserOnStart": false})
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestProductionStartupIgnoresHistoricalBackups(t *testing.T) {
	for _, kind := range []string{"legacy-fingerprint", "missing", "corrupt-file", "directory", "invalid-metadata"} {
		t.Run(kind, func(t *testing.T) {
			directory := t.TempDir()
			cfg := startupFixtureConfig(t, directory)
			path := cfg.GetDatabasePath()
			store, err := storage.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			store.Close()
			db, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			backup := filepath.Join(directory, "historical-backup.sqlite")
			raw := `{"path":"missing","baseline":"historical"}`
			switch kind {
			case "legacy-fingerprint":
				raw = legacyStartupBackup(t, db, backup)
			case "corrupt-file":
				if err := os.WriteFile(backup, []byte("broken original backup"), 0600); err != nil {
					t.Fatal(err)
				}
			case "directory":
				if err := os.Mkdir(backup, 0700); err != nil {
					t.Fatal(err)
				}
			case "invalid-metadata":
				raw = "{invalid preserved metadata"
			}
			if kind == "corrupt-file" || kind == "directory" {
				encoded, _ := json.Marshal(map[string]string{"path": backup, "baseline": "historical"})
				raw = string(encoded)
			}
			recoveryExec(t, db, `INSERT INTO settings VALUES('protocol_engine_v2_backup',?,'old')`, raw)
			before, _ := os.ReadFile(backup)
			restarts := 1
			if kind == "legacy-fingerprint" {
				restarts = 10
			}
			var baseline string
			var counts map[string]int
			var active any
			for attempt := 0; attempt <= restarts; attempt++ {
				s := New(cfg)
				func() {
					defer s.doShutdown()
					defer s.store.Close()
					if s.startupErr != nil || s.protocolRuntimeError() != nil || !s.protocolRuntimeReady.Load() {
						t.Fatal("production recovery blocked", s.startupErr, s.protocolRuntimeFailure.Load())
					}
					assertRecoveredPresets(t, s)
					current, err := s.store.ProtocolUpgradeBaseline(t.Context())
					if err != nil {
						t.Fatal(err)
					}
					activations, err := s.store.ListProtocolActivations(t.Context())
					if err != nil {
						t.Fatal(err)
					}
					for _, activation := range activations {
						if activation.Generation != 1 {
							t.Fatal("duplicate activation", activation)
						}
					}
					currentCounts := recoveryCounts(t, db)
					if currentCounts["protocol_verification_reports"] != 4 {
						t.Fatal("duplicate evidence", currentCounts)
					}
					if attempt == 0 {
						baseline, counts, active = current, currentCounts, activations
					} else if current != baseline || !reflect.DeepEqual(counts, currentCounts) || !reflect.DeepEqual(active, activations) {
						t.Fatal("restart mutated protocol data")
					}
					rows, err := s.store.ListCustomProtocols(t.Context())
					if err != nil || len(rows) != 0 {
						t.Fatal("legacy presets seeded", rows, err)
					}
					receipt, err := s.store.ProtocolUpgradeStatus(t.Context())
					if err != nil || receipt == nil || receipt.BackupMode != "not_required" || receipt.Backup != "" {
						t.Fatal("invalid migration completion", receipt, err)
					}
					s.setupProtocolRevisionRoutes(s.engine.Group("/api/admin"))
					response := revisionAdminRequest(t, s.engine, http.MethodGet, "/api/admin/protocols", nil, "")
					state := decodeAdminData(t, response)
					if state["runtimeReady"] != true || state["startupFailure"] != nil {
						t.Fatal("wrong readiness", state)
					}
				}()
			}
			var saved string
			if err := db.QueryRow(`SELECT value FROM settings WHERE key='protocol_engine_v2_backup'`).Scan(&saved); err != nil || saved != raw {
				t.Fatal("historical metadata changed", err)
			}
			after, _ := os.ReadFile(backup)
			if string(before) != string(after) {
				t.Fatal("historical backup changed")
			}
			snapshots, err := filepath.Glob(path + ".protocol-snapshot-v2-*")
			if err != nil || len(snapshots) != 0 {
				t.Fatal("insert-only recovery made backups", snapshots, err)
			}
		})
	}
}

func TestProductionReloadCompletesPreviouslyFailedStartup(t *testing.T) {
	directory := t.TempDir()
	cfg := startupFixtureConfig(t, directory)
	store, err := storage.Open(cfg.GetDatabasePath())
	if err != nil {
		t.Fatal(err)
	}
	store.Close()
	db, err := sql.Open("sqlite", cfg.GetDatabasePath())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	recoveryExec(t, db, `CREATE TRIGGER stop_creation BEFORE INSERT ON protocol_verification_reports BEGIN SELECT RAISE(ABORT,'injected creation failure'); END`)
	s := New(cfg)
	defer s.doShutdown()
	defer s.store.Close()
	if s.protocolRuntimeReady.Load() || s.protocolStartupFailure.Load() == nil {
		t.Fatal("failure marked ready")
	}
	if counts := recoveryCounts(t, db); counts["protocol_activations"] != 0 {
		t.Fatal("partial activation", counts)
	}
	s.setupProtocolRevisionRoutes(s.engine.Group("/api/admin"))
	failed := revisionAdminRequest(t, s.engine, http.MethodGet, "/api/admin/protocols", nil, "")
	if !strings.Contains(failed.Body.String(), `"stage":"preset_recovery"`) {
		t.Fatal("missing failure stage", failed.Body)
	}
	recoveryExec(t, db, `DROP TRIGGER stop_creation`)
	response := revisionAdminRequest(t, s.engine, http.MethodPost, "/api/admin/protocols/reload", nil, "")
	if response.Code != http.StatusOK || !s.protocolRuntimeReady.Load() || s.protocolStartupFailure.Load() != nil {
		t.Fatal("reload did not finish initialization", response.Body)
	}
	assertRecoveredPresets(t, s)
}

func TestProductionRecoveredPresetsDiscoverAndGenerate(t *testing.T) {
	defer relay.SetDeniedIPRanges(relay.DeniedIPRanges())
	for _, id := range requiredPresetIDs {
		t.Run(id, func(t *testing.T) {
			directory := t.TempDir()
			cfg := startupFixtureConfig(t, directory)
			initial, err := storage.Open(cfg.GetDatabasePath())
			if err != nil {
				t.Fatal(err)
			}
			initial.Close()
			db := recoveryDB(t, cfg.GetDatabasePath())
			backup := legacyStartupBackup(t, db, filepath.Join(directory, "old.sqlite"))
			recoveryExec(t, db, `INSERT INTO settings VALUES('protocol_engine_v2_backup',?,'old')`, backup)
			cfg.Outbound.DeniedIPRanges = []string{} // Allow the isolated loopback provider.
			s := New(cfg)
			defer s.store.Close()
			defer s.doShutdown()
			if !s.protocolRuntimeReady.Load() {
				t.Fatal("recovered runtime is not ready", s.protocolRuntimeFailure.Load())
			}
			service, err := s.protocolService()
			if err != nil {
				t.Fatal(err)
			}
			upstream, exists := service.Pin(id)
			if !exists {
				t.Fatal("preset was not loaded", id)
			}
			var reply protocol.Value
			for _, sample := range upstream.Definition().Samples {
				if sample.ID == "text-decode-response" {
					reply = sample.Input
				}
			}
			if reply.IsZero() {
				t.Fatal("missing provider fixture")
			}
			var discoveryCalls, generationCalls atomic.Int32
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.Method == http.MethodGet {
					discoveryCalls.Add(1)
					expected := "/v1/models"
					if id == protocol.PresetGeminiID {
						expected = "/v1beta/models"
					}
					if r.URL.Path != expected {
						t.Errorf("discovery path %s, want %s", r.URL.Path, expected)
					}
					if id == protocol.PresetGeminiID {
						_, _ = w.Write([]byte(`{"models":[{"name":"models/upstream-model","supportedGenerationMethods":["generateContent"]}]}`))
					} else {
						_, _ = w.Write([]byte(`{"data":[{"id":"upstream-model","type":"model","display_name":"Test"}],"has_more":false}`))
					}
					return
				}
				generationCalls.Add(1)
				_, _ = w.Write(reply.Bytes())
			}))
			defer provider.Close()
			baseURL := provider.URL
			if id == protocol.PresetChatCompletionsID || id == protocol.PresetResponsesID {
				baseURL += "/v1"
			}
			// Configure a model binding, but never manually create or activate presets.
			setupGatewayModel(t, s, upstream, baseURL)
			sources, err := s.store.ListSources(t.Context())
			if err != nil || len(sources) != 1 {
				t.Fatal("source configuration", sources, err)
			}
			models, err := s.fetchModelsFromSource(t.Context(), sources[0], "mock-only")
			if err != nil || len(models) != 1 || models[0].ID != "upstream-model" || discoveryCalls.Load() != 1 {
				t.Fatal("discovery after recovery", models, err, discoveryCalls.Load())
			}
			r := httptest.NewRequest(http.MethodPost, "/gateway/"+protocol.PresetChatCompletionsID+"/chat/completions", strings.NewReader(`{"model":"group","max_tokens":32,"messages":[{"role":"user","content":"hi"}]}`))
			r.Header.Set("Authorization", "Bearer gateway-test-token")
			response := httptest.NewRecorder()
			s.engine.ServeHTTP(response, r)
			if response.Code != http.StatusOK || generationCalls.Load() != 1 || !strings.Contains(response.Body.String(), "hi") {
				t.Fatal("generation after recovery", response.Code, generationCalls.Load(), response.Body)
			}
			wire, err := protocol.ParseValue(response.Body.Bytes())
			if err != nil {
				t.Fatal(err)
			}
			ingress, _ := service.Pin(protocol.PresetChatCompletionsID)
			if err := ingress.ValidateWireOutput(protocol.EncodeResponse, wire); err != nil {
				t.Fatal("invalid generated response", err)
			}
		})
	}
}
