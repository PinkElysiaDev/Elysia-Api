package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/elysia-api/backend/protocol"
	"github.com/elysia-api/backend/storage"
)

func gatewayStreamDefinition(t *testing.T, name string, transport protocol.Transport, field string) protocol.Definition {
	t.Helper()
	definition := loadGatewayDefinition(t, name)
	definition.Capabilities[protocol.UsageCapability] = true
	for direction, mapping := range definition.Directions {
		mapping.Capabilities = protocol.CapabilitySet{protocol.TextCapability: true}
		definition.Directions[direction] = mapping
	}
	decode := protocol.Expression{Op: "read", From: "input", Path: "/" + field, Required: true}
	fields := map[string]protocol.Expression{}
	for _, key := range []string{"type", "itemId", "item", "delta", "usage", "error"} {
		fields[key] = protocol.Expression{Op: "read", From: "input", Path: "/" + key}
	}
	encode := protocol.Expression{Op: "object", Fields: map[string]protocol.Expression{field: {Op: "object", Fields: fields}}}
	definition.Directions[protocol.DecodeEvent] = protocol.Mapping{Transform: &decode}
	definition.Directions[protocol.EncodeEvent] = protocol.Mapping{Transform: &encode}
	operation := definition.Operations["generate"]
	operation.Transport = transport
	definition.Operations["generate"] = operation
	wires, semantics := []protocol.Value{}, []protocol.Value{}
	for _, raw := range []string{
		`{"type":"item.started","itemId":"text","item":{"kind":"text","payload":""}}`,
		`{"type":"item.delta","itemId":"text","delta":"hello"}`,
		`{"type":"item.finished","itemId":"text","item":{"kind":"text","payload":"hello"}}`,
		`{"type":"response.finished"}`,
		`{"type":"usage.updated","usage":{"input":{"count":100,"origin":"observed"},"cacheRead":{"count":40,"origin":"observed"},"cacheCreation":{"count":0,"origin":"observed"}}}`,
	} {
		value, err := protocol.ParseValue([]byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		wire, _ := protocol.EncodeValue(protocol.Object{field: value})
		wires = append(wires, wire)
		object, _ := value.ReadObject()
		object["schemaVersion"], _ = protocol.EncodeValue(protocol.SemanticSchemaVersion)
		semantic, _ := protocol.EncodeValue(object)
		semantics = append(semantics, semantic)
	}
	wireValue, _ := protocol.EncodeValue(wires)
	semanticValue, _ := protocol.EncodeValue(semantics)
	definition.Samples = append(definition.Samples,
		protocol.Sample{ID: "events.decode", Direction: protocol.DecodeEvent, Capabilities: []protocol.Capability{protocol.TextCapability, protocol.UsageCapability}, Input: wireValue, Expected: semanticValue, Sequence: true},
		protocol.Sample{ID: "events.encode", Direction: protocol.EncodeEvent, Capabilities: []protocol.Capability{protocol.TextCapability, protocol.UsageCapability}, Input: semanticValue, Expected: wireValue, Sequence: true})
	return definition
}

func TestGatewaySSEAndNDJSONBridgeDrainsUsageAndPersists(t *testing.T) {
	for _, ingressTransport := range []protocol.Transport{protocol.SSE, protocol.NDJSON} {
		t.Run(string(ingressTransport), func(t *testing.T) {
			server, _ := newProtocolAdminTestServer(t)
			upstreamTransport := protocol.SSE
			if ingressTransport == protocol.SSE {
				upstreamTransport = protocol.NDJSON
			}
			ingress := activateGatewayDefinition(t, server, gatewayStreamDefinition(t, "text-alpha", ingressTransport, "clientEvent"))
			definition := gatewayStreamDefinition(t, "text-beta", upstreamTransport, "providerEvent")
			upstream := activateGatewayDefinition(t, server, definition)
			provider := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				writer.Header().Set("Content-Type", protocol.TransportContentType(upstreamTransport))
				var frames []protocol.Value
				definition.Samples[len(definition.Samples)-2].Input.Decode(&frames)
				for _, frame := range frames {
					if err := protocol.WriteFrame(writer, definition.Operations["generate"], frame); err != nil {
						t.Error(err)
					}
					writer.(http.Flusher).Flush()
				}
			}))
			defer provider.Close()
			setupGatewayModel(t, server, upstream, provider.URL)
			result := gatewayTestRequest(server, "text-alpha", `{"deployment":"group","turns":[{"actor":"user","segments":[{"text":"hello"}]}]}`, "gateway-test-token")
			if result.Code != 200 {
				t.Fatalf("stream: %d %s", result.Code, result.Body)
			}
			var events []protocol.Event
			err := protocol.ReadFrames(t.Context(), result.Body, ingress.Definition().Operations["generate"], protocol.DefaultLimits().BufferBytes, func(frame protocol.Value, metadata protocol.Object) error {
				decoded, err := ingress.DecodeEvents(t.Context(), frame, protocol.EvaluationContext{Values: metadata})
				events = append(events, decoded...)
				return err
			})
			if err != nil || len(events) != 5 || events[4].Usage.CacheRead.Count != 40 || events[4].Usage.CacheCreation.Count != 0 {
				t.Fatalf("tail lost: %v %+v", err, events)
			}
			_, records, err := server.store.QueryUsageLogs(t.Context(), storage.UsageQuery{Limit: 10})
			if err != nil || len(records) != 1 {
				t.Fatalf("usage records: %v %+v", err, records)
			}
			raw, _, err := server.store.GetUsageRecordJSON(t.Context(), records[0].RequestID)
			if err != nil {
				t.Fatal(err)
			}
			var record usageRecord
			if err := json.Unmarshal(raw, &record); err != nil {
				t.Fatal(err)
			}
			if record.ProtocolUsage.CacheRead.Count != 40 || record.UsageDetail.CacheCreationInputTokens == nil || *record.UsageDetail.CacheCreationInputTokens != 0 || record.IngressRevision != ingress.Hash() || record.UpstreamRevision != upstream.Hash() {
				t.Fatalf("persisted usage: %s", raw)
			}
		})
	}
}

func TestGatewayBrokenStreamReportsErrorAndNeverRetries(t *testing.T) {
	server, _ := newProtocolAdminTestServer(t)
	definition := gatewayStreamDefinition(t, "text-alpha", protocol.SSE, "event")
	compiled := activateGatewayDefinition(t, server, definition)
	var calls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		writer.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(writer, "data: {\"event\":{\"type\":\"item.started\",\"itemId\":\"x\",\"item\":{\"kind\":\"text\",\"payload\":\"\"}}}\n\n")
		writer.(http.Flusher).Flush()
	}))
	defer provider.Close()
	setupGatewayModel(t, server, compiled, provider.URL)
	result := gatewayTestRequest(server, "text-alpha", `{"deployment":"group","turns":[{"actor":"user","segments":[{"text":"hello"}]}]}`, "gateway-test-token")
	if calls.Load() != 1 || !strings.Contains(result.Body.String(), "operation.failed") || strings.Contains(result.Body.String(), "response.finished") || result.Result().Trailer.Get(gatewayStreamErrorTrailer) == "" {
		t.Fatalf("broken stream: calls=%d trailer=%v body=%s", calls.Load(), result.Result().Trailer, result.Body)
	}
}
