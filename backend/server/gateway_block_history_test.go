package server

import (
	"encoding/json"
	"fmt"
	"github.com/elysia-api/backend/protocol"
	"github.com/elysia-api/backend/storage"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestGatewayResponsesReturnsSignedThinkingWithItsAnthropicToolTurn(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprint(stream), func(t *testing.T) {
			s, cfg := newProtocolAdminTestServer(t)
			s.store.Close()
			var err error
			s.store, err = storage.OpenWithKey(filepath.Join(filepath.Dir(cfg), "history.sqlite3"), s.config.GetDBEncryptionKey())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { s.store.Close() })
			activateDiscoveryPresets(t, s)
			service, _ := s.protocolService()
			upstream, _ := service.Pin(protocol.PresetAnthropicID)
			calls := 0
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if calls == 2 {
					var body struct {
						Messages []struct {
							Role    string
							Content []struct {
								Type      string
								Thinking  string
								Signature string
								Text      string
								ID        string
							}
						}
					}
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
						w.WriteHeader(400)
						return
					}
					if len(body.Messages) != 3 || body.Messages[1].Role != "assistant" || len(body.Messages[1].Content) != 3 {
						t.Error("thinking, text and tool split across messages")
						w.WriteHeader(400)
						return
					}
					blocks := body.Messages[1].Content
					if blocks[0].Thinking != "visible thought" || blocks[0].Signature != "synthetic-provider-signature" || blocks[1].Text != "before tool" || blocks[2].Type != "tool_use" || blocks[2].ID != "call1" {
						t.Error("signed block or order lost")
						w.WriteHeader(400)
						return
					}
					w.Header().Set("Content-Type", "application/json")
					fmt.Fprint(w, `{"id":"r2","model":"upstream-model","type":"message","role":"assistant","content":[{"type":"text","text":"OK"}],"stop_reason":"end_turn","stop_sequence":null,"usage":{"input_tokens":1,"output_tokens":1}}`)
					return
				}
				if !stream {
					w.Header().Set("Content-Type", "application/json")
					fmt.Fprint(w, `{"id":"r1","model":"upstream-model","type":"message","role":"assistant","content":[{"type":"thinking","thinking":"visible thought","signature":"synthetic-provider-signature"},{"type":"text","text":"before tool"},{"type":"tool_use","id":"call1","name":"echo","input":{}}],"stop_reason":"tool_use","stop_sequence":null,"usage":{"input_tokens":1,"output_tokens":2}}`)
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				frames := []string{
					`{"type":"message_start","message":{"id":"r1","model":"upstream-model","type":"message","role":"assistant","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":1,"output_tokens":0}}}`,
					`{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}`,
					`{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"visible thought"}}`,
					`{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"synthetic-provider-signature"}}`,
					`{"type":"content_block_stop","index":0}`,
					`{"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}`,
					`{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"before tool"}}`,
					`{"type":"content_block_stop","index":1}`,
					`{"type":"content_block_start","index":2,"content_block":{"type":"tool_use","id":"call1","name":"echo","input":{}}}`,
					`{"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"{}"}}`,
					`{"type":"content_block_stop","index":2}`,
					`{"type":"message_delta","delta":{"stop_reason":"tool_use","stop_sequence":null},"usage":{"output_tokens":2}}`,
					`{"type":"message_stop"}`,
				}
				for _, frame := range frames {
					fmt.Fprintf(w, "data: %s\n\n", frame)
				}
			}))
			defer provider.Close()
			setupGatewayModel(t, s, upstream, provider.URL)
			if err = s.store.ReplaceSourceModels(t.Context(), storage.ModelSource{ID: "gateway-source", Name: "gateway-source", BaseURL: provider.URL, Platform: "custom:" + protocol.PresetAnthropicID, Enabled: true}, []storage.Model{{ID: "upstream-model", Name: "upstream-model", Available: true, Enabled: true, ToolsCapable: true}}); err != nil {
				t.Fatal(err)
			}
			if err = s.store.UpsertGroup(t.Context(), storage.ModelGroup{ID: "gateway-group", Name: "group", Enabled: true, ToolsCapable: true, Models: []string{"gateway-source:upstream-model"}, Strategy: "sequential"}); err != nil {
				t.Fatal(err)
			}
			s.invalidateRouteCache()
			call := func(body string) *httptest.ResponseRecorder {
				r := httptest.NewRequest("POST", "/gateway/openai-responses/responses", strings.NewReader(body))
				r.Header.Set("Authorization", "Bearer gateway-test-token")
				r.Header.Set("x-elysia-session-id", "block-history")
				out := httptest.NewRecorder()
				s.engine.ServeHTTP(out, r)
				return out
			}
			first := call(fmt.Sprintf(`{"model":"group","max_output_tokens":64,"store":false,"include":["reasoning.encrypted_content"],"stream":%v,"input":[{"role":"user","content":"hi"}]}`, stream))
			if first.Code != 200 || first.Result().Trailer.Get(gatewayStreamErrorTrailer) != "" {
				t.Fatal(first.Code, first.Body.String())
			}
			var response struct{ Output []json.RawMessage }
			if stream {
				for _, line := range strings.Split(first.Body.String(), "\n") {
					if !strings.HasPrefix(line, "data: {") {
						continue
					}
					var e struct {
						Type     string
						Response json.RawMessage
					}
					if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &e) == nil && e.Type == "response.completed" {
						if err = json.Unmarshal(e.Response, &response); err != nil {
							t.Fatal(err)
						}
					}
				}
			} else if err = json.Unmarshal(first.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if len(response.Output) < 3 || !strings.Contains(first.Body.String(), protocol.ContinuationPrefix) {
				t.Fatal("missing output/carrier")
			}
			items := []json.RawMessage{json.RawMessage(`{"role":"user","content":"hi"}`)}
			items = append(items, response.Output...)
			items = append(items, json.RawMessage(`{"type":"function_call_output","call_id":"call1","output":"done"}`))
			history, _ := json.Marshal(items)
			second := call(`{"model":"group","max_output_tokens":64,"store":false,"input":` + string(history) + `}`)
			if second.Code != 200 || calls != 2 {
				t.Fatal(second.Code, calls, second.Body.String())
			}
		})
	}
}
