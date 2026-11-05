package server

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/bytedance/sonic"
	"github.com/elysia-api/backend/protocol"
	"github.com/elysia-api/backend/storage"
)

func buildPackageGatewayRequest(name string, isStream bool) string {
	switch name {
	case "openai-chat":
		options := ""
		if isStream {
			options = `,"stream_options":{"include_usage":true}`
		}
		return fmt.Sprintf(`{"model":"group","messages":[{"role":"user","content":"hi"}],"max_tokens":32,"stream":%t%s}`, isStream, options)
	case "openai-responses":
		return fmt.Sprintf(`{"model":"group","input":"hi","max_output_tokens":32,"store":false,"stream":%t}`, isStream)
	case "anthropic":
		return fmt.Sprintf(`{"model":"group","messages":[{"role":"user","content":"hi"}],"max_tokens":32,"stream":%t}`, isStream)
	case "google-generate-content":
		return `{"contents":[{"role":"user","parts":[{"text":"hi"}]}],"generationConfig":{"maxOutputTokens":32}}`
	}
	panic("unknown matrix package")
}

func buildPackageGatewayReply(t *testing.T, compiled *protocol.Compiled, isStream bool) []byte {
	t.Helper()
	name := "text-decode-response"
	if isStream {
		name = "declared-stream-native-decode"
	}
	document, _ := compiled.Package()
	for _, sample := range document.Mapping.Samples {
		if sample.ID != name {
			continue
		}
		if !isStream {
			return sample.Input.Bytes()
		}
		var frames []protocol.Object
		if err := sample.Input.Decode(&frames); err != nil {
			t.Fatal(err)
		}
		var body bytes.Buffer
		for _, frame := range frames {
			// Native fixtures separately exercise unknown extensions. This matrix
			// converts documented generation fields across all four protocols.
			delete(frame, "extension")
			if err := protocol.WriteFrame(&body, compiled.Operations()["stream"], mustEncodedProtocolValue(t, frame)); err != nil {
				t.Fatal(err)
			}
		}
		return body.Bytes()
	}
	t.Fatal("missing generation sample", name)
	return nil
}

func collectPackageGatewayReply(t *testing.T, compiled *protocol.Compiled, reply *httptest.ResponseRecorder, isStream bool, scope protocol.Scope) *protocol.Response {
	t.Helper()
	if !isStream {
		response, err := compiled.DecodeResponse(t.Context(), reply.Body.Bytes(), protocol.EvaluationContext{Scope: scope})
		if err != nil {
			t.Fatal(err, reply.Body.String())
		}
		return response
	}
	options := protocol.EvaluationContext{Scope: scope, State: protocol.NewEvaluationState()}
	defer options.State.Release()
	collector, err := protocol.NewResponseCollector(compiled.Target(protocol.EncodeEvent, scope), compiled.ResourceLimits())
	if err != nil {
		t.Fatal(err)
	}
	err = protocol.ReadFrames(t.Context(), reply.Body, compiled.Operations()["stream"], compiled.ResourceLimits().BufferBytes, func(value protocol.Value, metadata protocol.Object) error {
		options.Values = metadata
		frame, err := compiled.DecodeFrame(t.Context(), value, options)
		if err != nil {
			return err
		}
		for _, event := range frame.Events {
			if _, _, err := collector.Consume(event); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := compiled.FinishInputEvents(t.Context(), options); err != nil {
		t.Fatal(err)
	}
	response, err := collector.Finish()
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func checkPackageGatewayReply(t *testing.T, response *protocol.Response, expectedText string, inputTokens, outputTokens int64) {
	t.Helper()
	var text strings.Builder
	var visit func([]protocol.Node)
	visit = func(nodes []protocol.Node) {
		for _, node := range nodes {
			if node.Kind == protocol.TextNode {
				var content string
				if err := node.Payload.Decode(&content); err != nil {
					t.Fatal(err)
				}
				text.WriteString(content)
			}
			visit(node.Children)
		}
	}
	visit(response.Content)
	if text.String() != expectedText || !response.Status.IsString("completed") || response.Usage == nil || response.Usage.Input == nil || response.Usage.Input.Count != inputTokens || response.Usage.Output == nil || response.Usage.Output.Count != outputTokens {
		t.Fatal("full consumption lost content, terminal status or usage", text.String(), response.Status, response.Usage)
	}
}

func TestDefaultPackagesGatewayMatrix(t *testing.T) {
	for _, upstreamName := range gatewayPackageNames {
		t.Run(upstreamName, func(t *testing.T) {
			server, _ := newProtocolAdminTestServer(t)
			packages := seedGatewayPackages(t, server)
			upstream := packages[upstreamName]
			jsonReply, streamReply := buildPackageGatewayReply(t, upstream, false), buildPackageGatewayReply(t, upstream, true)
			var multiRootReply []byte
			var layoutReply bytes.Buffer
			fixtureName := map[string]string{"openai-responses": "responses-stream-layout.json", "anthropic": "anthropic-stream-layout.json", "google-generate-content": "gemini-stream-layout.json"}[upstreamName]
			if fixtureName != "" {
				fixture, err := os.ReadFile("../protocol/builtin/testdata/" + fixtureName)
				if err != nil {
					t.Fatal(err)
				}
				var frames []protocol.Value
				if err := sonic.ConfigStd.Unmarshal(fixture, &frames); err != nil {
					t.Fatal(err)
				}
				for _, frame := range frames {
					if err := protocol.WriteFrame(&layoutReply, upstream.Operations()["stream"], frame); err != nil {
						t.Fatal(err)
					}
				}
			}
			if upstreamName == "openai-responses" {
				value, err := protocol.ParseValue(jsonReply)
				if err != nil {
					t.Fatal(err)
				}
				fields, err := value.ReadObject()
				if err != nil {
					t.Fatal(err)
				}
				fields["output_text"] = protocol.StringValue("hi")
				jsonReply = mustEncodedProtocolValue(t, fields).Bytes()
				var items []protocol.Object
				for index, text := range []string{"h", "i"} {
					items = append(items, protocol.Object{
						"type": protocol.StringValue("message"), "role": protocol.StringValue("assistant"),
						"id": protocol.StringValue(fmt.Sprintf("r_item_%d", index)), "status": protocol.StringValue("completed"),
						"content": mustEncodedProtocolValue(t, []protocol.Object{{
							"type": protocol.StringValue("output_text"), "text": protocol.StringValue(text),
							"annotations": mustEncodedProtocolValue(t, []protocol.Value{}),
						}}),
					})
				}
				fields["output"] = mustEncodedProtocolValue(t, items)
				multiRootReply = mustEncodedProtocolValue(t, fields).Bytes()
			}
			var calls atomic.Int32
			var expectsInstructions atomic.Bool
			var expectsMultipleRoots atomic.Bool
			var expectsStreamLayout atomic.Bool
			provider := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				calls.Add(1)
				body, err := io.ReadAll(request.Body)
				if err != nil {
					t.Error(err)
					return
				}
				var control struct{ Stream bool }
				if err := sonic.ConfigStd.Unmarshal(body, &control); err != nil {
					t.Error(err)
					return
				}
				if upstreamName == "anthropic" || upstreamName == "google-generate-content" {
					value, err := protocol.ParseValue(body)
					if err != nil {
						t.Error(err)
						return
					}
					fields, err := value.ReadObject()
					if err != nil {
						t.Error(err)
						return
					}
					instruction := fields["system"]
					if upstreamName == "google-generate-content" {
						instruction = fields["systemInstruction"]
					}
					if expectsInstructions.Load() && (instruction.IsZero() || !strings.Contains(instruction.JSONString(), "matrix policy") || !strings.Contains(instruction.JSONString(), "matrix detail") || strings.Index(instruction.JSONString(), "matrix policy") > strings.Index(instruction.JSONString(), "matrix detail")) {
						t.Error("declared instruction layout changed upstream block order", string(body))
					}
				}
				if upstreamName != "anthropic" {
					value, err := protocol.ParseValue(body)
					if err != nil {
						t.Error(err)
						return
					}
					fields, err := value.ReadObject()
					if err != nil || !fields["cache_control"].IsZero() {
						t.Error("declared cache omission did not reach the upstream", string(body), err)
					}
				}
				isStream := control.Stream || request.URL.Query().Get("alt") == "sse"
				if isStream {
					writer.Header().Set("Content-Type", "text/event-stream")
					response := streamReply
					if expectsStreamLayout.Load() {
						response = layoutReply.Bytes()
					}
					_, _ = writer.Write(response)
				} else {
					writer.Header().Set("Content-Type", "application/json")
					response := jsonReply
					if expectsMultipleRoots.Load() {
						response = multiRootReply
					}
					_, _ = writer.Write(response)
				}
			}))
			defer provider.Close()
			capabilities := protocol.CapabilitySet{
				protocol.TextCapability: true, protocol.UsageCapability: true, protocol.FunctionToolsCapability: true,
			}
			if upstreamName == "anthropic" || upstreamName == "google-generate-content" {
				capabilities[protocol.ReasoningCapability], capabilities[protocol.SignaturesCapability] = true, true
			}
			setupGatewayModelWithCapabilities(t, server, upstream, provider.URL, capabilities)
			if fixtureName != "" {
				yes := true
				if _, err := server.store.UpdateModel(t.Context(), "upstream-model", "gateway-source", storage.ModelPatch{ToolsCapable: &yes}); err != nil {
					t.Fatal(err)
				}
				if err := server.store.UpsertGroup(t.Context(), storage.ModelGroup{ID: "gateway-group", Name: "group", Enabled: true, ToolsCapable: true, Models: []string{"gateway-source:upstream-model"}, Strategy: "sequential"}); err != nil {
					t.Fatal(err)
				}
				server.invalidateRouteCache()
			}
			for _, ingressName := range gatewayPackageNames {
				for _, isStream := range []bool{false, true} {
					operationName := "generate"
					if isStream {
						operationName = "stream"
					}
					t.Run(ingressName+"/"+operationName, func(t *testing.T) {
						ingress := packages[ingressName]
						path := strings.ReplaceAll(ingress.Operations()[operationName].Path, "{model}", "group")
						body := buildPackageGatewayRequest(ingressName, isStream)
						expectsMultipleRoots.Store(upstreamName == "openai-responses" && (ingressName == "openai-chat" || ingressName == "anthropic" || ingressName == "google-generate-content"))
						hasTextRanges := upstreamName == "openai-responses" && (ingressName == "openai-chat" || ingressName == "openai-responses") && isStream
						hasThinkingLayout := upstreamName == "anthropic" && ingressName == "anthropic" && isStream
						hasPartsLayout := upstreamName == "google-generate-content" && ingressName == "google-generate-content" && isStream
						expectsStreamLayout.Store(hasTextRanges || hasThinkingLayout || hasPartsLayout)
						if hasTextRanges {
							tools := `,"tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object","properties":{"n":{"type":"integer"}}}}}]}`
							if ingressName == "openai-responses" {
								tools = `,"tools":[{"type":"function","name":"lookup","parameters":{"type":"object","properties":{"n":{"type":"integer"}}}}]}`
							}
							body = strings.TrimSuffix(body, "}") + tools
						}
						if hasThinkingLayout {
							body = strings.TrimSuffix(body, "}") + `,"tools":[{"name":"lookup","input_schema":{"type":"object","properties":{"n":{"type":"integer"}}}}]}`
						}
						if hasPartsLayout {
							body = strings.TrimSuffix(body, "}") + `,"tools":[{"functionDeclarations":[{"name":"lookup","parameters":{"type":"object","properties":{"n":{"type":"integer"}}}}]}]}`
						}
						hasInstructions := ingressName == "openai-chat" && (upstreamName == "anthropic" || upstreamName == "google-generate-content")
						expectsInstructions.Store(hasInstructions)
						if hasInstructions {
							body = strings.Replace(body, `"messages":[{"role":"user","content":"hi"}]`, `"messages":[{"role":"user","content":"hi"},{"role":"system","content":"matrix policy"},{"role":"developer","content":"matrix detail"}]`, 1)
						}
						if ingressName == "anthropic" && upstreamName != "anthropic" {
							body = strings.TrimSuffix(body, "}") + `,"cache_control":{"type":"ephemeral","ttl":"1h"}}`
						}
						request := httptest.NewRequest(http.MethodPost, "/gateway/"+ingress.Identity().DefinitionID+path, strings.NewReader(body))
						request.Header.Set("Authorization", "Bearer gateway-test-token")
						request.Header.Set("Content-Type", "application/json")
						before := calls.Load()
						reply := httptest.NewRecorder()
						serveTestHTTP(server.engine, reply, request)
						if reply.Code != http.StatusOK || calls.Load() != before+1 {
							t.Fatal("generation failed or repeated", reply.Code, calls.Load()-before, reply.Body.String())
						}
						expectedText := "hi"
						var inputTokens, outputTokens int64 = 20, 2
						if hasTextRanges {
							expectedText = "😀hi!"
						}
						if hasPartsLayout {
							expectedText, inputTokens, outputTokens = "hello", 3, 3
						}
						clientScope := protocol.Scope{}
						if hasThinkingLayout || hasPartsLayout {
							clientScope = protocol.Scope{Provider: "gateway-fixture", Account: "client-fixture", Model: "group"}
						}
						if isStream && reply.Result().Trailer.Get(gatewayStreamErrorTrailer) != "" {
							t.Fatal("stream failed before collection", reply.Body.String(), latestUsageRecords(t, server))
						}
						collected := collectPackageGatewayReply(t, ingress, reply, isStream, clientScope)
						checkPackageGatewayReply(t, collected, expectedText, inputTokens, outputTokens)
						if hasPartsLayout {
							if len(collected.Content) != 1 || len(collected.Content[0].Children) != 3 {
								t.Fatal("gateway lost ordered parts", collected.Content)
							}
							items := collected.Content[0].Children
							if !items[0].Payload.IsString("think") || !items[1].Payload.IsString("hello") || len(items[0].Resources) != 1 || !items[0].Resources[0].ID.IsString("sig-thought") || items[2].Kind != protocol.ToolCallNode || !items[2].CallID.IsString("call") || !items[2].Name.IsString("lookup") || items[2].Input == nil || items[2].Input.Value.JSONString() != `{"n":9007199254740993}` || len(items[2].Resources) != 1 || !items[2].Resources[0].ID.IsString("sig-tool") || collected.Usage.Total == nil || collected.Usage.Total.Count != 6 || collected.Usage.Details["output.reasoning_tokens"].Count != 1 {
								t.Fatal("gateway changed part resources, tool association or reasoning accounting", items, collected.Usage)
							}
						}
						if hasThinkingLayout {
							if len(collected.Content) != 1 || len(collected.Content[0].Children) != 5 {
								t.Fatal("gateway lost ordered thinking/tool blocks", collected.Content)
							}
							items := collected.Content[0].Children
							if !items[0].Payload.IsString("plan-a") || !items[3].Payload.IsString("plan-b") || len(items[0].Resources) != 1 || len(items[3].Resources) != 1 || !items[0].Resources[0].ID.IsString("sig-a") || !items[3].Resources[0].ID.IsString("sig-b") || items[2].Kind != protocol.ToolCallNode || !items[2].CallID.IsString("call") || !items[2].Name.IsString("lookup") || items[2].Input == nil || items[2].Input.Value.JSONString() != `{"n":9007199254740993}` {
								t.Fatal("gateway changed thinking resources or tool association", items)
							}
						}
						if hasTextRanges {
							tools, citations, annotationOwners := 0, 0, 0
							var visit func([]protocol.Node)
							visit = func(nodes []protocol.Node) {
								for _, node := range nodes {
									if node.Kind == protocol.ToolCallNode {
										tools++
										if !node.CallID.IsString("call") || !node.Name.IsString("lookup") || node.Input == nil || node.Input.Value.JSONString() != `{"n":9007199254740993}` {
											t.Fatal("gateway changed tool identity or arguments", node)
										}
									}
									for _, metadata := range node.Metadata {
										if metadata.Name == "annotations" {
											annotationOwners++
											var entries []protocol.Value
											if err := metadata.Value.Decode(&entries); err != nil {
												t.Fatal(err)
											}
											citations += len(entries)
										}
									}
									visit(node.Children)
								}
							}
							visit(collected.Content)
							expectedOwners := 1
							if ingressName == "openai-responses" {
								expectedOwners = 3
							}
							if tools != 1 || citations != 1 || annotationOwners != expectedOwners {
								t.Fatal("gateway lost tool, citation or explicit empty annotations", tools, citations, annotationOwners)
							}
						}
					})
				}
			}
		})
	}
}

func TestPackageChatUsageCollectionRespectsClientPreference(t *testing.T) {
	for _, collectUsage := range []bool{true, false} {
		t.Run(fmt.Sprintf("collect=%t", collectUsage), func(t *testing.T) {
			server, _ := newProtocolAdminTestServer(t)
			compiled := seedGatewayPackages(t, server)["openai-chat"]
			stream := buildPackageGatewayReply(t, compiled, true)
			var calls atomic.Int32
			provider := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				calls.Add(1)
				body, err := io.ReadAll(request.Body)
				if err != nil {
					t.Error(err)
					return
				}
				var control struct {
					Stream        bool `json:"stream"`
					StreamOptions struct {
						IncludeUsage *bool `json:"include_usage"`
					} `json:"stream_options"`
				}
				if err := sonic.ConfigStd.Unmarshal(body, &control); err != nil {
					t.Error(err)
					return
				}
				if !control.Stream || control.StreamOptions.IncludeUsage == nil || *control.StreamOptions.IncludeUsage != collectUsage {
					t.Error("upstream usage instruction ignored the selected policy", string(body))
				}
				// A provider may report usage even when it was not requested.
				writer.Header().Set("Content-Type", "text/event-stream")
				_, _ = writer.Write(stream)
			}))
			defer provider.Close()
			setupGatewayModelWithCapabilities(t, server, compiled, provider.URL, protocol.CapabilitySet{
				protocol.TextCapability: true, protocol.UsageCapability: true,
			})
			if !collectUsage {
				bindings, err := server.store.ListProtocolBindings(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				binding := bindings[0]
				binding.Conversion = &protocol.ConversionSelection{Overrides: &protocol.ConversionPolicy{
					SchemaVersion: protocol.ConversionPolicySchemaVersion, ID: "usage-preference",
					Usage: &protocol.ConversionUsageSettings{CollectUpstreamUsage: false},
				}}
				binding.Combinations = verifyFixtureBinding(t, server, binding)
				if err := server.store.SaveProtocolBinding(t.Context(), binding); err != nil {
					t.Fatal(err)
				}
				server.invalidateRouteCache()
			}
			request := httptest.NewRequest(http.MethodPost, "/gateway/"+compiled.Identity().DefinitionID+compiled.Operations()["stream"].Path,
				strings.NewReader(`{"model":"group","messages":[{"role":"user","content":"hi"}],"stream":true,"stream_options":{"include_usage":false}}`))
			request.Header.Set("Authorization", "Bearer gateway-test-token")
			request.Header.Set("Content-Type", "application/json")
			reply := httptest.NewRecorder()
			serveTestHTTP(server.engine, reply, request)
			if reply.Code != http.StatusOK || calls.Load() != 1 {
				t.Fatal("generation failed or repeated", reply.Code, calls.Load(), reply.Body.String())
			}
			response := collectPackageGatewayReply(t, compiled, reply, true, protocol.Scope{})
			if !response.Status.IsString("completed") || response.Usage != nil || len(response.Content) != 1 || len(response.Content[0].Children) != 1 || !response.Content[0].Children[0].Payload.IsString("hi") {
				t.Fatal("client preference changed content, termination or exposed usage", response)
			}
			records := latestUsageRecords(t, server)
			if len(records) != 1 || records[0].Error != "" {
				t.Fatal("request did not settle once", records)
			}
			raw, exists, err := server.store.GetUsageRecordJSON(t.Context(), records[0].RequestID)
			if err != nil || !exists {
				t.Fatal("missing final record", err)
			}
			var record usageRecord
			if err := sonic.ConfigStd.Unmarshal(raw, &record); err != nil {
				t.Fatal(err)
			}
			usage := record.ProtocolUsage
			if usage == nil || usage.Input == nil || usage.Input.Count != 20 || usage.Output == nil || usage.Output.Count != 2 || record.Usage.Estimated {
				t.Fatal("client presentation suppressed observed accounting", usage, record.Usage)
			}
		})
	}
}
