package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/elysia-api/backend/storage"
	"github.com/gin-gonic/gin"
)

func mcpTestProtocolDraft(t *testing.T) string {
	t.Helper()
	definition := loadGatewayDefinition(t, "text-alpha")
	definition.ID = "mcp-draft"
	raw, err := json.Marshal(definition)
	if err != nil {
		t.Fatal(err)
	}
	return "elysia protocol draft '" + string(raw) + "'"
}

const mcpProtocolSample = `'{"schemaVersion":1,"model":"m","content":[{"kind":"message","role":"user","children":[{"kind":"text","payload":"hi"}]}]}'`

func callMCPCLI(t *testing.T, s *Server, args map[string]any) (map[string]any, string) {
	t.Helper()
	c, rec := remoteContext(http.MethodPost, "/mcp", mcpRequest(1, "tools/call", map[string]any{
		"name": "elysia_cli", "arguments": args, "_meta": map[string]any{"progressToken": "cli-test"},
	}), mcpHeaders(nil))
	s.handleMCP(c)
	if rec.Code != http.StatusOK {
		t.Fatalf("MCP status: %d %s", rec.Code, rec.Body.String())
	}
	for _, frame := range parseA2AFrames(t, rec.Body.String()) {
		if result, ok := frame["result"].(map[string]any); ok {
			return result, rec.Body.String()
		}
	}
	t.Fatalf("missing MCP result: %s", rec.Body.String())
	return nil, ""
}

func cliMCPData(t *testing.T, result map[string]any, failed bool) map[string]any {
	t.Helper()
	if result["isError"] != failed {
		t.Fatalf("isError: want %v, got %v", failed, result)
	}
	data, ok := result["structuredContent"].(map[string]any)
	if !ok || data["ok"] != !failed {
		t.Fatalf("missing CLI result: %v", result)
	}
	return data
}

func TestMCPCLIDefinitionAndStatelessOperations(t *testing.T) {
	s := newAgentIntegrationServer(t)
	tool := mcpFindTool("elysia_cli")
	if tool == nil || !strings.Contains(tool.description, cliToolDescription) || !strings.Contains(tool.description, "每次调用无状态") {
		t.Fatal("CLI definition is missing its shared contract or direct execution policy")
	}
	properties := tool.schema["properties"].(map[string]any)
	if properties["command"] == nil || properties["sessionId"] != nil || len(tool.schema["required"].([]string)) != 1 {
		t.Fatalf("unexpected input schema: %v", tool.schema)
	}
	if _, hasSession := (&elysiaCLITool{}).Definition().Parameters["properties"].(map[string]any)["sessionId"]; hasSession {
		t.Fatal("MCP-specific session parameter leaked into internal tool")
	}
	result, _ := callMCPCLI(t, s, map[string]any{"command": "elysia help group create"})
	data := cliMCPData(t, result, false)
	if !strings.Contains(data["output"].(string), "服务端权限") {
		t.Fatalf("help lost server-side constraints: %v", data)
	}
	result, raw := callMCPCLI(t, s, map[string]any{"command": "elysia group create --name external-group; elysia group ls"})
	data = cliMCPData(t, result, false)
	if !strings.Contains(data["output"].(string), "external-group") || !strings.Contains(raw, "notifications/progress") {
		t.Fatalf("missing command results/progress: %s", raw)
	}
	groups, err := s.store.ListGroups(t.Context())
	if err != nil || len(groups) != 1 {
		t.Fatalf("direct mutation failed: %v %v", groups, err)
	}
	result, _ = callMCPCLI(t, s, map[string]any{"command": "elysia group delete --group external-group"})
	cliMCPData(t, result, false)
	groups, _ = s.store.ListGroups(t.Context())
	sessions, _ := s.store.ListAgentSessions(t.Context())
	if len(groups) != 0 || len(sessions) != 0 {
		t.Fatal("stateless operations failed or created an assistant session")
	}
}

func TestMCPCLIRejectsSessionID(t *testing.T) {
	s := newAgentIntegrationServer(t)
	result, _ := callMCPCLI(t, s, map[string]any{"command": "elysia group ls", "sessionId": "legacy-session"})
	if !result["isError"].(bool) {
		t.Fatalf("sessionId must be rejected: %v", result)
	}
	content := result["content"].([]any)
	if !strings.Contains(content[len(content)-1].(map[string]any)["text"].(string), "无状态") {
		t.Fatalf("missing stateless error: %v", result)
	}
}

func TestMCPCLIProtocolSaveUpdate(t *testing.T) {
	s := newAgentIntegrationServer(t)
	original := mcpTestProtocolDraft(t)
	result, _ := callMCPCLI(t, s, map[string]any{"command": original + "; elysia protocol save"})
	cliMCPData(t, result, false)
	service, err := s.protocolService()
	if err != nil {
		t.Fatal(err)
	}
	draft, err := service.ReadDraft(t.Context(), "mcp-draft")
	if err != nil {
		t.Fatal(err)
	}
	changed := strings.Replace(original, `"name":"Text Alpha"`, `"name":"Updated Alpha"`, 1)
	if changed == original {
		changed = strings.Replace(original, `"version":"1"`, `"version":"2"`, 1)
	}
	for _, expected := range []string{"", "stale", draft.Hash} {
		suffix := ""
		if expected != "" {
			suffix = " --expected " + expected
		}
		result, _ = callMCPCLI(t, s, map[string]any{"command": changed + "; elysia protocol save" + suffix})
		cliMCPData(t, result, expected != draft.Hash)
		if _, enabled := service.Pin("mcp-draft"); enabled {
			t.Fatal("draft save activated protocol")
		}
	}
	rows, err := s.store.ListCustomProtocols(t.Context())
	if err != nil || len(rows) != 0 {
		t.Fatal("v2 authoring wrote legacy storage", rows, err)
	}
}

func TestMCPCLIHelpCommands(t *testing.T) {
	s := newAgentIntegrationServer(t)
	for _, command := range []string{"elysia", "elysia help", "elysia --help", "elysia | head 1"} {
		t.Run(command, func(t *testing.T) {
			result, _ := callMCPCLI(t, s, map[string]any{"command": command})
			data := cliMCPData(t, result, false)
			if !strings.Contains(data["output"].(string), "网关运维 CLI") {
				t.Fatalf("missing help: %v", data)
			}
		})
	}
	result, _ := callMCPCLI(t, s, map[string]any{"command": "elysia group create --name help-batch; elysia; elysia group ls"})
	data := cliMCPData(t, result, false)
	if data["summary"] != "执行 3 条命令：3 成功、0 失败" || !strings.Contains(data["output"].(string), "help-batch") {
		t.Fatalf("bare command lost batch results: %v", data)
	}
}

func TestMCPCLIPartialFailureAndMasking(t *testing.T) {
	s := newAgentIntegrationServer(t)
	result, raw := callMCPCLI(t, s, map[string]any{"command": "elysia source create --name test --base-url https://example.com --manual-models eval --api-key sk-private-test; elysia missing; elysia source ls"})
	data := cliMCPData(t, result, true)
	output := data["output"].(string)
	if data["exitCode"] != float64(1) || !strings.Contains(output, "未知命令") || !strings.Contains(output, "模型源") {
		t.Fatalf("partial results were lost: %v", data)
	}
	if strings.Contains(raw, "sk-private-test") {
		t.Fatal("credential leaked into echoed command, progress or result")
	}
	// 文本结果同样包含完整的 CLI 结果，老客户端无需 structuredContent 支持。
	content := result["content"].([]any)[0].(map[string]any)["text"].(string)
	if !strings.Contains(content, "未知命令") || !strings.Contains(content, "exitCode") {
		t.Fatalf("text result omitted error context: %s", content)
	}
}

func TestMCPCLIStatelessBatchAndIsolation(t *testing.T) {
	s := newAgentIntegrationServer(t)
	var hits int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		_, _ = w.Write([]byte(`{"requestId":"r","answer":[{"actor":"assistant","segments":[{"text":"ok"}]}]}`))
	}))
	t.Cleanup(upstream.Close)
	batch := mcpTestProtocolDraft(t) + " ; elysia protocol preview --direction encode_request --sample " + mcpProtocolSample + " ; elysia protocol test --operation generate --sample " + mcpProtocolSample + " --base-url " + upstream.URL + " --api-key sk-mcp-batch-secret"
	result, raw := callMCPCLI(t, s, map[string]any{"command": batch})
	data := cliMCPData(t, result, false)
	if !strings.Contains(data["output"].(string), "Shared runtime preview") || hits != 1 || strings.Contains(raw, "sk-mcp-batch-secret") {
		t.Fatalf("batch state or masking failed: %v %s", data, raw)
	}
	result, _ = callMCPCLI(t, s, map[string]any{"command": "elysia protocol preview"})
	data = cliMCPData(t, result, true)
	if !strings.Contains(data["output"].(string), "no_draft") {
		t.Fatalf("state leaked across calls: %v", data)
	}
	sessions, _ := s.store.ListAgentSessions(t.Context())
	if len(sessions) != 0 {
		t.Fatalf("stateless MCP created Agent sessions: %d", len(sessions))
	}
}

func TestMCPCLIDiscoveryAndModernCall(t *testing.T) {
	s := newAgentIntegrationServer(t)
	for _, method := range []string{"initialize", "server/discover"} {
		params := map[string]any{"protocolVersion": mcpLatestLegacyVersion}
		if method == "server/discover" {
			params = map[string]any{"_meta": map[string]any{mcpMetaProtocolVersion: mcpModernProtocolVersion}}
		}
		_, response, _ := mcpCall(t, s, mcpRequest(1, method, params), nil)
		result, ok := response["result"].(map[string]any)
		if !ok || result["instructions"] != mcpInstructions {
			t.Fatalf("%s does not advertise direct CLI authorization: %v", method, response)
		}
	}
	c, rec := remoteContext(http.MethodPost, "/mcp", mcpRequest(2, "tools/call", map[string]any{
		"name": "elysia_cli", "arguments": map[string]any{"command": "elysia group ls"},
		"_meta": map[string]any{mcpMetaProtocolVersion: mcpModernProtocolVersion, "progressToken": "modern-cli"},
	}), mcpHeaders(map[string]string{"MCP-Protocol-Version": mcpModernProtocolVersion, "Mcp-Method": "tools/call", "Mcp-Name": "elysia_cli"}))
	s.handleMCP(c)
	frames := parseA2AFrames(t, rec.Body.String())
	if rec.Code != http.StatusOK || len(frames) < 2 {
		t.Fatalf("modern call missing progress/result: %s", rec.Body.String())
	}
	result, ok := frames[len(frames)-1]["result"].(map[string]any)
	if !ok || result["resultType"] != "complete" {
		t.Fatalf("modern result missing completion: %v", frames)
	}
	cliMCPData(t, result, false)
}

func TestMCPCLICancellationIsStateless(t *testing.T) {
	s := newAgentIntegrationServer(t)
	started := make(chan struct{})
	release := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		close(started)
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	t.Cleanup(func() { close(release); upstream.Close() })
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	args, _ := json.Marshal(map[string]any{
		"command": mcpTestProtocolDraft(t) + " ; elysia protocol test --operation generate --sample " + mcpProtocolSample + " --base-url " + upstream.URL + "; elysia group create --name must-not-run",
	})
	type outcome struct {
		data any
		err  error
	}
	done := make(chan outcome, 1)
	go func() {
		data, err := s.runMCPCLI(ctx, args, nil)
		done <- outcome{data, err}
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("outbound command did not start")
	}
	cancel()
	select {
	case got := <-done:
		data, ok := got.data.(map[string]any)
		if !errors.Is(got.err, context.Canceled) || !ok || data["ok"] != false || data["output"] == nil {
			t.Fatalf("cancellation lost status or partial output: %v %v", got.data, got.err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("request cancellation did not stop CLI execution")
	}
	groups, err := s.store.ListGroups(t.Context())
	if err != nil || len(groups) != 0 {
		t.Fatalf("commands continued after cancellation: %v %v", groups, err)
	}
	sessions, _ := s.store.ListAgentSessions(t.Context())
	if len(sessions) != 0 {
		t.Fatalf("cancellation created an Agent session: %d", len(sessions))
	}
}

func TestMCPCLICancelledStatelessRequest(t *testing.T) {
	s := newAgentIntegrationServer(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	data, err := s.runMCPCLI(ctx, json.RawMessage(`{"command":"elysia group create --name must-not-run"}`), nil)
	if !errors.Is(err, context.Canceled) || data.(map[string]any)["ok"] != false {
		t.Fatalf("cancelled request reported success: %v %v", data, err)
	}
	groups, err := s.store.ListGroups(t.Context())
	if err != nil || len(groups) != 0 {
		t.Fatalf("cancelled request changed data: %v %v", groups, err)
	}
}

func TestMCPCLIAuthAndRemoteSwitch(t *testing.T) {
	s := newAgentIntegrationServer(t)
	for name, scopes := range map[string][]string{"plain": nil, "operator": {storage.TokenScopeAgent}} {
		if err := s.store.UpsertAPIToken(t.Context(), storage.APIToken{Name: name, Token: name, Enabled: true, Scopes: scopes}); err != nil {
			t.Fatal(err)
		}
	}
	s.invalidateRouteCache()
	router := gin.New()
	router.POST("/mcp", s.agentRemoteGate(), s.agentRemoteAuth(), s.handleMCP)
	for _, tc := range []struct {
		key    string
		status int
	}{{"", 401}, {"plain", 403}, {"operator", 200}} {
		req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(mcpRequest(1, "tools/call", map[string]any{"name": "elysia_cli", "arguments": map[string]any{"command": "elysia group ls"}})))
		req.Header.Set("Authorization", "Bearer "+tc.key)
		req.Header.Set("Accept", "application/json, text/event-stream")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != tc.status {
			t.Fatalf("key %q: status=%d want=%d", tc.key, rec.Code, tc.status)
		}
	}
	s.config.AgentRemote.Enabled = new(bool)
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(mcpRequest(2, "tools/list", nil)))
	req.Header.Set("Authorization", "Bearer operator")
	req.Header.Set("Accept", "application/json, text/event-stream")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("remote disabled: status=%d", rec.Code)
	}
}
