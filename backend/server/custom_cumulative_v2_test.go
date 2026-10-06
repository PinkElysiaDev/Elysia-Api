package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/elysia-api/backend/protocol"
)

func cumulativeWireDefinition(t *testing.T) protocol.Definition {
	t.Helper()
	definition := standaloneWireDefinition(t, protocol.SSE)
	definition.ID, definition.Family = "vendor-cumulative", "vendor-cumulative"
	definition.Native.Preserve = false
	operation := definition.Operations["generate"]
	operation.Framing = &protocol.Framing{Done: []string{"END"}}
	definition.Operations["generate"] = operation
	raw := `{
 "initial":{"op":"if","when":{"op":"exists","source":{"op":"read","path":"/event"}},"then":{"op":"literal","value":[]},"otherwise":{"op":"literal","value":{"type":"item.started","itemId":"text","item":{"kind":"text","payload":""}}}},
 "unknownEvent":"reject",
 "rules":[
  {"when":{"op":"equal","items":[{"op":"read","from":"context","path":"/eventName"},{"op":"literal","value":"ignored"}]},"emit":{"op":"literal","value":[]}},
  {"when":{"op":"exists","source":{"op":"read","path":"/event"}},"emit":{"op":"read","path":"/event"}},
  {"when":{"op":"any","items":[{"op":"exists","source":{"op":"read","path":"/payload/text"}},{"op":"equal","items":[{"op":"read","path":"/finished"},{"op":"literal","value":true}]},{"op":"exists","source":{"op":"read","path":"/usage"}}]},
   "emit":{"op":"concat","items":[
    {"op":"if","when":{"op":"exists","source":{"op":"read","path":"/payload/text"}},"then":{"op":"array","items":[{"op":"object","fields":{"type":{"op":"literal","value":"item.snapshot"},"itemId":{"op":"literal","value":"text"},"item":{"op":"object","fields":{"kind":{"op":"literal","value":"text"},"payload":{"op":"read","path":"/payload/text"}}}}}]},"otherwise":{"op":"literal","value":[]}},
    {"op":"if","when":{"op":"equal","items":[{"op":"read","path":"/finished"},{"op":"literal","value":true}]},"then":{"op":"literal","value":[{"type":"item.finished","itemId":"text"},{"type":"response.finished","response":{"schemaVersion":1,"content":[],"attributes":{"finishReason":"stop"}}}]},"otherwise":{"op":"literal","value":[]}},
    {"op":"if","when":{"op":"exists","source":{"op":"read","path":"/usage"}},"then":{"op":"array","items":[{"op":"object","fields":{"type":{"op":"literal","value":"usage.updated"},"usage":{"op":"object","fields":{"input":{"op":"object","fields":{"count":{"op":"read","path":"/usage/input_tokens","required":true},"origin":{"op":"literal","value":"observed"}}},"output":{"op":"object","fields":{"count":{"op":"read","path":"/usage/output_tokens","required":true},"origin":{"op":"literal","value":"observed"}}}}}}}]},"otherwise":{"op":"literal","value":[]}}
   ]}}
 ]}`
	var mapping protocol.Mapping
	if err := json.Unmarshal([]byte(raw), &mapping); err != nil {
		t.Fatal(err)
	}
	definition.Directions[protocol.DecodeEvent] = mapping
	definition.Samples = definition.Samples[:len(definition.Samples)-2]
	wires := mustProtocolValue(t, `[{"payload":{"text":"hel"},"finished":false},{"payload":{"text":"hello"},"finished":false},{"payload":{"text":"hello!"},"finished":true,"usage":{"input_tokens":2,"output_tokens":3}}]`)
	semantics := mustProtocolValue(t, `[
 {"schemaVersion":1,"type":"item.started","itemId":"text","item":{"kind":"text","payload":""}},
 {"schemaVersion":1,"type":"item.snapshot","itemId":"text","item":{"kind":"text","payload":"hel"}},
 {"schemaVersion":1,"type":"item.snapshot","itemId":"text","item":{"kind":"text","payload":"hello"}},
 {"schemaVersion":1,"type":"item.snapshot","itemId":"text","item":{"kind":"text","payload":"hello!"}},
 {"schemaVersion":1,"type":"item.finished","itemId":"text"},
 {"schemaVersion":1,"type":"response.finished","response":{"schemaVersion":1,"content":[],"attributes":{"finishReason":"stop"}}},
 {"schemaVersion":1,"type":"usage.updated","usage":{"input":{"count":2,"origin":"observed"},"output":{"count":3,"origin":"observed"}}}
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
	definition.Samples = append(definition.Samples,
		protocol.Sample{ID: "cumulative.decode", Direction: protocol.DecodeEvent, Sequence: true, Input: wires, Expected: semantics, Context: protocol.Object{"eventName": protocol.StringValue("message")}},
		protocol.Sample{ID: "cumulative.encode", Direction: protocol.EncodeEvent, Sequence: true, Input: semantics, Expected: mustEncodedProtocolValue(t, encoded), Context: protocol.Object{"eventName": protocol.StringValue("message")}})
	return definition
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
	ctx, response := chatRequestContext(`{"model":"grp","stream":true,"messages":[{"role":"user","content":"hello"}]}`)
	server.chatCompletions(ctx)
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
		ctx, response := chatRequestContext(`{"model":"grp","stream":true,"messages":[{"role":"user","content":"hello"}]}`)
		server.chatCompletions(ctx)
		provider.Close()
		if response.Result().Trailer.Get(gatewayStreamErrorTrailer) == "" || strings.Contains(response.Body.String(), "[DONE]") {
			t.Fatal("invalid stream disguised as success", response.Body)
		}
	}
}
