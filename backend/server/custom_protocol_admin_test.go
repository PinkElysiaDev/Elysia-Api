package server

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elysia-api/backend/config"
	"github.com/elysia-api/backend/logging"
	"github.com/elysia-api/backend/relay"
	"github.com/elysia-api/backend/storage"
	"github.com/gin-gonic/gin"
)

// newProtocolAdminTestServer constructs isolated configuration and protocol storage.
func newProtocolAdminTestServer(t *testing.T) (*Server, string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	relay.SetAllowPrivateDial(true)

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(cfgPath, []byte(`{"host":"127.0.0.1","port":8765}`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	store, err := storage.Open(filepath.Join(dir, "test.sqlite3"))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return &Server{
		config:                 cfg,
		engine:                 gin.New(),
		protocolTransport:      relay.NewProtocolTransport(10 * time.Second),
		roundRobinIndex:        make(map[string]int),
		rateLimits:             make(map[string]*rateLimitState),
		affinity:               newAffinityCache(),
		store:                  store,
		skipOutboundValidation: true,
	}, cfgPath
}

func adminProtocolContext(method, target, body string) (*gin.Context, *httptest.ResponseRecorder) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	c.Request = req
	return c, rec
}

func decodeAdminData(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var envelope struct {
		OK    bool           `json:"ok"`
		Data  map[string]any `json:"data"`
		Error *struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode response %q: %v", rec.Body.String(), err)
	}
	if !envelope.OK {
		t.Fatalf("expected ok response, got error %+v (status %d)", envelope.Error, rec.Code)
	}
	return envelope.Data
}

func TestSystemLogWrite(t *testing.T) {
	s, _ := newProtocolAdminTestServer(t)
	s.logger = logging.New(s.config.GetLoggingOptions(), logging.NewWriter(s.store.LogStore.WriteSystemLogBatch))
	s.logSystemEvent("info", "server started", map[string]any{"version": "test", "port": 8765})
	if err := s.logger.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	total, logs, err := s.store.QuerySystemLogs(t.Context(), 10, 0, "")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if total != 1 || len(logs) != 1 || logs[0].Level != "info" || logs[0].Message != "server started" {
		t.Fatalf("unexpected logs: %+v total=%d", logs, total)
	}
	if !strings.Contains(logs[0].Fields, `"version":"test"`) {
		t.Fatalf("fields must persist: %s", logs[0].Fields)
	}
	// store 为 nil 时静默不 panic。
	bare := &Server{}
	bare.logSystemEvent("info", "no store", nil)
}
