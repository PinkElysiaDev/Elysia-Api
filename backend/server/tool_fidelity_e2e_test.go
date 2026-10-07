package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestResponsesToolFidelityHTTPPaths(t *testing.T) {
	preset := presetDefinition(t, "openai-responses")
	preset.ID = "independent-tools"
	const requestBody = `{"model":"grp","input":[{"type":"custom_tool_call","call_id":"c1","name":"patch","input":"edit file"},{"type":"custom_tool_call_output","call_id":"c1","output":"done"}],"tools":[{"type":"custom","name":"patch","format":{"type":"text"}},{"type":"mcp","server_label":"docs","server_url":"https://example.invalid/mcp"}]}`
	for _, platform := range []string{"responses", "custom:openai-responses", "custom:independent-tools"} {
		for _, isStream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%v", platform, isStream), func(t *testing.T) {
				captured := make(chan []byte, 1)
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					body, err := io.ReadAll(r.Body)
					if err != nil {
						t.Error(err)
						w.WriteHeader(500)
						return
					}
					captured <- body
					response := `{"id":"r1","model":"m","status":"completed","output":[{"type":"custom_tool_call","id":"item2","call_id":"c2","name":"patch","input":"another edit"}],"usage":{"input_tokens":100,"output_tokens":3,"input_tokens_details":{"cached_tokens":50}}}`
					if isStream {
						w.Header().Set("Content-Type", "text/event-stream")
						_, _ = io.WriteString(w, "data: {\"type\":\"response.output_item.added\",\"output_index\":0,\"item\":{\"type\":\"custom_tool_call\",\"id\":\"item2\",\"call_id\":\"c2\",\"name\":\"patch\",\"input\":\"\"}}\n\n")
						_, _ = io.WriteString(w, "data: {\"type\":\"response.custom_tool_call_input.delta\",\"output_index\":0,\"delta\":\"another edit\"}\n\n")
						_, _ = io.WriteString(w, "data: {\"type\":\"response.completed\",\"response\":"+response+"}\n\n")
						return
					}
					w.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(w, response)
				}))
				defer upstream.Close()
				s := newTestServer(t, presetGroup(t, platform, upstream.URL), preset)
				recorder := httptest.NewRecorder()
				ctx, _ := gin.CreateTestContext(recorder)
				body := strings.Replace(requestBody, `"model":"grp"`, fmt.Sprintf(`"model":"grp","stream":%v`, isStream), 1)
				ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
				ctx.Request.Header.Set("Content-Type", "application/json")
				s.responses(ctx)
				if recorder.Code != http.StatusOK {
					t.Fatalf("status %d: %s", recorder.Code, recorder.Body.String())
				}
				select {
				case body := <-captured:
					var wire map[string]any
					if err := json.Unmarshal(body, &wire); err != nil {
						t.Fatal(err)
					}
					tools := wire["tools"].([]any)
					if tools[0].(map[string]any)["name"] != "patch" || tools[1].(map[string]any)["server_label"] != "docs" {
						t.Fatalf("tools lost: %s", body)
					}
					if input := wire["input"].([]any); input[0].(map[string]any)["input"] != "edit file" || input[1].(map[string]any)["call_id"] != "c1" {
						t.Fatalf("history lost: %s", body)
					}
				default:
					t.Fatal("no upstream request")
				}
				for _, expected := range []string{`"input":"another edit"`, `"call_id":"c2"`, `"cached_tokens":50`} {
					if !strings.Contains(recorder.Body.String(), expected) {
						t.Fatalf("missing %s: %s", expected, recorder.Body.String())
					}
				}
			})
		}
	}
}
