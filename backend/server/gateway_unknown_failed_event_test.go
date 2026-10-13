package server

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/elysia-api/backend/protocol"
)

func TestGatewayUnknownFailedEventNeverReplays(t *testing.T) {
	s, _ := newProtocolAdminTestServer(t)
	activateDiscoveryPresets(t, s)
	service, _ := s.protocolService()
	upstream, _ := service.Pin(protocol.PresetResponsesID)
	var calls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: ***.failed\ndata: {\"type\":\"***.failed\",\"sequence_number\":0,\"response\":{\"id\":\"r\",\"model\":\"m\",\"created_at\":1,\"status\":\"failed\",\"output\":[],\"error\":{\"code\":\"gateway_concurrency_limit\",\"message\":\"synthetic provider limit\"}}}\n\n")
	}))
	defer provider.Close()
	setupGatewayModel(t, s, upstream, provider.URL)
	for _, client := range []struct{ path, body string }{
		{"/gateway/openai-responses/responses", `{"model":"group","input":"hi","stream":true,"store":false}`},
		{"/gateway/anthropic-messages/v1/messages", `{"model":"group","messages":[{"role":"user","content":"hi"}],"max_tokens":32,"stream":true}`},
	} {
		req := httptest.NewRequest("POST", client.path, strings.NewReader(client.body))
		req.Header.Set("Authorization", "Bearer gateway-test-token")
		out := httptest.NewRecorder()
		s.engine.ServeHTTP(out, req)
		if out.Code != 429 || !strings.Contains(out.Body.String(), "synthetic provider limit") || !strings.Contains(out.Body.String(), "upstream_contract_violation at /type") || strings.Contains(out.Body.String(), "event: ***.failed") || strings.Contains(out.Body.String(), "unsupported_native") {
			t.Fatal(out.Code, out.Body.String())
		}
	}
	if calls.Load() != 2 {
		t.Fatal("generation retried", calls.Load())
	}
}
