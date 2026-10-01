package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/elysia-api/backend/protocol"
	"github.com/gin-gonic/gin"
)

func revisionAdminRequest(t *testing.T, engine *gin.Engine, method, path string, body []byte, etag string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	if etag != "" {
		request.Header.Set("If-Match", etag)
	}
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	return response
}

func TestProtocolRevisionAdminDraftVerifyActivateAndPreview(t *testing.T) {
	server, _ := newProtocolAdminTestServer(t)
	server.setupProtocolRevisionRoutes(server.engine.Group("/api/admin"))
	raw, err := os.ReadFile(filepath.Join("..", "protocol", "testdata", "text-alpha.json"))
	if err != nil {
		t.Fatal(err)
	}
	created := revisionAdminRequest(t, server.engine, http.MethodPut, "/api/admin/protocols/text-alpha/draft", raw, "")
	if created.Code != http.StatusOK {
		t.Fatalf("create: %d %s", created.Code, created.Body)
	}
	data := decodeAdminData(t, created)
	draftHash := data["draft"].(map[string]any)["hash"].(string)
	service, err := server.protocolService()
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := service.Pin("text-alpha"); exists {
		t.Fatal("save bypassed activation")
	}
	stale := revisionAdminRequest(t, server.engine, http.MethodPut, "/api/admin/protocols/text-alpha/draft", raw, "")
	if stale.Code != http.StatusConflict {
		t.Fatalf("unconditional overwrite: %d %s", stale.Code, stale.Body)
	}
	verifyBody, _ := json.Marshal(map[string]string{"draftHash": draftHash})
	verified := revisionAdminRequest(t, server.engine, http.MethodPost, "/api/admin/protocols/text-alpha/verify", verifyBody, "")
	if verified.Code != http.StatusOK {
		t.Fatalf("verify: %d %s", verified.Code, verified.Body)
	}
	evidence := decodeAdminData(t, verified)
	if !evidence["report"].(map[string]any)["passed"].(bool) {
		t.Fatalf("verification failed: %s", verified.Body)
	}
	revisionHash := evidence["revision"].(map[string]any)["hash"].(string)
	activationBody, _ := json.Marshal(map[string]string{"revisionHash": revisionHash, "expectedActive": ""})
	activated := revisionAdminRequest(t, server.engine, http.MethodPost, "/api/admin/protocols/text-alpha/activate", activationBody, "")
	if activated.Code != http.StatusOK {
		t.Fatalf("activate: %d %s", activated.Code, activated.Body)
	}
	pinned, exists := service.Pin("text-alpha")
	if !exists || pinned.Hash() != revisionHash {
		t.Fatal("activation did not publish selected revision")
	}
	previewBody, _ := json.Marshal(map[string]any{"definition": json.RawMessage(raw), "direction": "decode_request", "input": map[string]any{"deployment": "m", "turns": []any{map[string]any{"actor": "user", "segments": []any{map[string]any{"text": "preview"}}}}}})
	preview := revisionAdminRequest(t, server.engine, http.MethodPost, "/api/admin/protocols/preview", previewBody, "")
	if preview.Code != http.StatusOK {
		t.Fatalf("preview: %d %s", preview.Code, preview.Body)
	}
	previewData := decodeAdminData(t, preview)
	semantic := previewData["semantic"].(map[string]any)
	if semantic["schemaVersion"].(float64) != 1 || semantic["native"] == nil {
		t.Fatalf("preview lost semantic provenance: %s", preview.Body)
	}
	for _, path := range []string{"/api/admin/protocols", "/api/admin/protocols/schema", "/api/admin/protocols/text-alpha/draft", "/api/admin/protocols/text-alpha/revisions", "/api/admin/protocols/text-alpha/revisions/" + revisionHash} {
		response := revisionAdminRequest(t, server.engine, http.MethodGet, path, nil, "")
		if response.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", path, response.Code, response.Body)
		}
	}
}

func TestLegacySaveCannotActivateOrOverwriteVersionedProtocol(t *testing.T) {
	server, _ := newProtocolAdminTestServer(t)
	raw, err := os.ReadFile(filepath.Join("..", "protocol", "testdata", "text-alpha.json"))
	if err != nil {
		t.Fatal(err)
	}
	context, recorder := adminProtocolContext(http.MethodPut, "/api/admin/custom-protocols/text-alpha", string(raw))
	context.Params = gin.Params{{Key: "id", Value: "text-alpha"}}
	server.adminUpsertCustomProtocol(context)
	if recorder.Code != http.StatusOK {
		t.Fatalf("v2 legacy-path draft save: %d %s", recorder.Code, recorder.Body)
	}
	if decodeAdminData(t, recorder)["activated"] != false {
		t.Fatal("legacy save bypassed activation")
	}
	rows, err := server.store.ListCustomProtocols(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatal("v2 draft was also installed into legacy registry")
	}
	legacy := `{"id":"text-alpha","request":{"path":"/chat","body":{"model":{"field":"model"}}},"response":{"textPath":"text"}}`
	context, recorder = adminProtocolContext(http.MethodPut, "/api/admin/custom-protocols/text-alpha", legacy)
	context.Params = gin.Params{{Key: "id", Value: "text-alpha"}}
	server.adminUpsertCustomProtocol(context)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("v1 overwrite escaped revision gate: %d %s", recorder.Code, recorder.Body)
	}
}

func TestProtocolAdminRejectsForgedReportAndActivationWithoutEvidence(t *testing.T) {
	server, _ := newProtocolAdminTestServer(t)
	server.setupProtocolRevisionRoutes(server.engine.Group("/api/admin"))
	response := revisionAdminRequest(t, server.engine, http.MethodPost, "/api/admin/protocols/text-alpha/activate", []byte(`{"revisionHash":"unverified","expectedActive":"","report":{"passed":true}}`), "")
	if response.Code != http.StatusBadRequest {
		t.Fatalf("accepted client report: %d %s", response.Code, response.Body)
	}
	service, err := server.protocolService()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join("..", "protocol", "testdata", "text-alpha.json"))
	if err != nil {
		t.Fatal(err)
	}
	compiled, issues := service.Validate(raw)
	if err := protocol.IssuesError(issues); err != nil {
		t.Fatal(err)
	}
	value, err := protocol.ParseValue(raw)
	if err != nil {
		t.Fatal(err)
	}
	if err := server.store.SaveProtocolRevision(t.Context(), protocol.Revision{ProtocolID: "text-alpha", Hash: compiled.Hash(), Definition: value}); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]string{"revisionHash": compiled.Hash(), "expectedActive": ""})
	response = revisionAdminRequest(t, server.engine, http.MethodPost, "/api/admin/protocols/text-alpha/activate", body, "")
	if response.Code == http.StatusOK {
		t.Fatal("revision without verification activated")
	}
	if _, exists := service.Pin("text-alpha"); exists {
		t.Fatal("invalid activation populated registry")
	}
}
