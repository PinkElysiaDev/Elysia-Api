package server

import (
	"fmt"
	"github.com/elysia-api/backend/protocol"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGatewayVisibleReasoningHistoryAndSummaryRejection(t *testing.T) {
	s, _ := newProtocolAdminTestServer(t)
	activateDiscoveryPresets(t, s)
	service, _ := s.protocolService()
	upstream, _ := service.Pin(protocol.PresetChatCompletionsID)
	calls := 0
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"r","created":1,"object":"chat.completion","model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"OK"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
	}))
	defer provider.Close()
	setupGatewayModel(t, s, upstream, provider.URL)
	for _, item := range []string{
		`{"type":"reasoning","summary":[],"content":[{"type":"reasoning_text","text":"visible"}]}`,
		`{"type":"reasoning","summary":[{"type":"summary_text","text":"summary"}]}`,
		`{"type":"reasoning","summary":[],"content":[{"type":"reasoning_text","text":"part1"},{"type":"reasoning_text","text":"part2"}]}`,
	} {
		r := httptest.NewRequest("POST", "/gateway/openai-responses/responses", strings.NewReader(`{"model":"group","store":false,"input":[{"role":"user","content":"hi"},`+item+`,{"role":"assistant","content":"OK"},{"role":"user","content":"continue"}]}`))
		r.Header.Set("Authorization", "Bearer gateway-test-token")
		out := httptest.NewRecorder()
		s.engine.ServeHTTP(out, r)
		if strings.Contains(item, `"text":"visible"`) {
			if out.Code != 200 {
				t.Fatal(out.Code, out.Body.String())
			}
		} else if out.Code != 400 {
			t.Fatal("unsupported history accepted", out.Code, out.Body.String())
		}
		if calls != 1 {
			t.Fatal("rejected history reached upstream", calls)
		}
	}
}
