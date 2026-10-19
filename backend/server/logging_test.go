package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/elysia-api/backend/config"
	"github.com/elysia-api/backend/logging"
	"github.com/elysia-api/backend/storage"
	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"
)

func TestShutdownDrainsBothLogQueues(t *testing.T) {
	path := filepath.Join(t.TempDir(), "business.db")
	store, err := storage.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{store: store, config: &config.Config{}}
	s.logger = logging.New(logging.Options{Level: "error"}, logging.NewWriter(store.LogStore.WriteSystemLogBatch))
	t.Cleanup(s.doShutdown)
	s.startUsageWriter()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for j := 0; j < 64; j++ {
				id := fmt.Sprintf("%d-%d", worker, j)
				s.recordUsage(&usageRecord{RequestID: id, StartedAt: time.Now(), StatusCode: 200})
				s.logSystemEvent("info", "completed", map[string]any{"request_id": id})
			}
		}(i)
	}
	wg.Wait()
	s.doShutdown()
	if s.shutdownErr != nil {
		t.Fatal(s.shutdownErr)
	}
	reopened, err := storage.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if count, err := reopened.CountUsageRecords(t.Context()); err != nil || count != 512 {
		t.Fatalf("usage count %d: %v", count, err)
	}
	if count, _, err := reopened.QuerySystemLogs(t.Context(), 1, 0, ""); err != nil || count != 513 {
		t.Fatalf("system count %d: %v", count, err)
	}
	if s.Logger().Stats().RetainedBytes != 0 || s.usageWriterStats().RetainedBytes != 0 {
		t.Fatal("shutdown retained queued data")
	}
}

func TestRequestLoggerCorrelatesAndRecovers(t *testing.T) {
	s := &Server{logger: logging.New(logging.Options{Level: "debug", Format: "json"}, nil)}
	var output bytes.Buffer
	s.logger.SetOutput(&output)
	engine := gin.New()
	engine.Use(s.requestLogging())
	var id string
	engine.GET("/panic", func(c *gin.Context) {
		id = c.GetString("requestID")
		zerolog.Ctx(c.Request.Context()).Info().Msg("in handler")
		panic("test panic")
	})
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/panic", nil)
	r.Header.Set("X-Elysia-Request-Id", "untrusted")
	engine.ServeHTTP(w, r)
	if w.Code != 500 || id == "" || id == "untrusted" || w.Header().Get("X-Elysia-Request-Id") != id {
		t.Fatalf("status/id: %d %q", w.Code, id)
	}
	for _, line := range bytes.Split(bytes.TrimSpace(output.Bytes()), []byte("\n")) {
		var event map[string]any
		if err := json.Unmarshal(line, &event); err != nil {
			t.Fatal(err)
		}
		if event["request_id"] != id {
			t.Fatalf("uncorrelated event: %s", line)
		}
	}
}

func TestShutdownReportsPersistenceFailure(t *testing.T) {
	store, err := storage.Open(filepath.Join(t.TempDir(), "failed.db"))
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{store: store, config: &config.Config{}}
	s.logger = logging.New(logging.Options{Level: "error"}, logging.NewWriter(store.LogStore.WriteSystemLogBatch))
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	s.logSystemEvent("info", "cannot persist", nil)
	s.doShutdown()
	if s.shutdownErr == nil || s.Logger().Stats().Failures == 0 {
		t.Fatal("failed drain reported successful shutdown")
	}
	if err := s.logger.Close(context.Background()); err == nil {
		t.Fatal("idempotent close lost the persistence error")
	}
}

func TestRuntimeLogSettingsAreAtomicAndPathsRequireRestart(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"logLevel":"info"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{config: cfg, logger: logging.New(cfg.GetLoggingOptions(), nil)}
	alias := cfg.GetDatabasePath()
	if _, err := s.updateRuntimeConfig(runtimeConfigPayload{LogDatabasePath: &alias, LogLevel: "debug"}); err == nil {
		t.Fatal("accepted business database as log database")
	} else if invalid, ok := err.(*runtimeConfigError); !ok || invalid.status != http.StatusBadRequest || invalid.code != "invalid_database_path" {
		t.Fatalf("path validation lost its HTTP error semantics: %v", err)
	}
	if cfg.GetLogLevel() != "info" {
		t.Fatal("invalid path published partial logger configuration")
	}
	logs := filepath.Join(dir, "separate", "logs.db")
	restart, err := s.updateRuntimeConfig(runtimeConfigPayload{LogDatabasePath: &logs, LogLevel: "error", LogPersistLevel: "warn", LogFormat: "json"})
	if err != nil || !restart {
		t.Fatalf("path update: restart=%t err=%v", restart, err)
	}
	loaded, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.GetLogDatabasePath() != logs || loaded.GetLoggingOptions().PersistLevel != "warn" || loaded.GetLoggingOptions().Format != "json" {
		t.Fatal("logging settings did not persist")
	}
	var console bytes.Buffer
	s.logger.SetOutput(&console)
	s.logEvent("info").Msg("filtered")
	if console.Len() != 0 {
		t.Fatal("console threshold was not reloaded")
	}
	restart, err = s.updateRuntimeConfig(runtimeConfigPayload{LogLevel: "debug"})
	if err != nil || restart {
		t.Fatalf("level update required restart: %t %v", restart, err)
	}
	s.logger.SetOutput(&console)
	s.logEvent("debug").Msg("visible")
	if !bytes.Contains(console.Bytes(), []byte("visible")) {
		t.Fatal("debug threshold was not applied")
	}
}
