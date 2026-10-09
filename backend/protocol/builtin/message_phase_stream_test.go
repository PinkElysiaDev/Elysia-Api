package builtin

import (
	"fmt"
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

// Three messages deliberately carry different phases; one has two parts and
// one is empty. A phase must never migrate to a neighboring output message.
func phaseStreamFrames(t *testing.T, timing string, phases []string) []p.Value {
	t.Helper()
	frames := []p.Value{testValue(t, `{"type":"response.created","response":{"id":"r","model":"m","object":"response","created_at":1,"status":"in_progress","output":[]}}`)}
	var output []p.Value
	for i, phase := range phases {
		index, _ := p.EncodeValue(i)
		id := p.StringValue(fmt.Sprintf("msg_%d", i))
		message := p.Object{"id": id, "type": p.StringValue("message"), "role": p.StringValue("assistant"), "status": p.StringValue("in_progress"), "content": array(nil)}
		if timing == "start" && phase != "" {
			message["phase"] = testValue(t, phase)
		}
		frames = append(frames, object(p.Object{"type": p.StringValue("response.output_item.added"), "output_index": index, "item": object(message)}))
		var parts []p.Value
		count := 1
		if i == 1 {
			count = 2
		}
		if i == 2 {
			count = 0
		}
		for j := 0; j < count; j++ {
			contentIndex, _ := p.EncodeValue(j)
			text := p.StringValue(fmt.Sprintf("text_%d_%d", i, j))
			frame := p.Object{"type": p.StringValue("response.content_part.added"), "output_index": index, "content_index": contentIndex, "item_id": id, "part": testValue(t, `{"type":"output_text","text":"","annotations":[]}`)}
			frames = append(frames, object(frame))
			delete(frame, "part")
			frame["type"], frame["delta"] = p.StringValue("response.output_text.delta"), text
			frames = append(frames, object(frame))
			delete(frame, "delta")
			frame["type"], frame["text"] = p.StringValue("response.output_text.done"), text
			frames = append(frames, object(frame))
			delete(frame, "text")
			part := object(p.Object{"type": p.StringValue("output_text"), "text": text, "annotations": array(nil)})
			frame["type"], frame["part"] = p.StringValue("response.content_part.done"), part
			frames = append(frames, object(frame))
			parts = append(parts, part)
		}
		message["content"], message["status"] = array(parts), p.StringValue("completed")
		if phase != "" {
			message["phase"] = testValue(t, phase)
		}
		if timing != "terminal" {
			frames = append(frames, object(p.Object{"type": p.StringValue("response.output_item.done"), "output_index": index, "item": object(message)}))
		}
		output = append(output, object(message))
	}
	frames = append(frames, object(p.Object{"type": p.StringValue("response.completed"), "response": object(p.Object{"id": p.StringValue("r"), "model": p.StringValue("m"), "created_at": testValue(t, `1`), "object": p.StringValue("response"), "status": p.StringValue("completed"), "output": array(output)})}))
	return frames
}

func TestResponsesMessagePhaseStreamOwnership(t *testing.T) {
	for _, timing := range []string{"start", "done", "terminal"} {
		for _, phases := range [][]string{{`"commentary"`, `"final_answer"`, `null`}, {`null`, ``, `"final_answer"`}} {
			t.Run(timing+strings.Join(phases, "/"), func(t *testing.T) {
				compiled := shippedProjectionProtocol(t, Responses)
				decode := p.EvaluationContext{State: p.NewEvaluationState()}
				encode := p.EvaluationContext{State: p.NewEvaluationState()}
				collector, err := p.NewResponseCollector(p.Target{Protocol: compiled.Identity(), Direction: p.EncodeEvent, Capabilities: compiled.Capabilities(p.EncodeEvent)}, p.DefaultLimits())
				if err != nil {
					t.Fatal(err)
				}
				var rendered []p.Value
				for _, value := range phaseStreamFrames(t, timing, phases) {
					frame, err := compiled.DecodeFrame(t.Context(), value, decode)
					if err != nil {
						t.Fatal(string(value.Bytes()), err)
					}
					for _, event := range frame.Events {
						if _, _, err = collector.Consume(event); err != nil {
							t.Fatal(err)
						}
					}
					frames, err := compiled.EncodeFrame(t.Context(), &p.EventFrame{Events: frame.Events}, encode)
					if err != nil {
						t.Fatal(err)
					}
					rendered = append(rendered, frames...)
				}
				end, err := compiled.FinishEvents(t.Context(), encode)
				if err != nil {
					t.Fatal(err)
				}
				rendered = append(rendered, end...)
				response, err := collector.Finish()
				if err != nil {
					t.Fatal(err)
				}
				for i, message := range response.Content {
					var phase p.Value
					for _, m := range message.Metadata {
						if m.Name == "phase" {
							phase = m.Value
						}
					}
					if phases[i] == "" {
						if !phase.IsZero() {
							t.Fatal("invented phase")
						}
					} else {
						sameJSON(t, phase.Bytes(), phases[i])
					}
				}
				for _, value := range rendered {
					if err := compiled.ValidateWireOutput(p.EncodeEvent, value); err != nil {
						t.Fatal(string(value.Bytes()), err)
					}
					fields, _ := value.ReadObject()
					var messages []p.Value
					if fields["type"] == p.StringValue("response.output_item.done") {
						messages = append(messages, fields["item"])
					}
					if fields["type"] == p.StringValue("response.completed") {
						final, _ := fields["response"].ReadObject()
						messages, _ = readArray(final["output"])
						if len(messages) != len(phases) {
							t.Fatalf("message boundaries changed: got %d outputs, want %d", len(messages), len(phases))
						}
						for i, value := range messages {
							message, _ := value.ReadObject()
							if message["id"] != p.StringValue(fmt.Sprintf("msg_%d", i)) {
								t.Fatalf("message identity changed: %s", value.Bytes())
							}
							parts, _ := readArray(message["content"])
							if len(parts) != []int{1, 2, 0}[i] {
								t.Fatalf("message parts changed: %s", value.Bytes())
							}
						}
					}
					for _, v := range messages {
						m, _ := v.ReadObject()
						parts, _ := readArray(m["content"])
						owner := 2
						for _, part := range parts {
							block, _ := part.ReadObject()
							if !block["phase"].IsZero() {
								t.Fatal("phase moved onto a content part")
							}
							var text string
							_ = block["text"].Decode(&text)
							_, _ = fmt.Sscanf(text, "text_%d_", &owner)
						}
						if phases[owner] == "" {
							if !m["phase"].IsZero() {
								t.Fatal("neighbor phase leaked")
							}
						} else {
							sameJSON(t, m["phase"].Bytes(), phases[owner])
						}
					}
				}
			})
		}
	}
}

func TestResponsesMessagePhaseRejectsInvalidFinalWire(t *testing.T) {
	c := shippedProjectionProtocol(t, Responses)
	for _, value := range []string{`42`, `true`, `[]`, `{}`, `"unknown"`} {
		message := `{"id":"m","type":"message","role":"assistant","status":"completed","phase":` + value + `,"content":[]}`
		cases := map[p.Direction]string{
			p.EncodeRequest:  `{"model":"m","input":[` + message + `]}`,
			p.EncodeResponse: `{"id":"r","model":"m","object":"response","created_at":1,"status":"completed","output":[` + message + `]}`,
			p.EncodeEvent:    `{"type":"response.output_item.done","sequence_number":1,"output_index":0,"item":` + message + `}`,
		}
		for direction, body := range cases {
			if err := c.ValidateWireOutput(direction, testValue(t, body)); err == nil || !strings.Contains(err.Error(), "/phase") {
				t.Fatal(direction, value, err)
			}
		}
	}
}

func TestResponsesMessagePhaseAndTextCannotChangeAfterCompletion(t *testing.T) {
	c := shippedProjectionProtocol(t, Responses)
	for _, field := range []string{"phase", "text", "id", "role", "status", "annotations", "omitted_message"} {
		frames := phaseStreamFrames(t, "done", []string{`"commentary"`})
		last := string(frames[len(frames)-1].Bytes())
		switch field {
		case "phase":
			last = strings.Replace(last, `"commentary"`, `"final_answer"`, 1)
		case "text":
			last = strings.Replace(last, `text_0_0`, `rewritten`, 1)
		case "id":
			last = strings.Replace(last, `msg_0`, `changed`, 1)
		case "role":
			last = strings.Replace(last, `"assistant"`, `"user"`, 1)
		case "status":
			last = strings.Replace(last, `"status":"completed"`, `"status":"incomplete"`, 1)
		case "annotations":
			last = strings.Replace(last, `"annotations":[]`, `"annotations":[{"type":"url_citation","url":"https://example.invalid","title":"test","start_index":0,"end_index":2}]`, 1)
		case "omitted_message":
			frame, _ := testValue(t, last).ReadObject()
			response, _ := frame["response"].ReadObject()
			response["output"] = array(nil)
			frame["response"] = object(response)
			last = string(object(frame).Bytes())
		}
		frames[len(frames)-1] = testValue(t, last)
		options := p.EvaluationContext{State: p.NewEvaluationState()}
		for i, frame := range frames {
			_, err := c.DecodeFrame(t.Context(), frame, options)
			if i == len(frames)-1 {
				if err == nil {
					t.Fatal("accepted changed terminal", field)
				}
			} else if err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestResponsesMessagePhaseNativeFramesRemainUnchanged(t *testing.T) {
	c := shippedProjectionProtocol(t, Responses)
	options := p.EvaluationContext{State: p.NewEvaluationState()}
	for _, value := range phaseStreamFrames(t, "done", []string{`"commentary"`, `"final_answer"`, `null`}) {
		frame, err := c.DecodeFrame(t.Context(), value, options)
		if err != nil {
			t.Fatal(err)
		}
		wire, err := c.EncodeFrame(t.Context(), frame, options)
		if err != nil || len(wire) != 1 {
			t.Fatal(err, wire)
		}
		sameJSON(t, wire[0].Bytes(), string(value.Bytes()))
	}
}

func TestResponsesMessagePhaseStreamProjectionModes(t *testing.T) {
	from := shippedProjectionProtocol(t, Responses)
	for _, target := range []string{Chat, Anthropic, Gemini} {
		to := shippedProjectionProtocol(t, target)
		for _, value := range []string{`null`, `"commentary"`, `"final_answer"`} {
			for _, mode := range []string{"compatible", "strict", "disabled"} {
				policy := p.DefaultConversionPolicy(to, from)
				if mode == "strict" {
					policy.Mode = mode
				}
				if mode == "disabled" {
					for i := range policy.Rules {
						if policy.Rules[i].Action == "response_metadata" {
							policy.Rules[i].Enabled = false
						}
					}
				}
				conversion, err := p.ResolveConversion(policy)
				if err != nil {
					t.Fatal(err)
				}
				meta := p.ResponseMetadata{Codec: Responses, SourceCodec: Responses, Location: "message", Name: "phase", Path: "/output/0/phase", Value: testValue(t, value)}
				event := p.Event{SchemaVersion: p.SemanticSchemaVersion, Type: p.ItemStarted, ItemID: p.StringValue("part"), Item: &p.Node{Kind: p.TextNode, Payload: p.StringValue("OK"), Metadata: []p.ResponseMetadata{meta}}}
				sink := &p.DiagnosticSink{}
				projected, err := conversion.Event(t.Context(), event, conversion.VerificationRoute(from.Identity(), to.Identity(), p.SSE), sink)
				if mode == "strict" && value != "null" {
					if err == nil || !strings.Contains(err.Error(), "/phase") {
						t.Fatal(target, mode, value, err)
					}
					continue
				}
				if err != nil {
					t.Fatal(err)
				}
				_, err = to.EncodeFrames(t.Context(), projected, p.EvaluationContext{State: p.NewEvaluationState()})
				if mode == "disabled" {
					if err == nil || !strings.Contains(err.Error(), "/phase") {
						t.Fatal("disabled stream rule still omitted phase", target, err)
					}
					continue
				}
				if err != nil {
					t.Fatal(target, value, err)
				}
				if !sinkHas(sink, "/phase") {
					t.Fatal("stream phase loss lacks diagnostic")
				}
				for _, issue := range sink.Issues() {
					if strings.HasSuffix(issue.Path, "/phase") && (issue.PolicyHash != conversion.Hash || issue.RuleID == "") {
						t.Fatal(issue)
					}
				}
				if len(event.Item.Metadata) != 1 {
					t.Fatal("source event mutated")
				}
			}
		}
	}
}
