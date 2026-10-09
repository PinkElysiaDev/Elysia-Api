package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/elysia-api/backend/protocol/builtin"

	"github.com/elysia-api/backend/protocol"
)

func TestGatewayFourPublicEntrypointsUseActiveRuntime(t *testing.T) {
	fixtures := []struct{ module, id, family, path, request, response string }{
		{"openai-chat", "openai-chat-completions", string(builtin.FormatOpenAIChat), "/v1/chat/completions", `{"model":"m","messages":[{"role":"user","content":"hello"}]}`, `{"id":"r1","object":"chat.completion","created":1,"model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"world"},"finish_reason":"stop"}]}`},
		{"responses", "openai-responses", string(builtin.FormatResponses), "/v1/responses", `{"model":"m","input":[{"role":"user","content":"hello"}]}`, `{"id":"r1","object":"response","created_at":1,"status":"completed","model":"m","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"world"}]}]}`},
		{"anthropic", "anthropic-messages", string(builtin.FormatClaude), "/v1/messages", `{"model":"m","max_tokens":10,"messages":[{"role":"user","content":"hello"}]}`, `{"id":"r1","type":"message","role":"assistant","content":[{"type":"text","text":"world"}],"model":"m","stop_reason":"end_turn"}`},
		{"gemini", "google-generate-content", string(builtin.FormatGemini), "/v1beta/models/{model}:generateContent", `{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`, `{"candidates":[{"content":{"role":"model","parts":[{"text":"world"}]},"finishReason":"STOP"}],"modelVersion":"m","responseId":"r1"}`},
	}
	for _, fixture := range fixtures {
		t.Run(fixture.module, func(t *testing.T) {
			server, _ := newProtocolAdminTestServer(t)
			parse := func(raw string) protocol.Value {
				value, err := protocol.ParseValue([]byte(raw))
				if err != nil {
					t.Fatal(err)
				}
				return value
			}
			semanticRequest := parse(`{"schemaVersion":1,"model":"m","content":[{"kind":"message","role":"user","children":[{"kind":"text","payload":"hello"}]}]}`)
			if fixture.module == "anthropic" {
				semanticRequest = parse(`{"schemaVersion":1,"model":"m","content":[{"kind":"message","role":"user","children":[{"kind":"text","payload":"hello"}]}],"parameters":{"max_output_tokens":10}}`)
			}
			semanticResponse := parse(`{"schemaVersion":1,"id":"r1","model":"m","status":"completed","content":[{"kind":"message","role":"assistant","children":[{"kind":"text","payload":"world"}]}],"attributes":{"created_at":1}}`)
			definition := protocol.Definition{SchemaVersion: 2, ID: fixture.id, Name: fixture.id, Version: "1", Family: fixture.family, WireVersion: "legacy-v1", Capabilities: protocol.CapabilitySet{protocol.TextCapability: true}, Directions: map[protocol.Direction]protocol.Mapping{protocol.DecodeRequest: {Module: fixture.module}, protocol.EncodeResponse: {Module: fixture.module}}, Operations: map[string]protocol.Operation{"generate": {Kind: "generate", Method: "POST", Path: fixture.path, Transport: protocol.HTTPJSON, Auth: protocol.Credential{Location: "none"}}}, Samples: []protocol.Sample{
				{ID: "request", Direction: protocol.DecodeRequest, Capabilities: []protocol.Capability{protocol.TextCapability}, Input: parse(fixture.request), Expected: semanticRequest, Context: protocol.Object{"model": protocol.StringValue("m")}},
				{ID: "response", Direction: protocol.EncodeResponse, Capabilities: []protocol.Capability{protocol.TextCapability}, Input: semanticResponse, Expected: parse(fixture.response)},
			}}
			if fixture.module == "responses" {
				definition.Samples[1].Input = parse(`{"schemaVersion":1,"id":"r1","model":"m","status":"completed","content":[{"kind":"message","id":"msg1","status":"completed","role":"assistant","children":[{"kind":"text","payload":"world"}]}],"attributes":{"created_at":1}}`)
				definition.Samples[1].Expected = parse(`{"id":"r1","object":"response","created_at":1,"status":"completed","model":"m","output":[{"type":"message","id":"msg1","status":"completed","role":"assistant","content":[{"type":"output_text","text":"world","annotations":[]}]}]}`)
			}
			if fixture.module == "anthropic" {
				definition.Capabilities[protocol.UsageCapability] = true
				definition.Samples[1].Input = parse(`{"schemaVersion":1,"id":"r1","model":"m","status":"completed","content":[{"kind":"text","payload":"world"}],"attributes":{"finishReason":"stop"},"usage":{"input":{"count":1,"origin":"observed"},"output":{"count":1,"origin":"observed"}}}`)
				definition.Samples[1].Expected = parse(`{"id":"r1","model":"m","type":"message","role":"assistant","content":[{"type":"text","text":"world"}],"stop_reason":"end_turn","stop_sequence":null,"usage":{"input_tokens":1,"output_tokens":1}}`)
			}
			ingress := activateGatewayDefinition(t, server, definition)
			upstreamDefinition := loadGatewayDefinition(t, "text-beta")
			upstreamDefinition.Directions[protocol.DecodeRequest].Transform.Fields["parameters"] = protocol.Expression{Op: "object", Fields: map[string]protocol.Expression{"max_output_tokens": {Op: "read", From: "input", Path: "/maxTokens"}}}
			upstreamDefinition.Directions[protocol.EncodeRequest].Transform.Fields["maxTokens"] = protocol.Expression{Op: "read", From: "input", Path: "/parameters/max_output_tokens"}
			upstream := activateGatewayDefinition(t, server, upstreamDefinition)
			provider := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				var body map[string]any
				if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if body["engine"] != "upstream-model" {
					t.Errorf("model not mapped: %+v", body)
				}
				writer.Header().Set("Content-Type", "application/json")
				writer.Write([]byte(`{"resultId":"r1","reply":[{"speaker":"assistant","chunks":[{"value":"world"}]}]}`))
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
			server.engine.ServeHTTP(result, request)
			if result.Code != 200 || !bytes.Contains(result.Body.Bytes(), []byte("world")) {
				t.Fatalf("public ingress: %d %s", result.Code, result.Body)
			}
			logs := latestUsageRecords(t, server)
			if len(logs) != 1 {
				t.Fatalf("logs: %+v", logs)
			}
			var record usageRecord
			if err := json.Unmarshal([]byte(storedRecordJSON(t, server.store, logs[0].RequestID)), &record); err != nil {
				t.Fatal(err)
			}
			if record.IngressRevision != ingress.Hash() || record.RelayMode != "protocol_v2" || record.ConversionPolicyHash == "" {
				t.Fatalf("public endpoint bypassed v2: %+v", record)
			}
		})
	}
}
