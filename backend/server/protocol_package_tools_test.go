package server

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/bytedance/sonic"
	"github.com/elysia-api/backend/protocol"
	"github.com/elysia-api/backend/storage"
)

func TestPackageGatewayToolResultDisposition(t *testing.T) {
	for _, name := range []string{"openai-chat", "openai-responses", "google-generate-content"} {
		t.Run(name, func(t *testing.T) {
			server, _ := newProtocolAdminTestServer(t)
			packages := seedGatewayPackages(t, server)
			ingress, upstream := packages["anthropic"], packages[name]
			jsonReply, streamReply := buildPackageGatewayReply(t, upstream, false), buildPackageGatewayReply(t, upstream, true)
			var calls atomic.Int32
			provider := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				calls.Add(1)
				body, err := io.ReadAll(request.Body)
				if err != nil {
					t.Error(err)
					return
				}
				var wire struct {
					Stream   bool
					Messages []struct {
						Role, Content string
						CallID        string `json:"tool_call_id"`
					}
					Input []struct {
						Type, Output string
						CallID       string `json:"call_id"`
					}
					Contents []struct {
						Parts []struct {
							FunctionResponse *struct {
								ID       string
								Response struct{ Error struct{ Result string } }
							}
						}
					}
				}
				if err := sonic.ConfigStd.Unmarshal(body, &wire); err != nil {
					t.Error(err)
					return
				}
				switch name {
				case "openai-chat":
					if len(wire.Messages) != 2 || wire.Messages[1].Role != "tool" || wire.Messages[1].Content != "[Tool error] result-body" || wire.Messages[1].CallID != "call" {
						t.Error("wrong Chat result", string(body))
					}
				case "openai-responses":
					if len(wire.Input) != 2 || wire.Input[1].Type != "function_call_output" || wire.Input[1].Output != "[Tool error] result-body" || wire.Input[1].CallID != "call" {
						t.Error("wrong Responses result", string(body))
					}
				case "google-generate-content":
					if len(wire.Contents) != 2 || len(wire.Contents[1].Parts) != 1 || wire.Contents[1].Parts[0].FunctionResponse == nil || wire.Contents[1].Parts[0].FunctionResponse.ID != "call" || wire.Contents[1].Parts[0].FunctionResponse.Response.Error.Result != "result-body" {
						t.Error("wrong Gemini error envelope", string(body))
					}
				}
				if wire.Stream || request.URL.Query().Get("alt") == "sse" {
					writer.Header().Set("Content-Type", "text/event-stream")
					_, _ = writer.Write(streamReply)
				} else {
					writer.Header().Set("Content-Type", "application/json")
					_, _ = writer.Write(jsonReply)
				}
			}))
			defer provider.Close()
			setupGatewayModelWithCapabilities(t, server, upstream, provider.URL, protocol.CapabilitySet{protocol.TextCapability: true, protocol.UsageCapability: true, protocol.FunctionToolsCapability: true})
			isToolsCapable := true
			if _, err := server.store.UpdateModel(t.Context(), "upstream-model", "gateway-source", storage.ModelPatch{ToolsCapable: &isToolsCapable}); err != nil {
				t.Fatal(err)
			}
			if err := server.store.UpsertGroup(t.Context(), storage.ModelGroup{ID: "gateway-group", Name: "group", Enabled: true, ToolsCapable: true, Models: []string{"gateway-source:upstream-model"}, Strategy: "sequential"}); err != nil {
				t.Fatal(err)
			}
			server.invalidateRouteCache()
			for _, isStream := range []bool{false, true} {
				operation := "generate"
				if isStream {
					operation = "stream"
				}
				t.Run(operation, func(t *testing.T) {
					body := fmt.Sprintf(`{"model":"group","max_tokens":32,"stream":%t,"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"call","name":"f","input":{}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"call","content":"result-body","is_error":true}]}]}`, isStream)
					request := httptest.NewRequest(http.MethodPost, "/gateway/"+ingress.Identity().DefinitionID+ingress.Operations()[operation].Path, strings.NewReader(body))
					request.Header.Set("Authorization", "Bearer gateway-test-token")
					request.Header.Set("Content-Type", "application/json")
					before := calls.Load()
					reply := httptest.NewRecorder()
					serveTestHTTP(server.engine, reply, request)
					if reply.Code != http.StatusOK || calls.Load() != before+1 {
						t.Fatal("tool history failed or repeated upstream", reply.Code, calls.Load()-before, reply.Body.String())
					}
					checkPackageGatewayReply(t, collectPackageGatewayReply(t, ingress, reply, isStream))
				})
			}
		})
	}
}
