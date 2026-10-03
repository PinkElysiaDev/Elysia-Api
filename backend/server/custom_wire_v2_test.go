package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/elysia-api/backend/config"
	"github.com/elysia-api/backend/protocol"
	"github.com/gin-gonic/gin"
)

// standaloneWireDefinition uses no built-in shape. Optional settings remain
// author-controlled and the same definition serves preview and actual HTTP.
func standaloneWireDefinition(t *testing.T, transport protocol.Transport) protocol.Definition {
	t.Helper()
	definition := loadGatewayDefinition(t, "text-alpha")
	if transport != protocol.HTTPJSON {
		definition = gatewayStreamDefinition(t, "text-alpha", transport, "event")
	}
	definition.ID, definition.Family = "vendor-json", "vendor-json"
	operation := definition.Operations["generate"]
	operation.Path = "/v2/generate/{model}"
	operation.Auth = protocol.Credential{Location: "header", Name: "X-Token"}
	operation.Headers = map[string]string{"X-Client": "gateway"}
	definition.Operations["generate"] = operation
	for _, direction := range []protocol.Direction{protocol.DecodeRequest, protocol.EncodeRequest} {
		mapping := definition.Directions[direction]
		mapping.Transform.Fields["parameters"] = protocol.Expression{Op: "read", Path: "/parameters"}
		definition.Directions[direction] = mapping
	}
	decode := definition.Directions[protocol.DecodeResponse]
	decode.Transform.Fields["attributes"] = protocol.Expression{Op: "object", Fields: map[string]protocol.Expression{"finishReason": {Op: "read", Path: "/finish"}}}
	definition.Directions[protocol.DecodeResponse] = decode
	encode := definition.Directions[protocol.EncodeResponse]
	encode.Transform.Fields["finish"] = protocol.Expression{Op: "read", Path: "/attributes/finishReason"}
	definition.Directions[protocol.EncodeResponse] = encode
	for index, sample := range definition.Samples {
		if sample.Sequence {
			semantic := sample.Expected
			if sample.Direction == protocol.EncodeEvent {
				semantic = sample.Input
			}
			var events []protocol.Event
			if err := semantic.Decode(&events); err != nil {
				t.Fatal(err)
			}
			var wires []protocol.Value
			for i := range events {
				event := &events[i]
				if event.Type == protocol.ResponseFinished {
					event.Response = &protocol.Response{SchemaVersion: 1, Content: []protocol.Node{}, Attributes: protocol.Object{"finishReason": protocol.StringValue("stop")}}
				}
				if event.Usage != nil {
					// This text protocol has no creation-counter field. The separate
					// gateway stream fixture retains explicit zero-creation coverage.
					event.Usage.CacheCreation = nil
				}
				fields, _ := mustEncodedProtocolValue(t, event).ReadObject()
				delete(fields, "schemaVersion")
				delete(fields, "source")
				wires = append(wires, mustEncodedProtocolValue(t, protocol.Object{"event": mustEncodedProtocolValue(t, fields)}))
			}
			semantic = mustEncodedProtocolValue(t, events)
			wire := mustEncodedProtocolValue(t, wires)
			if sample.Direction == protocol.DecodeEvent {
				sample.Input, sample.Expected = wire, semantic
			} else {
				sample.Input, sample.Expected = semantic, wire
			}
			definition.Samples[index] = sample
			continue
		}
		if sample.Direction != protocol.DecodeResponse && sample.Direction != protocol.EncodeResponse {
			continue
		}
		wire, semantic := sample.Input, sample.Expected
		if sample.Direction == protocol.EncodeResponse {
			wire, semantic = semantic, wire
		}
		wireFields, _ := wire.ReadObject()
		wireFields["finish"] = protocol.StringValue("stop")
		semanticFields, _ := semantic.ReadObject()
		semanticFields["attributes"] = mustProtocolValue(t, `{"finishReason":"stop"}`)
		wire = mustEncodedProtocolValue(t, wireFields)
		semantic = mustEncodedProtocolValue(t, semanticFields)
		if sample.Direction == protocol.DecodeResponse {
			sample.Input, sample.Expected = wire, semantic
		} else {
			sample.Input, sample.Expected = semantic, wire
		}
		definition.Samples[index] = sample
	}
	if transport != protocol.HTTPJSON {
		mapping := definition.Directions[protocol.EncodeEvent]
		fields := mapping.Transform.Fields["event"]
		fields.Fields["response"] = protocol.Expression{Op: "read", Path: "/response"}
		mapping.Transform.Fields["event"] = fields
		definition.Directions[protocol.EncodeEvent] = mapping
	}
	return definition
}

func standaloneGroup(t *testing.T, definition protocol.Definition, url string) []config.ModelGroupConfig {
	t.Helper()
	groups := presetGroup(t, "custom:"+definition.ID, url)
	groups[0].Models[0].ToolsCapable, groups[0].Models[0].VisionCapable = false, false
	return groups
}

func writeFixtureFrames(t *testing.T, writer http.ResponseWriter, definition protocol.Definition) {
	t.Helper()
	operation := definition.Operations["generate"]
	writer.Header().Set("Content-Type", protocol.TransportContentType(operation.Transport))
	var frames []protocol.Value
	if err := definition.Samples[len(definition.Samples)-2].Input.Decode(&frames); err != nil {
		t.Error(err)
		return
	}
	for _, frame := range frames {
		if err := protocol.WriteFrame(writer, operation, frame); err != nil {
			t.Error(err)
			return
		}
		writer.(http.Flusher).Flush()
	}
}

func TestStandaloneProtocolFourPublicIngresses(t *testing.T) {
	for _, transport := range []protocol.Transport{protocol.HTTPJSON, protocol.SSE, protocol.NDJSON} {
		for _, source := range []struct{ id, path, body string }{
			{"chat", "/v1/chat/completions", `{"model":"grp","messages":[{"role":"user","content":"hello"}],"temperature":0.4}`},
			{"responses", "/v1/responses", `{"model":"grp","input":"hello"}`},
			{"anthropic", "/v1/messages", `{"model":"grp","max_tokens":64,"messages":[{"role":"user","content":"hello"}]}`},
			{"gemini", "/v1beta/models/grp:generateContent", `{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`},
		} {
			t.Run(source.id+"/"+string(transport), func(t *testing.T) {
				definition := standaloneWireDefinition(t, transport)
				var captured []byte
				provider := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
					captured, _ = io.ReadAll(request.Body)
					if request.URL.Path != "/v2/generate/preset-model" || request.Header.Get("X-Client") != "gateway" || request.Header.Get("X-Token") != "k" {
						t.Error("declared transport fields were lost", request.URL.Path)
					}
					if transport != protocol.HTTPJSON {
						writeFixtureFrames(t, writer, definition)
						return
					}
					writer.Header().Set("Content-Type", "application/json")
					fmt.Fprint(writer, `{"requestId":"r","finish":"stop","answer":[{"actor":"assistant","segments":[{"text":"hello"}]}]}`)
				}))
				defer provider.Close()
				server := newTestServer(t, standaloneGroup(t, definition, provider.URL), definition)
				path, body := source.path, source.body
				if transport != protocol.HTTPJSON {
					if source.id == "gemini" {
						path = strings.Replace(path, ":generateContent", ":streamGenerateContent", 1)
					} else {
						body = strings.Replace(body, `"model":"grp"`, `"model":"grp","stream":true`, 1)
					}
				}
				recorder := httptest.NewRecorder()
				ctx, _ := gin.CreateTestContext(recorder)
				ctx.Request = httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
				server.chatCompletions(ctx)
				if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "hello") || recorder.Result().Trailer.Get(gatewayStreamErrorTrailer) != "" {
					t.Fatal(recorder.Code, recorder.Body)
				}
				var upstream struct {
					Deployment string            `json:"deployment"`
					Turns      []json.RawMessage `json:"turns"`
					Parameters map[string]any    `json:"parameters"`
				}
				if err := json.Unmarshal(captured, &upstream); err != nil || upstream.Deployment != "preset-model" || len(upstream.Turns) != 1 || upstream.Parameters["stream"] != (transport != protocol.HTTPJSON) {
					t.Fatal("request semantics lost", string(captured), err)
				}
				if source.id == "chat" && upstream.Parameters["temperature"] != 0.4 {
					t.Fatal("temperature lost", string(captured))
				}
				logs := latestUsageRecords(t, server)
				if len(logs) != 1 || logs[0].StatusCode != http.StatusOK || !strings.Contains(storedRecordJSON(t, server.store, logs[0].RequestID), `"upstreamRevision":`) {
					t.Fatal("forwarding failure hidden behind HTTP 200", logs)
				}
			})
		}
	}
}

func TestStandaloneProtocolHTTPFailureRetriesOnceAndSettlesOnce(t *testing.T) {
	definition := standaloneWireDefinition(t, protocol.HTTPJSON)
	var failures atomic.Int32
	broken := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		failures.Add(1)
		writer.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer broken.Close()
	healthy := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		fmt.Fprint(writer, `{"requestId":"r","finish":"stop","answer":[{"actor":"assistant","segments":[{"text":"recovered"}]}]}`)
	}))
	defer healthy.Close()
	groups := standaloneGroup(t, definition, broken.URL)
	backup := groups[0].Models[0]
	backup.ID, backup.BaseURL = "backup", healthy.URL
	groups[0].Models = append(groups[0].Models, backup)
	groups[0].MaxRetries, groups[0].Strategy = 1, "sequential"
	server := newTestServer(t, groups, definition)
	ctx, recorder := chatRequestContext(`{"model":"grp","messages":[{"role":"user","content":"hello"}]}`)
	server.chatCompletions(ctx)
	if recorder.Code != http.StatusOK || failures.Load() != 1 || !strings.Contains(recorder.Body.String(), "recovered") || len(latestUsageRecords(t, server)) != 1 {
		t.Fatal("retry or settlement failed", recorder.Code, recorder.Body, failures.Load())
	}
}
