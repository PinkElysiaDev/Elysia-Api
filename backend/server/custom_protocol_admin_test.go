package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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
	relay.ClearCustomProtocols()
	t.Cleanup(relay.ClearCustomProtocols)

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
		openaiAdapter:          relay.NewOpenAIAdapter(10 * time.Second),
		claudeAdapter:          relay.NewClaudeAdapter(10 * time.Second),
		geminiAdapter:          relay.NewGeminiAdapter(10 * time.Second),
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

func TestAdminCustomProtocolUpsertListDelete(t *testing.T) {
	s, _ := newProtocolAdminTestServer(t)

	c, rec := adminProtocolContext(http.MethodPut, "/api/admin/custom-protocols/vendor-json", vendorProtocolJSON)
	c.Params = gin.Params{{Key: "id", Value: "vendor-json"}}
	s.adminUpsertCustomProtocol(c)
	data := decodeAdminData(t, rec)
	if data["saved"] != true || data["synced"] != true {
		t.Fatalf("unexpected upsert result: %#v", data)
	}
	if _, ok := relay.GetCustomProtocol("vendor-json"); !ok {
		t.Fatal("protocol should be registered after upsert")
	}

	// 持久化在 SQLite：store 查询能读回同一行。
	rows, err := s.store.ListCustomProtocols(t.Context())
	if err != nil || len(rows) != 1 || rows[0].ID != "vendor-json" || rows[0].Type != "llm" {
		t.Fatalf("store should contain the protocol, rows=%#v err=%v", rows, err)
	}
	if !strings.Contains(rows[0].Config, `"vendor-json"`) {
		t.Fatalf("store config should keep raw JSON, got: %s", rows[0].Config)
	}

	c, rec = adminProtocolContext(http.MethodGet, "/api/admin/custom-protocols", "")
	s.adminListCustomProtocols(c)
	list := decodeAdminData(t, rec)
	items, _ := list["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("expected 1 protocol, got %d (%s)", len(items), rec.Body.String())
	}
	item := items[0].(map[string]any)
	if item["id"] != "vendor-json" || item["type"] != "llm" || item["valid"] != true {
		t.Fatalf("unexpected summary: %#v", item)
	}
	if item["updatedAt"] == nil || item["updatedAt"] == "" {
		t.Fatalf("summary should carry updatedAt, got %#v", item)
	}

	// 非法 type 拒绝。
	invalid := strings.Replace(vendorProtocolJSON, `"type": "llm"`, `"type": "bogus"`, 1)
	c, rec = adminProtocolContext(http.MethodPut, "/api/admin/custom-protocols/vendor-json", invalid)
	c.Params = gin.Params{{Key: "id", Value: "vendor-json"}}
	s.adminUpsertCustomProtocol(c)
	if message := decodeAdminError(t, rec); !strings.Contains(message, "type") {
		t.Fatalf("expected type validation error, got %q", message)
	}

	// upsert 同 ID 覆盖而非追加。
	c, rec = adminProtocolContext(http.MethodPut, "/api/admin/custom-protocols/vendor-json", vendorProtocolJSON)
	c.Params = gin.Params{{Key: "id", Value: "vendor-json"}}
	s.adminUpsertCustomProtocol(c)
	decodeAdminData(t, rec)
	c, rec = adminProtocolContext(http.MethodGet, "/api/admin/custom-protocols", "")
	s.adminListCustomProtocols(c)
	list = decodeAdminData(t, rec)
	if items, _ := list["items"].([]any); len(items) != 1 {
		t.Fatalf("upsert should replace, got %d items", len(items))
	}

	// id 不一致拒绝。
	c, rec = adminProtocolContext(http.MethodPut, "/api/admin/custom-protocols/other-id", vendorProtocolJSON)
	c.Params = gin.Params{{Key: "id", Value: "other-id"}}
	s.adminUpsertCustomProtocol(c)
	decodeAdminError(t, rec)

	c, rec = adminProtocolContext(http.MethodDelete, "/api/admin/custom-protocols/vendor-json", "")
	c.Params = gin.Params{{Key: "id", Value: "vendor-json"}}
	s.adminDeleteCustomProtocol(c)
	decodeAdminData(t, rec)
	if _, ok := relay.GetCustomProtocol("vendor-json"); ok {
		t.Fatal("protocol should be unregistered after delete")
	}
	c, rec = adminProtocolContext(http.MethodDelete, "/api/admin/custom-protocols/vendor-json", "")
	c.Params = gin.Params{{Key: "id", Value: "vendor-json"}}
	s.adminDeleteCustomProtocol(c)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("second delete should 404, got %d", rec.Code)
	}
}

func TestAdminPreviewCustomProtocolMasksCredentials(t *testing.T) {
	s, _ := newProtocolAdminTestServer(t)
	payload := fmt.Sprintf(`{"protocol":%s}`, vendorProtocolJSON)

	c, rec := adminProtocolContext(http.MethodPost, "/api/admin/custom-protocols/preview", payload)
	s.adminPreviewCustomProtocol(c)
	data := decodeAdminData(t, rec)
	if data["method"] != "POST" {
		t.Fatalf("unexpected method: %#v", data)
	}
	headers, _ := data["headers"].(map[string]any)
	if got := headers["x-api-key"]; got != "<source-api-key>" {
		t.Fatalf("expected masked x-api-key, got %#v", got)
	}
	if _, hasAuth := headers["Authorization"]; hasAuth {
		t.Fatal("header auth mode must not add Authorization")
	}
	body, _ := data["body"].(string)
	if !strings.Contains(body, "sample-model") || !strings.Contains(body, "\"messages\"") {
		t.Fatalf("preview body should render sample request, got: %s", body)
	}
	if !strings.Contains(body, "\n") {
		t.Fatal("preview body should be pretty-printed")
	}
}

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

func TestAdminTestCustomProtocolNonStream(t *testing.T) {
	s, _ := newProtocolAdminTestServer(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "secret-key" {
			t.Errorf("expected source api key via x-api-key, got %q", r.Header.Get("x-api-key"))
		}
		if r.URL.Path != "/v2/generate/vendor-model" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"answer":{"text":"ok"},"finish":"stop","usage":{"prompt_tokens":2,"completion_tokens":3}}`))
	}))
	defer upstream.Close()
	seedProtocolTestModel(t, s, upstream.URL)

	payload := fmt.Sprintf(`{"protocol":%s,"sourceId":"s1","model":"vendor-model","stream":false}`, vendorProtocolJSON)
	c, rec := adminProtocolContext(http.MethodPost, "/api/admin/custom-protocols/test", payload)
	s.adminTestCustomProtocol(c)
	data := decodeAdminData(t, rec)
	if data["statusCode"] != float64(http.StatusOK) {
		t.Fatalf("unexpected status: %#v", data)
	}
	if !strings.Contains(data["rawBody"].(string), "answer") {
		t.Fatalf("raw body missing: %#v", data)
	}
	maheshvara, _ := data["maheshvara"].(map[string]any)
	if maheshvara == nil {
		t.Fatalf("expected mapped maheshvara result: %s", rec.Body.String())
	}
	output, _ := json.Marshal(maheshvara["output"])
	if !strings.Contains(string(output), "ok") {
		t.Fatalf("mapped output missing text: %s", output)
	}
}

func TestAdminTestCustomProtocolStream(t *testing.T) {
	s, _ := newProtocolAdminTestServer(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"text\":\"He\"}\n\ndata: {\"text\":\"llo\"}\n\ndata: [DONE]\n\n"))
	}))
	defer upstream.Close()
	seedProtocolTestModel(t, s, upstream.URL)

	streamProtocol := `{
      "id": "vendor-stream",
      "type": "llm",
      "request": {"method": "POST", "path": "/v2/stream", "bodyTemplate": "{\"model\":\"{{maheshvara.model}}\",\"stream\":{{maheshvara.stream}}}"},
      "response": {"stream": {"mode": "delta", "doneValues": ["[DONE]"], "response": {"textPath": "text"}}}
    }`
	payload := fmt.Sprintf(`{"protocol":%s,"sourceId":"s1","model":"vendor-model","stream":true}`, streamProtocol)
	c, rec := adminProtocolContext(http.MethodPost, "/api/admin/custom-protocols/test", payload)
	s.adminTestCustomProtocol(c)
	data := decodeAdminData(t, rec)
	if data["statusCode"] != float64(http.StatusOK) {
		t.Fatalf("unexpected status: %#v", data)
	}
	events, _ := data["events"].([]any)
	if len(events) != 3 {
		t.Fatalf("expected 3 sampled events, got %d (%s)", len(events), rec.Body.String())
	}
	if _, has := data["streamError"]; has {
		t.Fatalf("unexpected stream error: %#v", data["streamError"])
	}
	decoded, _ := json.Marshal(data["decoded"])
	if !strings.Contains(string(decoded), "He") || !strings.Contains(string(decoded), "llo") {
		t.Fatalf("decoded events missing text deltas: %s", decoded)
	}
}

// 临时凭据测试：协议尚未落源时直接以 baseUrl/apiKey/模型名直连——不再要求
// 先建源、先填手动模型。
func TestAdminTestCustomProtocolAdHocCredentials(t *testing.T) {
	s, _ := newProtocolAdminTestServer(t)
	var gotAuth, gotModel string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("x-api-key")
		body, _ := io.ReadAll(r.Body)
		var parsed map[string]any
		_ = json.Unmarshal(body, &parsed)
		gotModel, _ = parsed["model"].(string)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"answer":{"text":"ad-hoc ok"},"finish":"stop","usage":{"prompt_tokens":1,"completion_tokens":1}}`))
	}))
	defer upstream.Close()

	payload := fmt.Sprintf(`{"protocol":%s,"baseUrl":%q,"apiKey":"sk-adhoc","model":"proto-model"}`, vendorProtocolJSON, upstream.URL)
	c, rec := adminProtocolContext(http.MethodPost, "/api/admin/custom-protocols/test", payload)
	s.adminTestCustomProtocol(c)
	data := decodeAdminData(t, rec)
	if data["statusCode"] != float64(http.StatusOK) || data["targetModel"] != "proto-model" {
		t.Fatalf("unexpected result: %#v (%s)", data, rec.Body.String())
	}
	if gotAuth != "sk-adhoc" {
		t.Fatalf("ad-hoc key must be applied, got %q", gotAuth)
	}
	if gotModel != "proto-model" {
		t.Fatalf("model name must come from the free-text field, got %q", gotModel)
	}
	maheshvara, _ := json.Marshal(data["maheshvara"])
	if !strings.Contains(string(maheshvara), "ad-hoc ok") {
		t.Fatalf("mapped result missing text: %s", maheshvara)
	}
}

// 模型发现试拉：按协议 models 配置请求上游，返回 ID 列表与原文，不写库；
// 协议未声明发现配置时给出可操作错误。
func TestAdminTestCustomProtocolModels(t *testing.T) {
	s, _ := newProtocolAdminTestServer(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" || r.Method != http.MethodGet {
			t.Errorf("unexpected discovery request: %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"m-a","display_name":"Model A"},{"id":"m-b"}]}`))
	}))
	defer upstream.Close()

	protocol := `{
      "id": "discovery-vendor",
      "request": {"method": "POST", "path": "/v2/generate", "bodyTemplate": "{\"model\":\"{{maheshvara.model}}\"}"},
      "models": {"path": "/v1/models", "listPath": "data", "namePath": "display_name"}
    }`
	payload := fmt.Sprintf(`{"protocol":%s,"baseUrl":%q,"apiKey":"sk-x"}`, protocol, upstream.URL)
	c, rec := adminProtocolContext(http.MethodPost, "/api/admin/custom-protocols/test-models", payload)
	s.adminTestCustomProtocolModels(c)
	data := decodeAdminData(t, rec)
	if data["statusCode"] != float64(http.StatusOK) {
		t.Fatalf("unexpected status: %#v (%s)", data, rec.Body.String())
	}
	models, _ := data["models"].([]any)
	if len(models) != 2 {
		t.Fatalf("expected 2 discovered models, got %#v", data["models"])
	}
	first, _ := models[0].(map[string]any)
	if first["id"] != "m-a" || first["name"] != "Model A" {
		t.Fatalf("unexpected first model: %#v", first)
	}

	// 未声明 models 的协议 → 可操作错误。
	payload = fmt.Sprintf(`{"protocol":%s,"baseUrl":%q}`, vendorProtocolJSON, upstream.URL)
	c, rec = adminProtocolContext(http.MethodPost, "/api/admin/custom-protocols/test-models", payload)
	s.adminTestCustomProtocolModels(c)
	if msg := decodeAdminError(t, rec); !strings.Contains(msg, "模型发现") {
		t.Fatalf("expected discovery-missing error, got %q", msg)
	}
}

func TestExtractAssistProtocolJSON(t *testing.T) {
	configJSON := `{"id":"x","request":{"path":"/a"}}`

	fenced := "设计说明……\n```json\n" + configJSON + "\n```\n完毕"
	if raw, ok := extractAssistProtocolJSON(fenced); !ok || !strings.Contains(string(raw), `"id"`) {
		t.Fatalf("fenced extraction failed: %q ok=%v", raw, ok)
	}

	plain := fmt.Sprintf("说明如下：\n%s\n以上。", configJSON)
	if raw, ok := extractAssistProtocolJSON(plain); !ok || !strings.Contains(string(raw), `"request"`) {
		t.Fatalf("plain extraction failed: %q ok=%v", raw, ok)
	}

	// 无 id/request 结构的 JSON 样例不应被误提取。
	decoy := "示例响应：\n```json\n{\"choices\":[{\"message\":{\"content\":\"hi\"}}]}\n```\n结束"
	if _, ok := extractAssistProtocolJSON(decoy); ok {
		t.Fatal("decoy JSON must not be extracted as protocol config")
	}

	if _, ok := extractAssistProtocolJSON("没有任何 JSON 的回复"); ok {
		t.Fatal("non-JSON reply must not yield a config")
	}
}

func TestParseAssistDataURL(t *testing.T) {
	mime, data, err := parseAssistDataURL("data:application/pdf;base64,SGVsbG8=")
	if err != nil || mime != "application/pdf" || string(data) != "Hello" {
		t.Fatalf("unexpected parse result: %q %q %v", mime, data, err)
	}
	if _, _, err := parseAssistDataURL("data:application/pdf,SGVsbG8="); err == nil {
		t.Fatal("non-base64 data URL must be rejected")
	}
	if _, _, err = parseAssistDataURL("not-a-data-url"); err == nil {
		t.Fatal("missing data: prefix must be rejected")
	}
}

func TestBuildAssistConversationPartShapes(t *testing.T) {
	dataURL := "data:image/png;base64,aGk="
	payload := customProtocolAssistPayload{
		Message: "请设计协议",
		Documents: []customProtocolAssistDocument{
			{Name: "api.md", Text: "POST /v2/generate ..."},
			{Name: "shot.png", DataURL: dataURL},
			{Name: "spec.pdf", DataURL: "data:application/pdf;base64,aGk="},
		},
		ExampleResponse: json.RawMessage(`{"answer":{"text":"ok"}}`),
	}
	openaiModel := storage.Model{Platform: "openai", Name: "m"}
	messages, err := buildAssistConversation(openaiModel, payload)
	if err != nil {
		t.Fatalf("buildAssistConversation: %v", err)
	}
	parts := messages[0].Content
	// 说明 + 文本材料 + 图片 + 文档 + 示例响应 + 输出要求
	if len(parts) != 6 {
		t.Fatalf("expected 6 parts, got %d", len(parts))
	}
	if parts[2].Type != relay.MaheshvaraContentImage || parts[2].ImageBase64 != "hi" {
		t.Fatalf("unexpected image part: %#v", parts[2])
	}
	if parts[3].Type != relay.MaheshvaraContentDocument || parts[3].FileData != "data:application/pdf;base64,aGk=" {
		t.Fatalf("openai target must keep data URL form, got %#v", parts[3])
	}
	if !strings.Contains(parts[4].Text, `"answer"`) {
		t.Fatalf("example response should be included as text part: %#v", parts[4].Text)
	}

	claudeModel := storage.Model{Platform: "claude", Name: "m"}
	messages, err = buildAssistConversation(claudeModel, payload)
	if err != nil {
		t.Fatalf("buildAssistConversation: %v", err)
	}
	docPart := messages[0].Content[3]
	if docPart.FileData == "data:application/pdf;base64,aGk=" || docPart.FileData == "" {
		t.Fatalf("claude target must use raw base64, got %q", docPart.FileData)
	}

	if _, err := buildAssistConversation(openaiModel, customProtocolAssistPayload{}); err == nil {
		t.Fatal("empty payload must be rejected")
	}
}

// 修复循环 + 离线验证：首轮返回带未知字段的坏配置，修复轮返回好配置；
// 有效草稿离线渲染并按示例响应验证映射，全程不触发真实测试端点。
func TestAdminAssistRepairLoopAndVerification(t *testing.T) {
	s, _ := newProtocolAdminTestServer(t)
	goodConfig := `{
	  "id": "vendor-json", "name": "Vendor", "type": "llm",
	  "request": {"method": "POST", "path": "/v2/generate", "auth": {"mode": "header", "header": "x-api-key"},
	    "body": {"model": {"field": "model", "mode": "string"}, "input": {"field": "messages"}}},
	  "response": {"fields": [
	    {"path": "answer.text", "field": "text"},
	    {"path": "finish", "field": "stop_reason"},
	    {"path": "usage.prompt", "field": "usage.input_tokens"},
	    {"path": "usage.completion", "field": "usage.output_tokens"}
	  ]}
	}`
	badConfig := strings.Replace(goodConfig, `"field": "model", "mode": "string"`, `"field": "no_such_field"`, 1)
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		content := "说明：首轮配置。\n```json\n" + badConfig + "\n```"
		if calls >= 2 {
			content = "说明：修复后的配置。\n```json\n" + goodConfig + "\n```"
		}
		response := relay.OpenAIResponse{
			ID: "cmpl-1", Object: "chat.completion", Model: "assistant",
			Choices: []relay.Choice{{Index: 0, Message: relay.Message{Role: "assistant", Content: content}, FinishReason: "stop"}},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer upstream.Close()
	seedProtocolTestModel(t, s, upstream.URL)

	payload := `{
	  "sourceId": "s1", "model": "vendor-model", "protocolType": "llm",
	  "message": "请根据文档设计协议",
	  "documents": [{"name": "api.md", "text": "POST /v2/generate ..."}],
	  "exampleResponse": {"answer": {"text": "ok"}, "finish": "stop", "usage": {"prompt": 2, "completion": 3}}
	}`
	c, rec := adminProtocolContext(http.MethodPost, "/api/admin/custom-protocols/assist", payload)
	s.adminAssistCustomProtocol(c)
	data := decodeAdminData(t, rec)
	if data["valid"] != true {
		t.Fatalf("expected valid draft after repair, got: %s", rec.Body.String())
	}
	if calls != 2 {
		t.Fatalf("repair loop should call the model twice, got %d", calls)
	}
	if data["rounds"] != float64(2) {
		t.Fatalf("expected rounds=2, got %#v", data["rounds"])
	}
	verification, _ := data["verification"].(map[string]any)
	if verification == nil {
		t.Fatalf("expected offline verification, got: %s", rec.Body.String())
	}
	request, _ := verification["request"].(map[string]any)
	if request == nil || !strings.Contains(request["body"].(string), "sample-model") {
		t.Fatalf("verification should contain rendered sample request: %#v", verification)
	}
	mappedJSON, _ := json.Marshal(verification["mappedResponse"])
	if !strings.Contains(string(mappedJSON), "ok") || !strings.Contains(string(mappedJSON), `"input_tokens":2`) {
		t.Fatalf("verification should map the example response, got: %s", mappedJSON)
	}
}

func TestAdminAssistCustomProtocolEndToEnd(t *testing.T) {
	s, _ := newProtocolAdminTestServer(t)
	reply := "设计说明：按 vendor 文档映射。\n```json\n" + vendorProtocolJSON + "\n```"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("unexpected upstream path %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer secret-key" {
			t.Errorf("expected bearer auth, got %q", r.Header.Get("Authorization"))
		}
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode upstream request: %v", err)
			return
		}
		// 多模态请求应包含文本材料内容。
		encoded, _ := json.Marshal(request)
		if !strings.Contains(string(encoded), "POST /v2/generate") {
			t.Errorf("document text missing from assist request: %s", encoded)
		}
		response := relay.OpenAIResponse{
			ID: "cmpl-1", Object: "chat.completion", Model: "assistant",
			Choices: []relay.Choice{{Index: 0, Message: relay.Message{Role: "assistant", Content: reply}, FinishReason: "stop"}},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer upstream.Close()
	seedProtocolTestModel(t, s, upstream.URL)

	payload := `{"sourceId":"s1","model":"vendor-model","protocolType":"llm","message":"请根据文档设计协议","documents":[{"name":"api.md","text":"POST /v2/generate ..."}]}`
	c, rec := adminProtocolContext(http.MethodPost, "/api/admin/custom-protocols/assist", payload)
	s.adminAssistCustomProtocol(c)
	data := decodeAdminData(t, rec)
	if data["valid"] != true {
		t.Fatalf("expected valid draft, got: %s", rec.Body.String())
	}
	configRaw, _ := json.Marshal(data["config"])
	var parsed relay.CustomProtocolConfig
	if err := json.Unmarshal(configRaw, &parsed); err != nil || parsed.ID != "vendor-json" {
		t.Fatalf("unexpected extracted config: %s", configRaw)
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
	if _, ok := relay.GetCustomProtocol("vendor-json"); ok {
		t.Fatal("registry should not be synced by migration itself (startup sync follows)")
	}
	s.syncCustomProtocols()
	if _, ok := relay.GetCustomProtocol("vendor-json"); !ok {
		t.Fatal("registry should contain the migrated protocol after sync")
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

func TestAdminCustomProtocolSchema(t *testing.T) {
	s, _ := newProtocolAdminTestServer(t)
	c, rec := adminProtocolContext(http.MethodGet, "/api/admin/custom-protocols/schema", "")
	s.adminCustomProtocolSchema(c)
	data := decodeAdminData(t, rec)
	encoded, _ := json.Marshal(data)
	for _, want := range []string{`"model"`, `"usage.input_tokens"`, `"metadata"`, `"output_items"`} {
		if !strings.Contains(string(encoded), want) {
			t.Fatalf("schema should contain %s, got: %s", want, encoded)
		}
	}
}
