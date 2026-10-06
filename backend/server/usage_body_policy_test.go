package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elysia-api/backend/agent"
	"github.com/elysia-api/backend/config"
)

func TestGatewayUsageBodyPolicy(t *testing.T) {
	zero, enabled := 0, 1024
	for _, policy := range []struct {
		name  string
		limit *int
	}{{"default", nil}, {"disabled", &zero}, {"enabled", &enabled}} {
		for _, endpoint := range []string{"chat", "responses"} {
			for _, outcome := range []string{"success", "stream", "error"} {
				t.Run(policy.name+"/"+endpoint+"/"+outcome, func(t *testing.T) {
					upstream := newCapturingUpstream(t, func(w http.ResponseWriter, _ string, _ int) {
						if outcome == "error" {
							w.WriteHeader(http.StatusBadRequest)
							fmt.Fprint(w, `{"error":{"message":"bad request","type":"invalid_request_error"}}`)
						} else if outcome == "stream" {
							w.Header().Set("Content-Type", "text/event-stream")
							fmt.Fprint(w, openAIChunk("c1", map[string]any{"role": "assistant", "content": "private-response"}, "", nil))
							fmt.Fprint(w, openAIChunk("c1", map[string]any{}, "stop", map[string]any{"prompt_tokens": 3, "completion_tokens": 2, "total_tokens": 5}), openAIDone())
						} else {
							w.Header().Set("Content-Type", "application/json")
							fmt.Fprint(w, `{"id":"c1","object":"chat.completion","model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"private-response"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}`)
						}
					})
					model := openAIModel("m", upstream.URL)
					model.VisionCapable = true
					s := newTestServerWithStore(t, []config.ModelGroupConfig{{ID: "g", Name: "grp", Enabled: true, VisionCapable: boolPtr(true), Strategy: "sequential", MaxRetries: 1, Models: []config.ModelRef{model}}})
					defer s.stopUsageWriter()
					s.config.SetDatabasePath(filepath.Join(t.TempDir(), "usage.sqlite3"))
					s.config.SetUsageLogConfig(config.UsageLogConfig{BodyMaxKB: policy.limit})
					media := "data:image/png;base64," + strings.Repeat("A", 600)
					var response *httptest.ResponseRecorder
					if endpoint == "chat" {
						c, rec := chatRequestContext(fmt.Sprintf(`{"model":"grp","stream":%t,"messages":[{"role":"user","content":[{"type":"text","text":"private-request"},{"type":"image_url","image_url":{"url":%q}}]}]}`, outcome == "stream", media))
						s.chatCompletions(c)
						response = rec
					} else {
						response = httptest.NewRecorder()
						c, _ := newResponsesContext(response, fmt.Sprintf(`{"model":"grp","stream":%t,"input":[{"role":"user","content":[{"type":"input_text","text":"private-request"},{"type":"input_image","image_url":%q}]}]}`, outcome == "stream", media))
						s.responses(c)
					}
					wantStatus := http.StatusOK
					if outcome == "error" {
						wantStatus = http.StatusBadRequest
					}
					if response.Code != wantStatus {
						t.Fatalf("status=%d: %s", response.Code, response.Body.String())
					}
					if outcome != "error" && !strings.Contains(response.Body.String(), "private-response") {
						t.Fatal("response content must still reach the client")
					}
					logs := latestUsageRecords(t, s)
					if len(logs) != 1 || logs[0].StatusCode != wantStatus {
						t.Fatalf("logs=%+v", logs)
					}
					if outcome != "error" && logs[0].TotalTokens != 5 {
						t.Fatalf("tokens=%d", logs[0].TotalTokens)
					}
					var record usageRecord
					if err := json.Unmarshal([]byte(storedRecordJSON(t, s.store, logs[0].RequestID)), &record); err != nil {
						t.Fatal(err)
					}
					for name, body := range map[string]usageBody{"incoming": record.IncomingBody, "outgoing": record.OutgoingBody, "provider": record.ProviderResponse, "downstream": record.DownstreamResponse} {
						if policy.name != "enabled" && (body.Content != "" || body.Truncated) {
							t.Fatalf("%s retained body: %+v", name, body)
						}
					}
					if policy.name == "enabled" {
						if !strings.Contains(record.IncomingBody.Content, "private-request") || record.OutgoingBody.Content == "" || record.ProviderResponse.Content == "" || record.DownstreamResponse.Content == "" {
							t.Fatalf("enabled capture lost a body: %+v", record)
						}
						if entries, err := os.ReadDir(s.usageAssetsRoot()); err != nil || len(entries) == 0 {
							t.Fatalf("enabled capture must externalize media: %v", err)
						}
					} else if entries, err := os.ReadDir(s.usageAssetsRoot()); !os.IsNotExist(err) && (err != nil || len(entries) != 0) {
						t.Fatalf("disabled capture wrote assets: %v, %v", entries, err)
					}
				})
			}
		}
	}
}

func TestAgentCallerDefaultLogsMetadataOnly(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprintf("error=%t", fail), func(t *testing.T) {
			s := newAgentIntegrationServer(t)
			upstream := newCapturingUpstream(t, func(w http.ResponseWriter, _ string, _ int) {
				if fail {
					w.WriteHeader(http.StatusBadRequest)
					fmt.Fprint(w, `{"error":{"message":"bad request"}}`)
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, openAIChunk("c1", map[string]any{"role": "assistant", "content": "private-response"}, "stop", map[string]any{"prompt_tokens": 3, "completion_tokens": 2, "total_tokens": 5}), openAIDone())
			})
			seedCallerModel(t, s, upstream.URL, "openai")
			result, err := newAgentStreamCaller(s).Call(t.Context(), callerRequest(), agent.StreamCallbacks{})
			if (err != nil) != fail {
				t.Fatalf("Call error=%v", err)
			}
			if !fail && (result.Text != "private-response" || result.Usage == nil || result.Usage.Total.Count != 5) {
				t.Fatalf("result=%+v", result)
			}
			logs := latestUsageRecords(t, s)
			if len(logs) != 1 || (!fail && logs[0].TotalTokens != 5) {
				t.Fatalf("logs=%+v", logs)
			}
			var record usageRecord
			if err := json.Unmarshal([]byte(storedRecordJSON(t, s.store, logs[0].RequestID)), &record); err != nil {
				t.Fatal(err)
			}
			if record.IncomingBody.Content != "" || record.OutgoingBody.Content != "" || record.ProviderResponse.Content != "" || record.DownstreamResponse.Content != "" {
				t.Fatalf("agent retained bodies: %+v", record)
			}
		})
	}
}
