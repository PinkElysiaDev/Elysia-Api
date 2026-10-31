package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/bytedance/sonic"
	"github.com/cloudwego/hertz/pkg/network"
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
	t.Parallel()
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
				if upstreamTransport == protocol.SSE {
					// Independent wire fixture: BOM, CR-only boundaries and control
					// frames must reach the real gateway's usage-tail drain path.
					fmt.Fprint(writer, "\xef\xbb\xbf: keepalive\r\rid: source-1\r\rretry: 200\r\r")
				}
				var frames []protocol.Value
				definition.Samples[len(definition.Samples)-2].Input.Decode(&frames)
				for _, frame := range frames {
					var err error
					if upstreamTransport == protocol.SSE {
						_, err = fmt.Fprintf(writer, "data: %s\r\r", frame.Bytes())
					} else {
						err = protocol.WriteFrame(writer, definition.Operations["generate"], frame)
					}
					if err != nil {
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
			if err := sonic.ConfigStd.Unmarshal(raw, &record); err != nil {
				t.Fatal(err)
			}
			if record.ProtocolUsage.CacheRead.Count != 40 || record.UsageDetail.CacheCreationInputTokens == nil || *record.UsageDetail.CacheCreationInputTokens != 0 || record.IngressRevision != ingress.Hash() || record.UpstreamRevision != upstream.Hash() {
				t.Fatalf("persisted usage: %s", raw)
			}
		})
	}
}

func TestGatewayBrokenStreamReportsErrorAndNeverRetries(t *testing.T) {
	t.Parallel()
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

type deliveryCommitWriter struct {
	network.ExtWriter
	writeError error
	flushError error
	afterFlush func()
	flushes    int
}

func (writer *deliveryCommitWriter) Write(body []byte) (int, error) {
	if writer.writeError != nil {
		return 0, writer.writeError
	}
	return writer.ExtWriter.Write(body)
}

func (writer *deliveryCommitWriter) WriteString(body string) (int, error) {
	return writer.Write([]byte(body))
}

func (writer *deliveryCommitWriter) Flush() error {
	writer.flushes++
	if writer.flushError != nil {
		return writer.flushError
	}
	if err := writer.ExtWriter.Flush(); err != nil {
		return err
	}
	if writer.afterFlush != nil {
		writer.afterFlush()
	}
	return nil
}

func TestGatewayOutputPositionCommitsAfterFlush(t *testing.T) {
	compiled := compileSourceStreamPackage(t, outputAwareStreamPackage(t))
	for _, ending := range []string{"complete", "write-failure", "flush-failure", "cancel-after-flush"} {
		t.Run(ending, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			c := newTestContext(httptest.NewRecorder())
			setTestRequest(c, httptest.NewRequest(http.MethodPost, "/generate", nil))
			writer := &deliveryCommitWriter{ExtWriter: startStream(c)}
			transportFailure := errors.New("synthetic output transport failure")
			switch ending {
			case "write-failure":
				writer.writeError = transportFailure
			case "flush-failure":
				writer.flushError = transportFailure
			case "cancel-after-flush":
				writer.afterFlush = cancel
			}
			defer flushTestResponse(c)
			validation, err := compiled.NewWireStreamValidation(protocol.Scope{})
			if err != nil {
				t.Fatal(err)
			}
			options := (protocol.EvaluationContext{State: protocol.NewEvaluationState()}).WithOutputStream(validation)
			defer options.State.Release()
			sourceOptions := protocol.EvaluationContext{State: protocol.NewEvaluationState()}
			defer sourceOptions.State.Release()
			wireOptions := protocol.EvaluationContext{State: protocol.NewEvaluationState()}
			defer wireOptions.State.Release()
			present := true
			conversion, err := protocol.CompileConversion(protocol.ConversionPolicy{SchemaVersion: protocol.ConversionPolicySchemaVersion, ID: "final-sequence", Rules: []protocol.ConversionRule{{
				ID: "sequence", Enabled: true, Phase: protocol.ConversionWire, Match: protocol.ConversionMatch{Path: "/initial", Present: &present},
				Action: "set", Path: "/sequence", Value: mustProtocolValue(t, "41"),
			}}})
			if err != nil {
				t.Fatal(err)
			}
			stream := &gatewayStream{ctx: ctx, context: c, writer: writer, record: &usageRecord{FirstByteMs: 1},
				plan:      &gatewayResponsePlan{ingress: compiled, operation: protocol.Operation{Transport: protocol.SSE}},
				candidate: gatewayCandidate{conversion: conversion}, limits: compiled.ResourceLimits(), options: options, finalValidation: validation, wireValidation: wireOptions}
			raw := mustProtocolValue(t, `{"type":"response.started","responseId":"delivered-id","sequence":0,"initial":true}`)
			decoded, err := compiled.DecodeFrame(ctx, raw, sourceOptions)
			if err != nil {
				t.Fatal(err)
			}
			frames, err := compiled.EncodeConvertedFrame(ctx, decoded, decoded.Events, options)
			if err != nil || len(frames) != 1 || frames[0] != raw {
				t.Fatal("native source was not replayed exactly once", frames, err)
			}
			err = stream.emit(frames[0])
			hasDelivered := ending == "complete" || ending == "cancel-after-flush"
			if hasDelivered && err != nil || !hasDelivered && !errors.Is(err, transportFailure) {
				t.Fatal("output transport result changed", err)
			}
			if ending == "cancel-after-flush" && ctx.Err() != context.Canceled {
				t.Fatal("flush cancellation fixture did not cancel")
			}
			probe := protocol.Event{SchemaVersion: protocol.ProtocolCoreVersion, Type: protocol.OperationFailed, ResponseID: protocol.StringValue("probe"), Sequence: mustProtocolValue(t, "99"), Error: protocol.StringValue("probe")}
			observed, err := compiled.EncodeEvent(t.Context(), probe, options)
			if err != nil {
				t.Fatal(err)
			}
			fields, _ := observed.ReadObject()
			var wasStarted bool
			if err := fields["was_started"].Decode(&wasStarted); err != nil || wasStarted != hasDelivered {
				t.Fatal("visible position advanced before a successful Flush", observed, err)
			}
			wantFlushes := 1
			if ending == "write-failure" {
				wantFlushes = 0
			}
			if writer.flushes != wantFlushes {
				t.Fatal("stream flush boundary changed", writer.flushes)
			}
			if ending != "complete" {
				return // An uncertain write/flush is not a reusable stream.
			}
			err = emitFailureEvent(ctx, c, stream.plan, options, func(wire protocol.Value) error {
				fields, _ := wire.ReadObject()
				if !fields["responseId"].IsString("delivered-id") || fields["sequence"].JSONString() != "42" {
					t.Fatal("local failure ignored the final edited wire position", wire)
				}
				return stream.emit(wire)
			}, errors.New("local failure"))
			if err != nil {
				t.Fatal("local failure did not pass final stream validation", err)
			}
			if err := validation.Finish(); err != nil || writer.flushes != 2 {
				t.Fatal("failure delivery did not terminate exactly once", err, writer.flushes)
			}
		})
	}
}
