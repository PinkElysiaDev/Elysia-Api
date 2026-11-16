package server

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bytedance/sonic"
	"github.com/elysia-api/backend/protocol"
	"github.com/elysia-api/backend/protocol/builtin"
)

func TestGatewayFourPublicEntrypointsUseActiveRuntime(t *testing.T) {
	t.Parallel()
	fixtures := []struct{ module, id, family, path, request, response string }{
		{"openai-chat", "openai-chat-completions", string(builtin.FormatOpenAIChat), "/v1/chat/completions", `{"model":"m","messages":[{"role":"user","content":"hello"}]}`, `{"id":"r1","object":"chat.completion","created":1,"model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"world"},"finish_reason":"stop"}]}`},
		{"responses", "openai-responses", string(builtin.FormatResponses), "/v1/responses", `{"model":"m","input":[{"role":"user","content":"hello"}]}`, `{"id":"r1","object":"response","created_at":1,"status":"completed","model":"m","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"world"}]}]}`},
		{"anthropic", "anthropic-messages", string(builtin.FormatClaude), "/v1/messages", `{"model":"m","max_tokens":10,"messages":[{"role":"user","content":"hello"}]}`, `{"id":"r1","type":"message","role":"assistant","content":[{"type":"text","text":"world"}],"model":"m","stop_reason":"end_turn"}`},
		{"gemini", "google-generate-content", string(builtin.FormatGemini), "/v1beta/models/{model}:generateContent", `{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`, `{"candidates":[{"content":{"role":"model","parts":[{"text":"world"}]},"finishReason":"STOP"}],"modelVersion":"m","responseId":"r1"}`},
	}
	for _, fixture := range fixtures {
		t.Run(fixture.module, func(t *testing.T) {
			server, _ := newProtocolAdminTestServer(t)
			if err := server.initializeProtocolRuntime(t.Context()); err != nil {
				t.Fatal(err)
			}
			service, err := server.protocolService()
			if err != nil {
				t.Fatal(err)
			}
			ingress, exists := service.Pin(fixture.id)
			if !exists || ingress.CoreVersion() != protocol.ProtocolCoreVersion {
				t.Fatal("public ingress did not load the default core2 package", fixture.id)
			}
			upstreamDefinition := buildPublicUpstreamPackage(t)
			upstream := activateGatewayFixture(t, server, upstreamDefinition)
			provider := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				var body map[string]any
				if err := sonic.ConfigStd.NewDecoder(request.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if body["engine"] != "upstream-model" {
					t.Errorf("model not mapped: %+v", body)
				}
				writer.Header().Set("Content-Type", "application/json")
				writer.Write([]byte(`{"resultId":"r1","engine":"upstream-model","finish":"stop","reply":[{"speaker":"assistant","chunks":[{"value":"world"}]}]}`))
			}))
			defer provider.Close()
			setupGatewayModel(t, server, upstream, provider.URL)
			path := strings.ReplaceAll(fixture.path, "{model}", "group")
			handler := server.chatCompletions
			if fixture.module == "responses" {
				handler = server.responses
			}
			server.engine.POST(path, server.authMiddleware(), handler)
			request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(strings.Replace(fixture.request, `"model":"m"`, `"model":"group"`, 1)))
			request.Header.Set("Authorization", "Bearer gateway-test-token")
			result := httptest.NewRecorder()
			serveTestHTTP(server.engine, result, request)
			if result.Code != 200 || !bytes.Contains(result.Body.Bytes(), []byte("world")) {
				t.Fatalf("public ingress: %d %s", result.Code, result.Body)
			}
			logs := latestUsageRecords(t, server)
			if len(logs) != 1 {
				t.Fatalf("logs: %+v", logs)
			}
			var record usageRecord
			if err := sonic.ConfigStd.Unmarshal([]byte(storedRecordJSON(t, server.store, logs[0].RequestID)), &record); err != nil {
				t.Fatal(err)
			}
			if record.IngressRevision != ingress.Hash() || record.RelayMode != "protocol_v2" || record.ConversionPolicyHash == "" {
				t.Fatalf("public endpoint bypassed v2: %+v", record)
			}
		})
	}
}

func buildPublicUpstreamPackage(t *testing.T) protocol.ProtocolPackage {
	t.Helper()
	document := loadGatewayFixture(t, "text-beta")
	document.Mapping.Directions[protocol.DecodeRequest].Program.Fields["parameters"] = protocol.Expression{Op: "object", Fields: map[string]protocol.Expression{"max_output_tokens": {Op: "read", Path: "/maxTokens"}}}
	document.Mapping.Directions[protocol.EncodeRequest].Program.Fields["maxTokens"] = protocol.Expression{Op: "read", Path: "/parameters/max_output_tokens"}
	addFixtureResponseEnvelope(t, &document, "engine", "m")
	return document
}
