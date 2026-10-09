package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/elysia-api/backend/protocol"
)

func TestGatewayGeminiThinkingOnlyTotalToChat(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprint(stream), func(t *testing.T) {
			s, _ := newProtocolAdminTestServer(t)
			activateDiscoveryPresets(t, s)
			service, _ := s.protocolService()
			upstream, _ := service.Pin(protocol.PresetGeminiID)
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body := `{"responseId":"r","candidates":[{"index":0,"content":{"role":"model","parts":[{"text":"OK"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":17,"thoughtsTokenCount":202,"totalTokenCount":219}}`
				if stream {
					w.Header().Set("Content-Type", "text/event-stream")
					fmt.Fprint(w, "data: "+body+"\n\n")
				} else {
					w.Header().Set("Content-Type", "application/json")
					fmt.Fprint(w, body)
				}
			}))
			defer provider.Close()
			setupGatewayModel(t, s, upstream, provider.URL)
			body := fmt.Sprintf(`{"model":"group","messages":[{"role":"user","content":"hi"}],"stream":%v,"stream_options":{"include_usage":true}}`, stream)
			req := httptest.NewRequest("POST", "/gateway/openai-chat-completions/chat/completions", strings.NewReader(body))
			req.Header.Set("Authorization", "Bearer gateway-test-token")
			rec := httptest.NewRecorder()
			s.engine.ServeHTTP(rec, req)
			if rec.Code != 200 || rec.Result().Trailer.Get(gatewayStreamErrorTrailer) != "" || !strings.Contains(rec.Body.String(), `"completion_tokens":202`) {
				t.Fatal(rec.Code, rec.Body.String())
			}
			items := latestUsageRecords(t, s)
			if len(items) != 1 {
				t.Fatal(items)
			}
			data, _, err := s.store.GetUsageRecordJSON(t.Context(), items[0].RequestID)
			if err != nil {
				t.Fatal(err)
			}
			var record usageRecord
			if err = json.Unmarshal(data, &record); err != nil {
				t.Fatal(err)
			}
			u := record.ProtocolUsage
			if u == nil || u.Output == nil || u.Output.Count != 202 || u.Output.Origin != protocol.InferredCount || u.Details["output.reasoning_tokens"].Origin != protocol.ObservedCount || u.Total.Count != 219 {
				t.Fatal(u)
			}
		})
	}
}
