package builtin

import (
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestResponsesPackageStreamRebuildAndNativeDelivery(t *testing.T) {
	compiled := compileBuiltinPackage(t, "openai-responses")
	scope := p.Scope{Provider: "fixture", Account: "fixture", Model: "m"}
	reasoning := responsesCompletedStream(structuredReasoningFinal, structuredReasoningSequence()...)
	var toolValues []p.Value
	if err := responsesToolStreamSample(t).Input.Decode(&toolValues); err != nil {
		t.Fatal(err)
	}
	tool := make([]string, len(toolValues))
	for index, value := range toolValues {
		tool[index] = value.JSONString()
	}
	textValues := phaseStreamFrames(t, "done", []string{`"commentary"`, `"final_answer"`, `null`})
	text := make([]string, len(textValues))
	for index, value := range textValues {
		text[index] = value.JSONString()
	}
	annotation := citationResponses[1 : len(citationResponses)-1]
	part := `{"type":"output_text","text":"OK","annotations":` + citationResponses + `,"logprobs":` + auditLogprobs + `}`
	message := `{"type":"message","id":"msg","role":"assistant","status":"completed","content":[` + part + `]}`
	arrays := responsesCompletedStream(message,
		`{"type":"response.output_item.added","output_index":0,"item":{"type":"message","id":"msg","role":"assistant","status":"in_progress","content":[]}}`,
		`{"type":"response.content_part.added","output_index":0,"content_index":0,"item_id":"msg","part":{"type":"output_text","text":"","annotations":[],"logprobs":[]}}`,
		`{"type":"response.output_text.delta","output_index":0,"content_index":0,"item_id":"msg","delta":"OK","logprobs":`+auditLogprobs+`}`,
		`{"type":"response.output_text.annotation.added","output_index":0,"content_index":0,"item_id":"msg","annotation_index":0,"annotation":`+annotation+`}`,
		`{"type":"response.output_text.done","output_index":0,"content_index":0,"item_id":"msg","text":"OK","logprobs":`+auditLogprobs+`}`,
		`{"type":"response.content_part.done","output_index":0,"content_index":0,"item_id":"msg","part":`+part+`}`,
		`{"type":"response.output_item.done","output_index":0,"item":`+message+`}`)
	custom := `{"type":"custom_tool_call","id":"custom","call_id":"c","name":"shell","status":"completed","input":"echo hello"}`
	textTool := responsesCompletedStream(custom,
		`{"type":"response.output_item.added","output_index":0,"item":{"type":"custom_tool_call","id":"custom","call_id":"c","name":"shell","status":"in_progress","input":""}}`,
		`{"type":"response.custom_tool_call_input.delta","output_index":0,"item_id":"custom","delta":"echo "}`,
		`{"type":"response.custom_tool_call_input.delta","output_index":0,"item_id":"custom","delta":"hello"}`,
		`{"type":"response.custom_tool_call_input.done","output_index":0,"item_id":"custom","input":"echo hello"}`,
		`{"type":"response.output_item.done","output_index":0,"item":`+custom+`}`)
	for name, source := range map[string][]string{"reasoning collections": reasoning, "message phases": text, "function arguments": tool, "incremental metadata": arrays, "text tool": textTool, "hosted tool usage": hostedUsageStreamFrames()} {
		t.Run(name, func(t *testing.T) {
			// Native delivery uses complete client-valid frames. Separate source
			// fixtures continue to exercise missing-sequence compatibility.
			for index, raw := range source {
				fields, err := testValue(t, raw).ReadObject()
				if err != nil {
					t.Fatal(err)
				}
				fields["sequence_number"], err = p.EncodeValue(index)
				if err != nil {
					t.Fatal(err)
				}
				frame, err := p.EncodeValue(fields)
				if err != nil {
					t.Fatal(err)
				}
				source[index] = frame.JSONString()
			}
			frames, response := decodePackageStream(t, compiled, source, scope)
			rebuilt := encodePackageStream(t, compiled, frames, scope, false)
			_, output := decodePackageStream(t, compiled, rebuilt, scope)
			if name == "hosted tool usage" {
				terminal, err := testValue(t, rebuilt[len(rebuilt)-1]).ReadObject()
				if err != nil {
					t.Fatal(err)
				}
				body, err := terminal["response"].ReadObject()
				if err != nil {
					t.Fatal(err)
				}
				if !equivalentJSON(body["tool_usage"], testValue(t, hostedUsageFixture)) {
					t.Fatal("terminal lost the hosted-tool ledger", body["tool_usage"].JSONString())
				}
				if output.Usage == nil || output.Usage.Total == nil || output.Usage.Total.Count != 4 || output.Usage.Details["tools.web_search_calls"].Count != 2 {
					t.Fatal("tool snapshots changed model totals or counted requests twice", output.Usage)
				}
			}
			if len(response.Content) != len(output.Content) {
				t.Fatalf("output item count changed: %d -> %d", len(response.Content), len(output.Content))
			}
			for index := range response.Content {
				assertResponsesStreamNode(t, response.Content[index], output.Content[index])
			}
			beforeUsage, _ := p.EncodeValue(response.Usage)
			afterUsage, _ := p.EncodeValue(output.Usage)
			if beforeUsage != afterUsage {
				t.Fatal("usage changed", beforeUsage.JSONString(), afterUsage.JSONString())
			}
			native := encodePackageStream(t, compiled, frames, scope, true)
			if len(native) != len(source) {
				t.Fatal("native frame duplicated or dropped", len(native), len(source))
			}
			for index := range source {
				if native[index] != source[index] {
					t.Fatal("native frame bytes changed", index)
				}
			}
		})
	}
}

func assertResponsesStreamNode(t *testing.T, before, after p.Node) {
	t.Helper()
	if before.Kind != after.Kind || before.ID != after.ID || before.Role != after.Role || before.Name != after.Name || before.CallID != after.CallID || before.Payload != after.Payload || before.ReasoningForm != after.ReasoningForm {
		t.Fatalf("stream node changed: before=%+v after=%+v", before, after)
	}
	beforeInput, _ := p.EncodeValue(before.Input)
	afterInput, _ := p.EncodeValue(after.Input)
	if beforeInput != afterInput {
		t.Fatal("tool argument value changed", beforeInput.JSONString(), afterInput.JSONString())
	}
	if len(before.Children) != len(after.Children) || len(before.ReasoningContent) != len(after.ReasoningContent) {
		t.Fatal("reasoning or message collection changed")
	}
	for index := range before.Children {
		assertResponsesStreamNode(t, before.Children[index], after.Children[index])
	}
	for index := range before.ReasoningContent {
		assertResponsesStreamNode(t, before.ReasoningContent[index], after.ReasoningContent[index])
	}
	for _, expected := range before.Metadata {
		hasField := false
		for _, actual := range after.Metadata {
			if actual.Codec == expected.Codec && actual.Name == expected.Name && actual.Location == expected.Location {
				hasField = true
				if actual.Value != expected.Value || actual.Update != "" {
					t.Fatal("metadata value changed", expected, actual)
				}
			}
		}
		if !hasField {
			t.Fatal("metadata disappeared", expected)
		}
	}
	beforeResources, _ := p.EncodeValue(before.Resources)
	afterResources, _ := p.EncodeValue(after.Resources)
	if beforeResources != afterResources {
		t.Fatal("resource scope or identity changed", beforeResources.JSONString(), afterResources.JSONString())
	}
}

func responsesCompletedStream(output string, frames ...string) []string {
	result := []string{`{"type":"response.created","response":{"id":"r","object":"response","model":"m","status":"in_progress","created_at":1,"output":[]}}`}
	result = append(result, frames...)
	return append(result, `{"type":"response.completed","response":{"id":"r","object":"response","model":"m","status":"completed","created_at":1,"output":[`+output+`],"usage":{"input_tokens":2,"output_tokens":3,"total_tokens":5}}}`)
}

func TestResponsesTargetWaitsForToolIdentityWithoutReorderingOutputs(t *testing.T) {
	compiled := compileBuiltinPackage(t, "openai-responses")
	event := func(kind p.EventType, id string, node *p.Node) p.Event {
		result := p.Event{SchemaVersion: compiled.CoreVersion(), Type: kind, ResponseID: p.StringValue("r"), Item: node}
		if id != "" {
			result.ItemID = p.StringValue(id)
		}
		return result
	}
	start := event(p.ResponseStarted, "", nil)
	start.Response = &p.Response{SchemaVersion: compiled.CoreVersion(), ID: p.StringValue("r"), Model: p.StringValue("m"), Status: p.StringValue("in_progress"), Attributes: p.Object{"created_at": testValue(t, "1")}}
	tool := event(p.ItemStarted, "tool", &p.Node{Kind: p.ToolCallNode, Input: &p.ToolInput{Kind: p.JSONInput}})
	prefix := event(p.ItemDelta, "tool", nil)
	prefix.Delta = p.StringValue(`{"n":`)
	identity := event(p.ItemDelta, "tool", &p.Node{Kind: p.ToolCallNode, Name: p.StringValue("lookup"), CallID: p.StringValue("call"), Input: &p.ToolInput{Kind: p.JSONInput}})
	identity.CallID, identity.Delta = p.StringValue("call"), p.StringValue("9007199254740993}")
	finish := event(p.ItemFinished, "tool", nil)
	terminal := event(p.ResponseFinished, "", nil)
	terminal.Response = &p.Response{SchemaVersion: compiled.CoreVersion(), Status: p.StringValue("completed"), Attributes: p.Object{"finishReason": p.StringValue("tool_calls")}}
	events := []p.Event{start, tool, prefix, event(p.ItemStarted, "text", &p.Node{Kind: p.TextNode, Payload: p.StringValue("ready")}), event(p.ItemFinished, "text", nil), identity, finish, terminal}
	frames := make([]*p.EventFrame, len(events))
	for index, event := range events {
		frames[index] = &p.EventFrame{Events: []p.Event{event}}
	}
	scope := p.Scope{Provider: "fixture", Account: "fixture", Model: "m"}
	wire := encodePackageStream(t, compiled, frames, scope, false)
	_, response := decodePackageStream(t, compiled, wire, scope)
	if len(response.Content) != 2 {
		t.Fatal("delayed identity lost an output", response.Content)
	}
	call, message := response.Content[0], response.Content[1]
	if call.Kind != p.ToolCallNode || !call.CallID.IsString("call") || !call.Name.IsString("lookup") || call.Input == nil || call.Input.Value.JSONString() != `{"n":9007199254740993}` {
		t.Fatal("tool identity or argument bytes changed", call)
	}
	if message.Kind != p.MessageNode || len(message.Children) != 1 || !message.Children[0].Payload.IsString("ready") {
		t.Fatal("pending message changed", message)
	}
	starts := 0
	for _, raw := range wire {
		fields, err := testValue(t, raw).ReadObject()
		if err != nil {
			t.Fatal(err)
		}
		if fields["type"].IsString("response.output_item.added") {
			starts++
		}
	}
	if starts != 2 {
		t.Fatal("late identity duplicated an output start", starts)
	}
}

func TestResponsesPackageRebuildsSourceFramesWithoutSequence(t *testing.T) {
	compiled := compileBuiltinPackage(t, "openai-responses")
	scope := p.Scope{Provider: "fixture", Account: "fixture", Model: "m"}
	values := phaseStreamFrames(t, "done", []string{`"final_answer"`})
	source := make([]string, len(values))
	for index, value := range values {
		fields, err := value.ReadObject()
		if err != nil {
			t.Fatal(err)
		}
		delete(fields, "sequence_number")
		frame, err := p.EncodeValue(fields)
		if err != nil {
			t.Fatal(err)
		}
		source[index] = frame.JSONString()
	}
	frames, response := decodePackageStream(t, compiled, source, scope)
	wire := encodePackageStream(t, compiled, frames, scope, true)
	for index, raw := range wire {
		fields, err := testValue(t, raw).ReadObject()
		if err != nil {
			t.Fatal(err)
		}
		var sequence int
		if fields["sequence_number"].IsZero() || fields["sequence_number"].Decode(&sequence) != nil || sequence != index {
			t.Fatal("reconstructed output has no standard sequence", index, raw)
		}
	}
	_, rebuilt := decodePackageStream(t, compiled, wire, scope)
	if len(response.Content) != len(rebuilt.Content) {
		t.Fatal("rebuilding missing sequences changed output count")
	}
	for index := range response.Content {
		assertResponsesStreamNode(t, response.Content[index], rebuilt.Content[index])
	}
}
