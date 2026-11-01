package server

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/bytedance/sonic"
	"github.com/elysia-api/backend/protocol"
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

func collectPackageGatewayReply(t *testing.T, compiled *protocol.Compiled, reply *httptest.ResponseRecorder, isStream bool) *protocol.Response {
	t.Helper()
	if !isStream {
		response, err := compiled.DecodeResponse(t.Context(), reply.Body.Bytes(), protocol.EvaluationContext{})
		if err != nil {
			t.Fatal(err, reply.Body.String())
		}
		return response
	}
	options := protocol.EvaluationContext{State: protocol.NewEvaluationState()}
	defer options.State.Release()
	collector, err := protocol.NewResponseCollector(compiled.Target(protocol.EncodeEvent, protocol.Scope{}), compiled.ResourceLimits())
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

func checkPackageGatewayReply(t *testing.T, response *protocol.Response) {
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
	if text.String() != "hi" || !response.Status.IsString("completed") || response.Usage == nil || response.Usage.Input == nil || response.Usage.Input.Count != 20 || response.Usage.Output == nil || response.Usage.Output.Count != 2 {
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
			var calls atomic.Int32
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
				isStream := control.Stream || request.URL.Query().Get("alt") == "sse"
				if isStream {
					writer.Header().Set("Content-Type", "text/event-stream")
					_, _ = writer.Write(streamReply)
				} else {
					writer.Header().Set("Content-Type", "application/json")
					_, _ = writer.Write(jsonReply)
				}
			}))
			defer provider.Close()
			setupGatewayModelWithCapabilities(t, server, upstream, provider.URL, protocol.CapabilitySet{
				protocol.TextCapability: true, protocol.UsageCapability: true,
			})
			for _, ingressName := range gatewayPackageNames {
				for _, isStream := range []bool{false, true} {
					operationName := "generate"
					if isStream {
						operationName = "stream"
					}
					t.Run(ingressName+"/"+operationName, func(t *testing.T) {
						ingress := packages[ingressName]
						path := strings.ReplaceAll(ingress.Operations()[operationName].Path, "{model}", "group")
						request := httptest.NewRequest(http.MethodPost, "/gateway/"+ingress.Identity().DefinitionID+path, strings.NewReader(buildPackageGatewayRequest(ingressName, isStream)))
						request.Header.Set("Authorization", "Bearer gateway-test-token")
						request.Header.Set("Content-Type", "application/json")
						before := calls.Load()
						reply := httptest.NewRecorder()
						serveTestHTTP(server.engine, reply, request)
						if reply.Code != http.StatusOK || calls.Load() != before+1 {
							t.Fatal("generation failed or repeated", reply.Code, calls.Load()-before, reply.Body.String())
						}
						checkPackageGatewayReply(t, collectPackageGatewayReply(t, ingress, reply, isStream))
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
			response := collectPackageGatewayReply(t, compiled, reply, true)
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
