package server

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elysia-api/backend/protocol"
)

func cumulativeWireDefinition(t *testing.T) protocol.ProtocolPackage {
	t.Helper()
	definition := standaloneWireDefinition(t, protocol.SSE)
	definition.Spec.ID, definition.Spec.Family = "vendor-cumulative", "vendor-cumulative"
	definition.Mapping.Native.Preserve = false
	operation := definition.Spec.Operations["generate"]
	operation.Framing = &protocol.Framing{Done: []string{"END"}}
	operation.Response = "cumulative-event"
	definition.Spec.Operations["generate"] = operation
	var mapping protocol.ProtocolDirectionMapping
	readCumulativeFixture(t, "mapping", &mapping)
	mapping.Effects = []protocol.ProtocolSemanticEffect{{ID: "task-boundary", Kind: "reject", Path: "/task",
		When:   &protocol.Expression{Op: "present", Source: &protocol.Expression{Op: "read", Path: "/task"}},
		Reason: "This text stream does not carry asynchronous task state."}}
	definition.Mapping.Directions[protocol.DecodeEvent] = mapping
	var contract protocol.WireContractSpec
	readCumulativeFixture(t, "spec", &contract)
	definition.Spec.Documents["cumulative-event"] = contract
	text := protocol.Expression{Op: "read", Path: "/payload/text"}
	finished := protocol.Expression{Op: "equal", Items: []protocol.Expression{{Op: "read", Path: "/finished"}, {Op: "literal", Value: mustProtocolValue(t, "true")}}}
	definition.Mapping.Expressions = map[string]protocol.Expression{"cumulative-text": text, "cumulative-finished": finished}
	definition.Spec.Expressions = map[string]protocol.Expression{"cumulative-text": text, "cumulative-finished": finished}
	definition.Mapping.Samples = definition.Mapping.Samples[:len(definition.Mapping.Samples)-2]
	wires := mustProtocolValue(t, `[{"payload":{"text":"hel"},"finished":false},{"payload":{"text":"hello"},"finished":false},{"payload":{"text":"hello!"},"finished":true,"usage":{"input_tokens":2,"output_tokens":3}}]`)
	semantics := mustProtocolValue(t, `[
 {"schemaVersion":2,"type":"item.started","itemId":"text","item":{"kind":"text","payload":""}},
 {"schemaVersion":2,"type":"item.snapshot","itemId":"text","item":{"kind":"text","payload":"hel"}},
 {"schemaVersion":2,"type":"item.snapshot","itemId":"text","item":{"kind":"text","payload":"hello"}},
 {"schemaVersion":2,"type":"item.snapshot","itemId":"text","item":{"kind":"text","payload":"hello!"}},
 {"schemaVersion":2,"type":"item.finished","itemId":"text"},
 {"schemaVersion":2,"type":"response.finished","response":{"schemaVersion":2,"content":[],"attributes":{"finishReason":"stop"}}},
 {"schemaVersion":2,"type":"usage.updated","usage":{"input":{"count":2,"origin":"observed"},"output":{"count":3,"origin":"observed"}}}
 ]`)
	var events []protocol.Value
	if err := semantics.Decode(&events); err != nil {
		t.Fatal(err)
	}
	var encoded []protocol.Value
	for _, event := range events {
		fields, _ := event.ReadObject()
		delete(fields, "schemaVersion")
		if response := fields["response"]; !response.IsZero() {
			body, _ := response.ReadObject()
			body["source"] = mustEncodedProtocolValue(t, protocol.Identity{})
			fields["response"] = mustEncodedProtocolValue(t, body)
		}
		encoded = append(encoded, mustEncodedProtocolValue(t, protocol.Object{"event": mustEncodedProtocolValue(t, fields)}))
	}
	definition.Mapping.Samples = append(definition.Mapping.Samples,
		protocol.Sample{ID: "cumulative.decode", Direction: protocol.DecodeEvent, Sequence: true, Scope: protocol.Scope{Model: "m"}, Input: wires, Expected: semantics, Context: protocol.Object{"eventName": protocol.StringValue("message")}},
		protocol.Sample{ID: "cumulative.encode", Direction: protocol.EncodeEvent, Sequence: true, Scope: protocol.Scope{Model: "m"}, Input: semantics, Expected: mustEncodedProtocolValue(t, encoded), Context: protocol.Object{"eventName": protocol.StringValue("message")}})
	return definition
}

func readCumulativeFixture(t *testing.T, part string, target any) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "protocols", "cumulative", part+".json"))
	if err != nil {
		t.Fatal(err)
	}
	value, err := protocol.ParseValue(raw)
	if err != nil {
		t.Fatal(err)
	}
	if err := value.Decode(target); err != nil {
		t.Fatal(err)
	}
}

func TestStandaloneCumulativeBooleanAndMultilineFrames(t *testing.T) {
	definition := cumulativeWireDefinition(t)
	provider := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(writer, "event: ignored\ndata: {\"heartbeat\":true}\n\n")
		fmt.Fprint(writer, "event: message\ndata: {\"payload\":{\"text\":\ndata: \"hel\"},\"finished\":false}\n\n")
		fmt.Fprint(writer, "data: {\"payload\":{\"text\":\"hello\"},\"finished\":false}\n\n")
		fmt.Fprint(writer, "data: {\"payload\":{\"text\":\"hello!\"},\"finished\":true,\"usage\":{\"input_tokens\":2,\"output_tokens\":3}}\n\ndata: END\n\n")
	}))
	defer provider.Close()
	server := newTestServer(t, standaloneGroup(t, definition, provider.URL), definition)
	ctx, response := chatRequestContext(`{"model":"grp","stream":true,"stream_options":{"include_usage":true},"messages":[{"role":"user","content":"hello"}]}`)
	server.chatCompletions(contextForTest(ctx), ctx)
	flushTestResponse(ctx)
	for _, want := range []string{`"content":"hel"`, `"content":"lo"`, `"content":"!"`, `"prompt_tokens":2`, `"completion_tokens":3`, `"finish_reason":"stop"`, "data: [DONE]"} {
		if !strings.Contains(response.Body.String(), want) {
			t.Fatal("cumulative/combined frame lost", want, response.Code, response.Body)
		}
	}
	if strings.Contains(response.Body.String(), `"content":"hello"`) || response.Result().Trailer.Get(gatewayStreamErrorTrailer) != "" {
		t.Fatal(response.Body)
	}
	logs := latestUsageRecords(t, server)
	if len(logs) != 1 || logs[0].StatusCode != http.StatusOK || logs[0].TotalTokens != 5 {
		t.Fatal(logs)
	}
}

func TestStandaloneCumulativeRewriteAndUnknownFrameFail(t *testing.T) {
	for _, last := range []string{`{"payload":{"text":"replacement"},"finished":false}`, `{"unexpected":"field"}`} {
		definition := cumulativeWireDefinition(t)
		provider := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			writer.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(writer, "data: {\"payload\":{\"text\":\"prefix\"},\"finished\":false}\n\n")
			fmt.Fprintf(writer, "data: %s\n\n", last)
		}))
		server := newTestServer(t, standaloneGroup(t, definition, provider.URL), definition)
		ctx, response := chatRequestContext(`{"model":"grp","stream":true,"stream_options":{"include_usage":true},"messages":[{"role":"user","content":"hello"}]}`)
		server.chatCompletions(contextForTest(ctx), ctx)
		flushTestResponse(ctx)
		provider.Close()
		if response.Result().Trailer.Get(gatewayStreamErrorTrailer) == "" || strings.Contains(response.Body.String(), "[DONE]") {
			t.Fatal("invalid stream disguised as success", response.Body)
		}
	}
}
