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

func TestGatewayAuditFailureDetailsReachAllClients(t *testing.T) {
	s, _ := newProtocolAdminTestServer(t)
	activateDiscoveryPresets(t, s)
	service, _ := s.protocolService()
	upstream, _ := service.Pin(protocol.PresetChatCompletionsID)
	var isStreaming atomic.Bool
	var calls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if isStreaming.Load() {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "data: {\"error\":{\"type\":\"rate_limit_error\",\"code\":\"gateway_concurrency_limit\",\"message\":\"synthetic limit\"}}\n\n")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(400)
		fmt.Fprint(w, `{"error":{"type":"invalid_request_error","message":"synthetic tool_choice.name","code":null,"param":""}}`)
	}))
	defer provider.Close()
	setupGatewayModel(t, s, upstream, provider.URL)
	for _, ingress := range []struct{ id, path, body string }{
		{protocol.PresetChatCompletionsID, "/chat/completions", `{"model":"group","messages":[{"role":"user","content":"hi"}],"stream":%v}`},
		{protocol.PresetResponsesID, "/responses", `{"model":"group","input":"hi","store":false,"stream":%v}`},
		{protocol.PresetAnthropicID, "/v1/messages", `{"model":"group","max_tokens":32,"messages":[{"role":"user","content":"hi"}],"stream":%v}`},
		{protocol.PresetGeminiID, "/v1beta/models/group:generateContent", `{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`},
	} {
		for _, stream := range []bool{false, true} {
			isStreaming.Store(stream)
			body, path := ingress.body, ingress.path
			if ingress.id == protocol.PresetGeminiID {
				if stream {
					path = "/v1beta/models/group:streamGenerateContent"
				}
			} else {
				body = fmt.Sprintf(body, stream)
			}
			req := httptest.NewRequest("POST", "/gateway/"+ingress.id+path, strings.NewReader(body))
			req.Header.Set("Authorization", "Bearer gateway-test-token")
			out := httptest.NewRecorder()
			before := calls.Load()
			s.engine.ServeHTTP(out, req)
			want := "synthetic tool_choice.name"
			if stream {
				want = "gateway_concurrency_limit"
			}
			if calls.Load() != before+1 || !strings.Contains(out.Body.String(), want) || strings.Contains(out.Body.String(), "unsupported_capability") {
				t.Fatal(ingress.id, stream, calls.Load()-before, out.Code, out.Body.String())
			}
			if stream && out.Result().Trailer.Get(gatewayStreamErrorTrailer) == "" || !stream && out.Code != 400 {
				t.Fatal(out.Code, out.Header())
			}
		}
	}
}
