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
	"github.com/elysia-api/backend/relay"
	"github.com/elysia-api/backend/storage"
	"github.com/gin-gonic/gin"
)

// newProtocolAdminTestServer 构造带临时 config.json 与 SQLite store 的管理
// 端点测试服务器：UpdateCustomProtocols 需要 config 落盘路径，预览/测试/助手
// 端点需要 store 提供模型凭证。返回服务器与 config.json 路径。
func newProtocolAdminTestServer(t *testing.T) (*Server, string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	relay.SetAllowPrivateDial(true)

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(cfgPath, []byte(`{"host":"127.0.0.1","port":8765,"customProtocols":[]}`), 0o644); err != nil {
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

func decodeAdminError(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var envelope struct {
		OK    bool `json:"ok"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode response %q: %v", rec.Body.String(), err)
	}
	if envelope.OK || envelope.Error == nil {
		t.Fatalf("expected error response, got %q (status %d)", rec.Body.String(), rec.Code)
	}
	return envelope.Error.Message
}

const vendorProtocolJSON = `{
  "id": "vendor-json",
  "name": "Vendor JSON API",
  "type": "llm",
  "request": {
    "method": "POST",
    "path": "/v2/generate/{{maheshvara.model}}",
    "auth": {"mode": "header", "header": "x-api-key"},
    "bodyTemplate": "{\"model\":\"{{maheshvara.model}}\",\"messages\":{{maheshvara.messages}},\"temperature\":{{maheshvara.temperature | default:0.2}}}"
  },
  "response": {
    "textPath": "answer.text",
    "usagePath": "usage",
    "finishReasonPath": "finish"
  }
}`

func seedProtocolTestModel(t *testing.T, s *Server, upstreamURL string) {
	t.Helper()
	source := storage.ModelSource{
		ID: "s1", Name: "vendor", BaseURL: upstreamURL, APIKey: "secret-key",
		Platform: "openai", Enabled: true,
	}
	if err := s.store.UpsertSource(t.Context(), source); err != nil {
		t.Fatalf("UpsertSource: %v", err)
	}
	models := []storage.Model{{
		ID: "m1", Name: "vendor-model", SourceID: "s1",
		BaseURL: upstreamURL, APIKey: "secret-key", Platform: "openai",
		Type: "llm", Enabled: true, Available: true,
	}}
	if err := s.store.ReplaceSourceModels(t.Context(), source, models); err != nil {
		t.Fatalf("ReplaceSourceModels: %v", err)
	}
}

func TestMigrateLegacyCustomProtocolsFromConfig(t *testing.T) {
	s, cfgPath := newProtocolAdminTestServer(t)

	// 模拟旧版本升级：config.json 带废弃的 customProtocols 键。
	legacy := `{"host":"127.0.0.1","port":8765,"customProtocols":[` + vendorProtocolJSON + `]}`
	if err := os.WriteFile(cfgPath, []byte(legacy), 0o644); err != nil {
		t.Fatalf("write legacy config: %v", err)
	}
	s.migrateLegacyCustomProtocols()

	rows, err := s.store.ListCustomProtocols(t.Context())
	if err != nil || len(rows) != 1 || rows[0].ID != "vendor-json" {
		t.Fatalf("migration should import into sqlite, rows=%#v err=%v", rows, err)
	}
	raw, _ := os.ReadFile(cfgPath)
	if strings.Contains(string(raw), "customProtocols") {
		t.Fatalf("deprecated key must be stripped from config.json, got: %s", raw)
	}

	// 同 ID 已存在时以库为准：重新出现的旧键不覆盖库内数据。
	if err := os.WriteFile(cfgPath, []byte(`{"customProtocols":[{"id":"vendor-json","request":{"bodyTemplate":"{\"x\":1}"}}]}`), 0o644); err != nil {
		t.Fatalf("rewrite legacy config: %v", err)
	}
	s.migrateLegacyCustomProtocols()
	rows, err = s.store.ListCustomProtocols(t.Context())
	if err != nil || len(rows) != 1 || !strings.Contains(rows[0].Config, "answer.text") {
		t.Fatalf("existing row must win over stale config.json entry, rows=%#v err=%v", rows, err)
	}
}

func TestSystemLogWrite(t *testing.T) {
	s, _ := newProtocolAdminTestServer(t)
	s.logSystemEvent("info", "server started", map[string]any{"version": "test", "port": 8765})
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
