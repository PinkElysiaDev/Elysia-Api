package builtin

import (
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestResponsesMessageBoundaryInterleavedTool(t *testing.T) {
	frames := []string{
		`{"type":"response.created","response":{"id":"r","model":"m","created_at":1,"object":"response","status":"in_progress","output":[]}}`,
		`{"type":"response.output_item.added","output_index":0,"item":{"id":"empty","type":"message","role":"assistant","status":"in_progress","content":[]}}`,
		`{"type":"response.output_item.done","output_index":0,"item":{"id":"empty","type":"message","role":"assistant","status":"completed","content":[]}}`,
		`{"type":"response.output_item.added","output_index":1,"item":{"id":"msg","type":"message","role":"assistant","status":"in_progress","content":[]}}`,
		`{"type":"response.output_item.added","output_index":2,"item":{"id":"tool","type":"function_call","call_id":"call","name":"lookup","status":"in_progress","arguments":""}}`,
		`{"type":"response.output_item.done","output_index":2,"item":{"id":"tool","type":"function_call","call_id":"call","name":"lookup","status":"completed","arguments":"{}"}}`,
		`{"type":"response.content_part.added","output_index":1,"content_index":0,"item_id":"msg","part":{"type":"output_text","text":"","annotations":[]}}`,
		`{"type":"response.output_text.delta","output_index":1,"content_index":0,"item_id":"msg","delta":"hi"}`,
		`{"type":"response.content_part.done","output_index":1,"content_index":0,"item_id":"msg","part":{"type":"output_text","text":"hi","annotations":[]}}`,
		`{"type":"response.content_part.added","output_index":1,"content_index":1,"item_id":"msg","part":{"type":"refusal","refusal":""}}`,
		`{"type":"response.refusal.delta","output_index":1,"content_index":1,"item_id":"msg","delta":"no"}`,
		`{"type":"response.content_part.done","output_index":1,"content_index":1,"item_id":"msg","part":{"type":"refusal","refusal":"no"}}`,
		`{"type":"response.output_item.done","output_index":1,"item":{"id":"msg","type":"message","role":"assistant","status":"completed","phase":"final_answer","content":[{"type":"output_text","text":"hi","annotations":[]},{"type":"refusal","refusal":"no"}]}}`,
		`{"type":"response.completed","response":{"id":"r","model":"m","object":"response","created_at":1,"status":"completed","output":[{"id":"empty","type":"message","role":"assistant","status":"completed","content":[]},{"id":"msg","type":"message","role":"assistant","status":"completed","phase":"final_answer","content":[{"type":"output_text","text":"hi","annotations":[]},{"type":"refusal","refusal":"no"}]},{"id":"tool","type":"function_call","call_id":"call","name":"lookup","status":"completed","arguments":"{}"}]}}`,
	}
	c := shippedProjectionProtocol(t, Responses)
	decodeOptions := fixtureOptions(p.EvaluationContext{State: p.NewEvaluationState()})
	encodeOptions, consume := bindFixtureOutput(t, c, fixtureOptions(p.EvaluationContext{State: p.NewEvaluationState()}))
	target := c.Target(p.EncodeEvent, p.Scope{})
	collector, _ := p.NewResponseCollector(target, p.DefaultLimits())
	var rendered []p.Value
	for i, body := range frames {
		frame, err := c.DecodeFrame(t.Context(), testValue(t, body), decodeOptions)
		if err != nil {
			t.Fatal(i, err)
		}
		for _, event := range frame.Events {
			if _, _, err := collector.Consume(event); err != nil {
				t.Fatal(i, err)
			}
		}
		wire, err := c.EncodeFrame(t.Context(), &p.EventFrame{Events: frame.Events}, encodeOptions)
		if err != nil {
			t.Fatal(i, err)
		}
		if i == 7 && (len(wire) != 1 || !strings.Contains(string(wire[0].Bytes()), `"delta":"hi"`)) {
			t.Fatal("text was buffered until parent completion", wire)
		}
		consume(wire)
		rendered = append(rendered, wire...)
	}
	tail, err := c.FinishEvents(t.Context(), encodeOptions)
	if err != nil {
		t.Fatal(err)
	}
	consume(tail)
	rendered = append(rendered, tail...)
	added, done, parts := 0, 0, 0
	for _, wire := range rendered {
		f, _ := wire.ReadObject()
		switch f["type"] {
		case p.StringValue("response.output_item.added"):
			added++
		case p.StringValue("response.output_item.done"):
			done++
		case p.StringValue("response.content_part.added"):
			if f["item_id"] != p.StringValue("msg") || f["output_index"] != testValue(t, `1`) || f["content_index"] != testValue(t, []string{`0`, `1`}[parts]) {
				t.Fatal(string(wire.Bytes()))
			}
			parts++
		case p.StringValue("response.completed"):
			response, _ := f["response"].ReadObject()
			output, _ := readArray(response["output"])
			if len(output) != 3 {
				t.Fatal(string(wire.Bytes()))
			}
			for i, id := range []string{"empty", "msg", "tool"} {
				item, _ := output[i].ReadObject()
				if item["id"] != p.StringValue(id) {
					t.Fatal(string(wire.Bytes()))
				}
			}
			message, _ := output[1].ReadObject()
			content, _ := readArray(message["content"])
			if len(content) != 2 {
				t.Fatal(string(wire.Bytes()))
			}
		}
	}
	if added != 3 || done != 3 || parts != 2 {
		t.Fatal(added, done, parts)
	}
	if _, err := collector.Finish(); err != nil {
		t.Fatal(err)
	}
}

func TestResponsesMessageBoundaryProjectionRules(t *testing.T) {
	from := shippedProjectionProtocol(t, Responses)
	for _, name := range []string{Responses, Chat, Anthropic, Gemini} {
		for _, mode := range []string{"compatible", "strict", "disabled"} {
			t.Run(name+"/"+mode, func(t *testing.T) {
				to := shippedProjectionProtocol(t, name)
				policy := p.DefaultConversionPolicy(to, from)
				if mode == "strict" {
					policy.Mode = mode
				}
				if mode == "disabled" {
					for i := range policy.Rules {
						if policy.Rules[i].ID == "response-shape" || policy.Rules[i].ID == "event-shape" {
							policy.Rules[i].Enabled = false
						}
					}
				}
				conversion, err := p.ResolveConversion(policy)
				if err != nil {
					t.Fatal(err)
				}
				decodeOptions := fixtureOptions(p.EvaluationContext{State: p.NewEvaluationState()})
				encodeOptions, consume := bindFixtureOutput(t, to, fixtureOptions(p.EvaluationContext{State: p.NewEvaluationState()}))
				route := conversion.VerificationRoute(from.Identity(), to.Identity(), p.SSE).WithOperation(encodeOptions)
				encodeOptions = encodeOptions.WithConversion(conversion, route)
				sink := &p.DiagnosticSink{}
				encodeOptions.Diagnostics = sink
				for _, value := range phaseStreamFrames(t, "done", []string{"", "", ""}) {
					frame, decodeErr := from.DecodeFrame(t.Context(), value, decodeOptions)
					if decodeErr != nil {
						t.Fatal(decodeErr)
					}
					route, err = conversion.WithFrameUsage(route, frame.Events)
					if err != nil {
						t.Fatal(err)
					}
					for _, event := range frame.Events {
						var projected p.Event
						projected, err = conversion.Event(t.Context(), event, route, sink)
						if err != nil {
							break
						}
						var wire []p.Value
						wire, err = to.EncodeFrame(t.Context(), &p.EventFrame{Events: []p.Event{projected}}, encodeOptions)
						if err != nil {
							break
						}
						consume(wire)
					}
					if err != nil {
						break
					}
				}
				if name != Responses && mode != "compatible" {
					if err == nil {
						t.Fatal("container loss bypassed policy")
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				tail, err := to.FinishEvents(t.Context(), encodeOptions)
				if err != nil {
					t.Fatal(err)
				}
				consume(tail)
				hasBoundaryDiagnostic := sinkHas(sink, "/parentId")
				for _, issue := range sink.Issues() {
					hasBoundaryDiagnostic = hasBoundaryDiagnostic || issue.RuleID == "event-layout" && issue.Code == p.ConversionDegraded
				}
				if name != Responses && !hasBoundaryDiagnostic {
					t.Fatal("boundary loss was not diagnosed", sink.Issues())
				}
			})
		}
	}
}

func TestResponsesMessageBoundaryIncompleteStatus(t *testing.T) {
	c := shippedProjectionProtocol(t, Responses)
	for _, kind := range []p.NodeKind{p.MessageNode, p.ToolCallNode} {
		node := p.Node{Kind: kind, ID: p.StringValue("item"), Status: p.StringValue("in_progress")}
		if kind == p.MessageNode {
			node.Role = p.StringValue("assistant")
		} else {
			node.CallID, node.Name, node.Input = p.StringValue("call"), p.StringValue("lookup"), &p.ToolInput{Kind: p.JSONInput, Value: testValue(t, `{}`)}
		}
		started := node
		node.Status = p.StringValue("incomplete")
		response := &p.Response{SchemaVersion: p.ProtocolCoreVersion, ID: p.StringValue("r"), Model: p.StringValue("m"), Status: p.StringValue("incomplete"), Attributes: p.Object{"created_at": testValue(t, `1`), "finishReason": p.StringValue("length")}}
		events := []p.Event{
			{SchemaVersion: p.ProtocolCoreVersion, Type: p.ResponseStarted, Response: &p.Response{SchemaVersion: p.ProtocolCoreVersion, ID: response.ID, Model: response.Model, Status: p.StringValue("in_progress"), Attributes: p.Object{"created_at": testValue(t, `1`)}}},
			{SchemaVersion: p.ProtocolCoreVersion, Type: p.ItemStarted, ItemID: p.StringValue("item"), Item: &started},
			{SchemaVersion: p.ProtocolCoreVersion, Type: p.ItemFinished, ItemID: p.StringValue("item"), Item: &node},
			{SchemaVersion: p.ProtocolCoreVersion, Type: p.ResponseFinished, Response: response},
		}
		options, consume := bindFixtureOutput(t, c, fixtureOptions(p.EvaluationContext{State: p.NewEvaluationState()}))
		frames, err := c.EncodeFrame(t.Context(), &p.EventFrame{Events: events}, options)
		if err != nil {
			t.Fatal(kind, err)
		}
		consume(frames)
		tail, err := c.FinishEvents(t.Context(), options)
		if err != nil {
			t.Fatal(kind, err)
		}
		consume(tail)
		frames = append(frames, tail...)
		for _, frame := range frames {
			fields, _ := frame.ReadObject()
			if fields["type"] == p.StringValue("response.output_item.done") {
				item, _ := fields["item"].ReadObject()
				if item["status"] != p.StringValue("incomplete") {
					t.Fatal(kind, string(frame.Bytes()))
				}
			}
		}
		last, _ := frames[len(frames)-1].ReadObject()
		if last["type"] != p.StringValue("response.incomplete") {
			t.Fatal(kind, last)
		}
		final, _ := last["response"].ReadObject()
		output, _ := readArray(final["output"])
		item, _ := output[0].ReadObject()
		if item["status"] != p.StringValue("incomplete") {
			t.Fatal(kind, item)
		}
	}
}
