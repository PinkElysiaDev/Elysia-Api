package server

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elysia-api/backend/storage"
	"github.com/gin-gonic/gin"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type mcpTestTransport func(*http.Request) (*http.Response, error)

func (f mcpTestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// Exercise the production Gin/auth boundary with the official client: this
// catches transport/framing errors that direct tool-handler tests cannot see.
func TestMCPOfficialClient(t *testing.T) {
	s := newAgentIntegrationServer(t)
	token := storage.APIToken{Name: "sdk", Token: "sdk-key", Enabled: true, Scopes: []string{storage.TokenScopeAgent}}
	if err := s.store.UpsertAPIToken(t.Context(), token); err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	router.Any("/mcp", s.agentRemoteGate(), s.agentRemoteAuth(), s.handleMCP)
	httpServer := httptest.NewServer(router)
	t.Cleanup(httpServer.Close)
	var sessionHeader atomic.Bool
	clientHTTP := httpServer.Client()
	transport := clientHTTP.Transport
	clientHTTP.Transport = mcpTestTransport(func(r *http.Request) (*http.Response, error) {
		r = r.Clone(r.Context())
		r.Header.Set("Authorization", "Bearer "+token.Token)
		if r.Header.Get("Mcp-Session-Id") != "" {
			sessionHeader.Store(true)
		}
		response, err := transport.RoundTrip(r)
		if response != nil && response.Header.Get("Mcp-Session-Id") != "" {
			sessionHeader.Store(true)
		}
		return response, err
	})
	var progressMu sync.Mutex
	var progress []mcp.ProgressNotificationParams
	client := mcp.NewClient(&mcp.Implementation{Name: "migration-test", Version: "1"}, &mcp.ClientOptions{
		Capabilities: &mcp.ClientCapabilities{},
		ProgressNotificationHandler: func(_ context.Context, req *mcp.ProgressNotificationClientRequest) {
			progressMu.Lock()
			defer progressMu.Unlock()
			progress = append(progress, *req.Params)
		},
	})
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint: httpServer.URL + "/mcp", HTTPClient: clientHTTP, MaxRetries: -1,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	info := session.InitializeResult()
	if info.ProtocolVersion != mcpModernProtocolVersion || info.Instructions != mcpInstructions || info.ServerInfo.Name != mcpServerName {
		t.Fatalf("discovery lost server identity or instructions: %+v", info)
	}
	tools, err := session.ListTools(ctx, nil)
	if err != nil || len(tools.Tools) != 1 || tools.Tools[0].Name != agentToolCLI || tools.TTLMs != 300_000 || tools.CacheScope != "private" {
		t.Fatalf("tool listing: %+v, %v", tools, err)
	}
	params := &mcp.CallToolParams{Name: agentToolCLI, Arguments: map[string]any{
		"command": "elysia group create --name sdk-group; elysia missing; elysia group ls",
	}}
	params.SetProgressToken("sdk-progress")
	result, err := session.CallTool(ctx, params)
	if err != nil || result == nil || !result.IsError || result.StructuredContent == nil || len(result.Content) != 2 {
		t.Fatalf("business failure must retain partial output, not become a protocol error: %+v, %v", result, err)
	}
	if text, ok := result.Content[0].(*mcp.TextContent); !ok || !strings.Contains(text.Text, "sdk-group") {
		t.Fatalf("partial output missing from text: %+v", result)
	}
	progressMu.Lock()
	if len(progress) == 0 {
		t.Error("missing progress notifications")
	}
	for i, notification := range progress {
		if notification.ProgressToken != "sdk-progress" || notification.Progress != float64(i+1) || notification.Message == "" {
			t.Errorf("progress %d: %+v", i, notification)
		}
	}
	progressMu.Unlock()
	if sessionHeader.Load() {
		t.Fatal("stateless endpoint negotiated a transport session")
	}
	// Client-side sessions must not bypass fresh authorization on later POSTs.
	token.Enabled = false
	if err := s.store.UpsertAPIToken(ctx, token); err != nil {
		t.Fatal(err)
	}
	s.invalidateRouteCache()
	if _, err := session.CallTool(ctx, &mcp.CallToolParams{Name: agentToolCLI, Arguments: map[string]any{
		"command": "elysia group create --name must-not-run",
	}}); err == nil {
		t.Fatal("revoked credential was accepted on an existing SDK client")
	}
	groups, err := s.store.ListGroups(ctx)
	if err != nil || len(groups) != 1 || groups[0].Name != "sdk-group" {
		t.Fatalf("unexpected mutation after revocation: %+v, %v", groups, err)
	}
}

func TestMCPGatewayHTTPPolicy(t *testing.T) {
	s := newAgentIntegrationServer(t)
	router := gin.New()
	router.Any("/mcp", s.handleMCP)
	body := mcpRequest(1, "tools/call", map[string]any{"name": agentToolCLI, "arguments": map[string]any{"command": "elysia help"}})
	for _, tc := range []struct {
		name, method, body string
		headers            map[string]string
		status             int
	}{
		{"missing version", "POST", body, map[string]string{"MCP-Protocol-Version": ""}, 400},
		{"legacy version", "POST", body, map[string]string{"MCP-Protocol-Version": "2025-11-25"}, 400},
		{"foreign origin", "POST", body, map[string]string{"Origin": "https://other.example"}, 403},
		{"same origin", "POST", body, map[string]string{"Origin": "http://example.com"}, 200},
		{"loopback origin", "POST", body, map[string]string{"Origin": "http://localhost:3000"}, 200},
		{"get", "GET", "", nil, 405},
		{"delete", "DELETE", "", nil, 405},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, "http://example.com/mcp", strings.NewReader(tc.body))
			for key, value := range mcpHeaders(body, tc.headers) {
				r.Header.Set(key, value)
			}
			w := httptest.NewRecorder()
			router.ServeHTTP(w, r)
			if w.Code != tc.status {
				t.Fatalf("status=%d want=%d: %s", w.Code, tc.status, w.Body.String())
			}
		})
	}
	// An oversized valid prefix must never be silently truncated into a command.
	// Unknown length models chunked bodies as well as requests with Content-Length.
	oversized := body + strings.Repeat(" ", agentMaxBodyBytes)
	for _, unknownLength := range []bool{false, true} {
		r := httptest.NewRequest(http.MethodPost, "/mcp", io.NopCloser(strings.NewReader(oversized)))
		if !unknownLength {
			r.ContentLength = int64(len(oversized))
		}
		for key, value := range mcpHeaders(body, nil) {
			r.Header.Set(key, value)
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		if w.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("unknownLength=%v: status=%d want=413", unknownLength, w.Code)
		}
	}
}
