package server

import (
	"fmt"
	"github.com/elysia-api/backend/protocol"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestGatewayRedundantFailureCodeMapsWithoutSecondaryFailure(t *testing.T) {
	s, _ := newProtocolAdminTestServer(t)
	activateDiscoveryPresets(t, s)
	service, _ := s.protocolService()
	upstream, _ := service.Pin(protocol.PresetChatCompletionsID)
	var calls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(400)
		fmt.Fprint(w, `{"error":{"type":"invalid_request_error","code":"invalid_request_error","param":null,"message":"Thinking mode does not support this tool_choice"}}`)
	}))
	defer provider.Close()
	setupGatewayModel(t, s, upstream, provider.URL)
	s.engine.POST("/v1/messages", s.authMiddleware(), s.chatCompletions)
	for _, path := range []string{"/v1/messages", "/gateway/anthropic-messages/v1/messages"} {
		r := httptest.NewRequest("POST", path, strings.NewReader(`{"model":"group","max_tokens":64,"messages":[{"role":"user","content":"hi"}]}`))
		r.Header.Set("Authorization", "Bearer gateway-test-token")
		out := httptest.NewRecorder()
		s.engine.ServeHTTP(out, r)
		if out.Code != 400 || !strings.Contains(out.Body.String(), "Thinking mode does not support this tool_choice") || strings.Contains(out.Body.String(), "conversion failed") || strings.Contains(out.Body.String(), "unsupported_capability") {
			t.Fatal(out.Code, out.Body.String())
		}
	}
	if calls.Load() != 2 {
		t.Fatal("upstream rejection retried", calls.Load())
	}
}
