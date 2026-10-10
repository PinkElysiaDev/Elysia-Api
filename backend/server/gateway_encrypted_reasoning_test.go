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
	"github.com/elysia-api/backend/protocol/builtin"
	"github.com/elysia-api/backend/storage"
)

// Synthetic visible reasoning plus opaque provider state, matching the shape
// found in the real Responses baseline. No real encrypted payload is stored.
func TestGatewayResponsesEncryptedReasoningProjection(t *testing.T) {
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
				if strings.Contains(string(raw), protocol.ContinuationPrefix) {
					t.Error("gateway carrier leaked upstream")
				}
				if !stream {
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
			history := []byte(`{"messages":[{"role":"assistant","content":` + string(content) + `}]}`)
			_, tokens, err := builtin.ExtractContinuationCarriers(history, "claude", 1<<20)
			if err != nil || len(tokens) != 1 || calls != 1 {
				t.Fatal("carrier count", len(tokens), calls, err)
			}
			codec, _ := protocol.NewContinuationCodec(s.config.GetDBEncryptionKey())
			stored, err := codec.Open(tokens[0], 1<<20)
			if err != nil || len(stored.Node.Resources) != 1 || stored.Node.Resources[0].Kind != "encrypted_content" || stored.Node.Resources[0].ID != protocol.StringValue("synthetic-opaque-state") {
				t.Fatal("carrier lost original state", stored.Node.Resources, err)
			}
			found := false
			rows := latestUsageRecords(t, s)
			if len(rows) != 1 {
				t.Fatal(rows)
			}
			data, _, err := s.store.GetUsageRecordJSON(t.Context(), rows[0].RequestID)
			if err != nil {
				t.Fatal(err)
			}
			var record usageRecord
			if err := json.Unmarshal(data, &record); err != nil {
				t.Fatal(err)
			}
			for _, issue := range record.ConversionIssues {
				if issue.Fidelity == "recoverable_wrapped" && strings.HasSuffix(issue.RuleID, "-signatures") && issue.PolicyHash != "" {
					found = true
				}
			}
			if !found {
				t.Fatal("recovery evidence not persisted", record.ConversionIssues)
			}
			stats, err := s.store.ContinuationStats(t.Context())
			if err != nil || stats["records"] < 1 {
				t.Fatal(stats, err)
			}
		})
	}
}
