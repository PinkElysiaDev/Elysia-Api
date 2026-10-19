package server

import (
	"database/sql"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bytedance/sonic"
	"github.com/elysia-api/backend/config"
	"github.com/elysia-api/backend/storage"
)

func startupFixtureConfig(t *testing.T, directory string) *config.Config {
	t.Helper()
	path := filepath.Join(directory, "config.json")
	raw, _ := sonic.ConfigStd.Marshal(map[string]any{"databasePath": filepath.Join(directory, "test.sqlite3"), "modelCatalog": map[string]any{"enabled": false}, "openBrowserOnStart": false})
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestProductionStartupPublishesCurrentPresets(t *testing.T) {
	cfg := startupFixtureConfig(t, t.TempDir())
	s := New(cfg)
	defer s.doShutdown()
	if s.startupErr != nil || !s.protocolRuntimeReady.Load() {
		t.Fatal("runtime failed to initialize", s.startupErr, s.protocolRuntimeFailure.Load())
	}
	assertRecoveredPresets(t, s)
}

func TestProductionReloadCompletesPreviouslyFailedStartup(t *testing.T) {
	cfg := startupFixtureConfig(t, t.TempDir())
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
