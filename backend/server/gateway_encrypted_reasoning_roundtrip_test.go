package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elysia-api/backend/protocol"
	"github.com/elysia-api/backend/storage"
)

// Synthetic visible reasoning plus opaque provider state, matching the shape
// found in the real Responses baseline. No real encrypted payload is stored.
func TestGatewayResponsesEncryptedReasoningContinuation(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprint(stream), func(t *testing.T) {
			s, cfgPath := newProtocolAdminTestServer(t)
			s.store.Close()
			var openErr error
			s.store, openErr = storage.OpenWithKey(filepath.Join(filepath.Dir(cfgPath), "encrypted.sqlite3"), s.config.GetDBEncryptionKey())
			if openErr != nil {
				t.Fatal(openErr)
			}
			t.Cleanup(func() { s.store.Close() })
			activateDiscoveryPresets(t, s)
			service, _ := s.protocolService()
			upstream, _ := service.Pin(protocol.PresetResponsesID)
			const reasoning = `{"type":"reasoning","id":"thought","status":"completed","summary":[],"content":[{"type":"reasoning_text","text":"visible thought"}],"encrypted_content":"synthetic-opaque-state"}`
			const message = `{"type":"message","id":"msg","role":"assistant","status":"completed","content":[{"type":"output_text","text":"OK","annotations":[]}]}`
			const complete = `{"id":"resp","object":"response","created_at":1,"model":"upstream-model","status":"completed","output":[` + reasoning + `,` + message + `],"usage":{"input_tokens":1,"output_tokens":2,"total_tokens":3}}`
			calls := 0
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				raw, _ := io.ReadAll(r.Body)
				if calls == 2 && (!strings.Contains(string(raw), `"encrypted_content":"synthetic-opaque-state"`) || !strings.Contains(string(raw), "visible thought")) {
					t.Error("original reasoning not restored", string(raw))
				}
				if strings.Contains(string(raw), protocol.ContinuationPrefix) {
					t.Error("gateway carrier leaked upstream")
				}
				if !stream || calls == 2 {
					w.Header().Set("Content-Type", "application/json")
					fmt.Fprint(w, complete)
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				frames := []string{
					`{"type":"response.created","response":{"id":"resp","object":"response","created_at":1,"model":"upstream-model","status":"in_progress","output":[]}}`,
					`{"type":"response.output_item.added","output_index":0,"item":{"type":"reasoning","id":"thought","status":"in_progress","summary":[],"content":[]}}`,
					`{"type":"response.content_part.added","output_index":0,"item_id":"thought","content_index":0,"part":{"type":"reasoning_text","text":""}}`,
					`{"type":"response.reasoning_text.delta","output_index":0,"item_id":"thought","content_index":0,"delta":"visible thought"}`,
					`{"type":"response.reasoning_text.done","output_index":0,"item_id":"thought","content_index":0,"text":"visible thought"}`,
					`{"type":"response.content_part.done","output_index":0,"item_id":"thought","content_index":0,"part":{"type":"reasoning_text","text":"visible thought"}}`,
					`{"type":"response.output_item.done","output_index":0,"item":` + reasoning + `}`,
					`{"type":"response.output_item.added","output_index":1,"item":{"type":"message","id":"msg","role":"assistant","status":"in_progress","content":[]}}`,
					`{"type":"response.content_part.added","output_index":1,"item_id":"msg","content_index":0,"part":{"type":"output_text","text":"","annotations":[]}}`,
					`{"type":"response.output_text.delta","output_index":1,"item_id":"msg","content_index":0,"delta":"OK"}`,
					`{"type":"response.output_text.done","output_index":1,"item_id":"msg","content_index":0,"text":"OK"}`,
					`{"type":"response.content_part.done","output_index":1,"item_id":"msg","content_index":0,"part":{"type":"output_text","text":"OK","annotations":[]}}`,
					`{"type":"response.output_item.done","output_index":1,"item":` + message + `}`,
					`{"type":"response.completed","response":` + complete + `}`,
				}
				for i, frame := range frames {
					fmt.Fprintf(w, "data: %s,\"sequence_number\":%d}\n\n", strings.TrimSuffix(frame, "}"), i)
				}
			}))
			defer provider.Close()
			setupGatewayModel(t, s, upstream, provider.URL)
			call := func(body string) *httptest.ResponseRecorder {
				r := httptest.NewRequest("POST", "/gateway/anthropic-messages/v1/messages", strings.NewReader(body))
				r.Header.Set("Authorization", "Bearer gateway-test-token")
				r.Header.Set("x-elysia-session-id", "encrypted-reasoning")
				rec := httptest.NewRecorder()
				s.engine.ServeHTTP(rec, r)
				return rec
			}
			first := call(fmt.Sprintf(`{"model":"group","max_tokens":64,"stream":%v,"messages":[{"role":"user","content":"hi"}]}`, stream))
			if first.Code != 200 || first.Result().Trailer.Get(gatewayStreamErrorTrailer) != "" || !strings.Contains(first.Body.String(), "OK") || !strings.Contains(first.Body.String(), protocol.ContinuationPrefix) {
				t.Fatal(first.Code, first.Header(), first.Body.String())
			}
			var content json.RawMessage
			if !stream {
				var body struct{ Content json.RawMessage }
				if err := json.Unmarshal(first.Body.Bytes(), &body); err != nil {
					t.Fatal(err)
				}
				content = body.Content
			} else {
				blocks := map[int]map[string]any{}
				for _, line := range strings.Split(first.Body.String(), "\n") {
					if !strings.HasPrefix(line, "data: {") {
						continue
					}
					var event struct {
						Type         string
						Index        int
						ContentBlock map[string]any `json:"content_block"`
						Delta        map[string]any
					}
					if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event); err != nil {
						t.Fatal(err)
					}
					if event.Type == "content_block_start" {
						blocks[event.Index] = event.ContentBlock
					}
					if event.Type == "content_block_delta" {
						for _, key := range []string{"text", "thinking", "signature"} {
							if value, ok := event.Delta[key].(string); ok {
								prev, _ := blocks[event.Index][key].(string)
								blocks[event.Index][key] = prev + value
							}
						}
					}
				}
				ordered := make([]map[string]any, len(blocks))
				for i := range ordered {
					ordered[i] = blocks[i]
				}
				content, _ = json.Marshal(ordered)
			}
			second := call(`{"model":"group","max_tokens":64,"messages":[{"role":"user","content":"hi"},{"role":"assistant","content":` + string(content) + `},{"role":"user","content":"continue"}]}`)
			if second.Code != 200 || calls != 2 {
				t.Fatal(second.Code, calls, second.Body.String())
			}
			stats, err := s.store.ContinuationStats(t.Context())
			if err != nil || stats["records"] < 1 {
				t.Fatal(stats, err)
			}
		})
	}
}
