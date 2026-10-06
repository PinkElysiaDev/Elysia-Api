package builtin

import (
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func convertStream(t *testing.T, source, target string, bodies []string) []p.Value {
	t.Helper()
	from, to := testCompiled(t, source), testCompiled(t, target)
	options := p.EvaluationContext{State: p.NewEvaluationState()}
	replay, err := p.NewEventReplay(p.Target{Protocol: to.Identity(), Direction: p.EncodeEvent, Capabilities: to.Capabilities(p.EncodeEvent)}, p.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	var output []p.Value
	for _, body := range bodies {
		frame, err := from.DecodeFrame(t.Context(), testValue(t, body), options)
		if err != nil {
			t.Fatal(err)
		}
		for _, event := range frame.Events {
			if _, err := replay.Consume(event); err != nil {
				t.Fatal(err)
			}
		}
		frames, err := to.EncodeFrame(t.Context(), frame, options)
		if err != nil {
			t.Fatal(err)
		}
		output = append(output, frames...)
	}
	if err := replay.Finish(); err != nil {
		t.Fatal(err)
	}
	tail, err := to.FinishEvents(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}
	return append(output, tail...)
}

func TestBuiltinTerminalReasonsAndFailure(t *testing.T) {
	for _, target := range []string{Chat, Anthropic, Gemini} {
		t.Run(target, func(t *testing.T) {
			frames := convertStream(t, Responses, target, []string{`{"type":"response.created","response":{"id":"r","status":"in_progress","output":[]}}`, `{"type":"response.incomplete","response":{"id":"r","status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"output":[]}}`})
			decoder := testCompiled(t, target)
			options := p.EvaluationContext{State: p.NewEvaluationState()}
			hasTerminal := false
			for _, wire := range frames {
				frame, err := decoder.DecodeFrame(t.Context(), wire, options)
				if err != nil {
					t.Fatal(err)
				}
				for _, event := range frame.Events {
					if event.Type == p.ResponseFinished {
						hasTerminal = true
						if string(event.Response.Attributes["finishReason"].Bytes()) != `"length"` {
							t.Fatal("incomplete response changed to success")
						}
					}
				}
			}
			if !hasTerminal {
				t.Fatal("missing terminal")
			}
		})
	}
	for _, target := range modules {
		t.Run("failure-"+target.name, func(t *testing.T) {
			frames := convertStream(t, Anthropic, target.name, []string{`{"type":"error","error":{"type":"overloaded_error","message":"retry later"}}`})
			if len(frames) != 1 || !strings.Contains(string(frames[0].Bytes()), "retry later") {
				t.Fatalf("failure was lost or followed by success: %v", frames)
			}
		})
	}
}

func TestBuiltinStreamExtensionsStayNative(t *testing.T) {
	compiled := testCompiled(t, Chat)
	options := p.EvaluationContext{State: p.NewEvaluationState()}
	input := testValue(t, `{"id":"r","choices":[{"index":0,"delta":{"content":"hi","vendor":{"n":9007199254740993}}}],"nullable":null}`)
	frame, err := compiled.DecodeFrame(t.Context(), input, options)
	if err != nil {
		t.Fatal(err)
	}
	if frame.Events[0].Unmapped == nil {
		t.Fatal("wire extensions lost their semantic evidence")
	}
	output, err := compiled.EncodeFrame(t.Context(), frame, options)
	if err != nil || len(output) != 1 {
		t.Fatal(output, err)
	}
	sameJSON(t, output[0].Bytes(), string(input.Bytes()))
	foreign := testCompiled(t, Anthropic)
	if _, err := foreign.EncodeFrame(t.Context(), frame, options); err == nil {
		t.Fatal("unknown fields silently crossed protocols")
	}
	// Tail metadata is attached to usage itself, so a native tail remains legal
	// after the response terminal without inventing a second content event.
	tail := testValue(t, `{"id":"r","choices":[],"usage":{"prompt_tokens":2},"billing":false}`)
	frame, err = compiled.DecodeFrame(t.Context(), tail, options)
	if err != nil || len(frame.Events) != 1 || frame.Events[0].Type != p.UsageUpdated || frame.Events[0].Unmapped == nil {
		t.Fatal(frame, err)
	}
}

func TestResponsesHostedStreamAndSequenceGuard(t *testing.T) {
	compiled := testCompiled(t, Responses)
	options := p.EvaluationContext{State: p.NewEvaluationState()}
	input := testValue(t, `{"type":"response.web_search_call.searching","sequence_number":7,"item_id":"search","output_index":0,"vendor":false}`)
	frame, err := compiled.DecodeFrame(t.Context(), input, options)
	if err != nil {
		t.Fatal(err)
	}
	output, err := compiled.EncodeFrame(t.Context(), frame, options)
	if err != nil || len(output) != 1 {
		t.Fatal(output, err)
	}
	sameJSON(t, output[0].Bytes(), string(input.Bytes()))
	if _, err := compiled.DecodeFrame(t.Context(), input, options); err == nil {
		t.Fatal("repeated provider sequence accepted")
	}
	if _, err := compiled.DecodeFrame(t.Context(), input, p.EvaluationContext{State: p.NewEvaluationState()}); err != nil {
		t.Fatal("codec state leaked across requests", err)
	}
}

func TestBuiltinStreamingTextDoesNotAccumulateAtIncrementalTargets(t *testing.T) {
	for _, target := range []string{Chat, Anthropic, Gemini} {
		t.Run(target, func(t *testing.T) {
			compiled := testCompiled(t, target)
			options := p.EvaluationContext{State: p.NewEvaluationState()}
			events := []p.Event{{SchemaVersion: 1, Type: p.ResponseStarted}, {SchemaVersion: 1, Type: p.ItemStarted, ItemID: p.StringValue("text"), Item: &p.Node{Kind: p.TextNode}}}
			for _, event := range events {
				if _, err := compiled.EncodeFrames(t.Context(), event, options); err != nil {
					t.Fatal(err)
				}
			}
			delta := p.StringValue(strings.Repeat("x", 64*1024))
			for index := 0; index < 2*p.DefaultLimits().BufferBytes/(64*1024); index++ {
				if _, err := compiled.EncodeFrames(t.Context(), p.Event{SchemaVersion: 1, Type: p.ItemDelta, ItemID: p.StringValue("text"), Delta: delta}, options); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := compiled.EncodeFrames(t.Context(), p.Event{SchemaVersion: 1, Type: p.ResponseFinished, Response: &p.Response{SchemaVersion: 1, Status: p.StringValue("completed"), Attributes: p.Object{"finishReason": p.StringValue("stop")}}}, options); err != nil {
				t.Fatal(err)
			}
			if _, err := compiled.FinishEvents(t.Context(), options); err != nil {
				t.Fatal(err)
			}
		})
	}
}

var textStreams = map[string][]string{
	Chat:      {`{"id":"r","model":"m","choices":[{"index":0,"delta":{"role":"assistant","content":"hi"},"finish_reason":null}]}`, `{"id":"r","model":"m","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`, `{"id":"r","choices":[],"usage":{"prompt_tokens":20,"completion_tokens":2,"total_tokens":22,"prompt_tokens_details":{"cached_tokens":15}}}`},
	Anthropic: {`{"type":"message_start","message":{"id":"r","model":"m","content":[],"usage":{"input_tokens":5,"cache_read_input_tokens":15}}}`, `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`, `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi"}}`, `{"type":"content_block_stop","index":0}`, `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":2}}`, `{"type":"message_stop"}`},
	Responses: {`{"type":"response.created","response":{"id":"r","model":"m","output":[],"status":"in_progress"}}`, `{"type":"response.output_item.added","output_index":0,"item":{"type":"message","role":"assistant","content":[]}}`, `{"type":"response.content_part.added","output_index":0,"content_index":0,"part":{"type":"output_text","text":""}}`, `{"type":"response.output_text.delta","output_index":0,"content_index":0,"delta":"hi"}`, `{"type":"response.output_text.done","output_index":0,"content_index":0,"text":"hi"}`, `{"type":"response.content_part.done","output_index":0,"content_index":0,"part":{"type":"output_text","text":"hi"}}`, `{"type":"response.completed","response":{"id":"r","model":"m","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hi"}]}],"usage":{"input_tokens":20,"output_tokens":2,"total_tokens":22,"input_tokens_details":{"cached_tokens":15}}}}`},
	Gemini:    {`{"responseId":"r","modelVersion":"m","candidates":[{"index":0,"content":{"role":"model","parts":[{"text":"hi"}]}}]}`, `{"responseId":"r","modelVersion":"m","candidates":[{"index":0,"content":{"role":"model","parts":[]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":20,"candidatesTokenCount":2,"totalTokenCount":22,"cachedContentTokenCount":15}}`},
}

var toolStreams = map[string][]string{
	Chat:      {`{"id":"r","model":"m","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"type":"function","id":"call","function":{"name":"lookup","arguments":""}}]}}]}`, `{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"n\":9007199254740993}"}}]}}]}`, `{"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`},
	Anthropic: {`{"type":"message_start","message":{"id":"r","model":"m","content":[]}}`, `{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"call","name":"lookup","input":{}}}`, `{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"n\":9007199254740993}"}}`, `{"type":"content_block_stop","index":0}`, `{"type":"message_delta","delta":{"stop_reason":"tool_use"}}`, `{"type":"message_stop"}`},
	Responses: {`{"type":"response.created","response":{"id":"r","model":"m","output":[],"status":"in_progress"}}`, `{"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","call_id":"call","name":"lookup","arguments":""}}`, `{"type":"response.function_call_arguments.delta","output_index":0,"delta":"{\"n\":9007199254740993}"}`, `{"type":"response.function_call_arguments.done","output_index":0,"arguments":"{\"n\":9007199254740993}"}`, `{"type":"response.output_item.done","output_index":0,"item":{"type":"function_call","call_id":"call","name":"lookup","arguments":"{\"n\":9007199254740993}"}}`, `{"type":"response.completed","response":{"id":"r","model":"m","status":"completed","output":[{"type":"function_call","call_id":"call","name":"lookup","arguments":"{\"n\":9007199254740993}"}]}}`},
	Gemini:    {`{"responseId":"r","modelVersion":"m","candidates":[{"index":0,"content":{"role":"model","parts":[{"functionCall":{"id":"call","name":"lookup","args":{"n":9007199254740993}}}]},"finishReason":"STOP"}]}`},
}

func TestBuiltinStreamToolMatrix(t *testing.T) {
	for _, source := range modules {
		for _, target := range modules {
			t.Run(source.name+"-"+target.name, func(t *testing.T) {
				frames := convertStream(t, source.name, target.name, toolStreams[source.name])
				compiled := testCompiled(t, target.name)
				options := p.EvaluationContext{State: p.NewEvaluationState()}
				collector, err := p.NewResponseCollector(p.Target{Protocol: compiled.Identity(), Direction: p.EncodeEvent, Capabilities: compiled.Capabilities(p.EncodeEvent)}, p.DefaultLimits())
				if err != nil {
					t.Fatal(err)
				}
				for _, wire := range frames {
					frame, err := compiled.DecodeFrame(t.Context(), wire, options)
					if err != nil {
						t.Fatalf("%s: %v", wire.Bytes(), err)
					}
					for _, event := range frame.Events {
						if _, _, err := collector.Consume(event); err != nil {
							t.Fatal(err)
						}
					}
				}
				response, err := collector.Finish()
				if err != nil {
					t.Fatal(err)
				}
				if len(response.Content) != 1 {
					t.Fatalf("tool count: %d", len(response.Content))
				}
				node := response.Content[0]
				if node.Kind != p.ToolCallNode || string(node.CallID.Bytes()) != `"call"` || string(node.Name.Bytes()) != `"lookup"` || node.Input == nil || string(node.Input.Value.Bytes()) != `{"n":9007199254740993}` {
					t.Fatalf("tool semantics lost: %+v", node)
				}
				if string(response.Attributes["finishReason"].Bytes()) != `"tool_calls"` {
					t.Fatalf("tool handoff lost: %+v", response.Attributes)
				}
			})
		}
	}
}

func TestBuiltinStreamMatrixPreservesTextAndUsageTails(t *testing.T) {
	for _, source := range modules {
		for _, target := range modules {
			t.Run(source.name+"-"+target.name, func(t *testing.T) {
				from, to := testCompiled(t, source.name), testCompiled(t, target.name)
				options := p.EvaluationContext{State: p.NewEvaluationState()}
				replay, err := p.NewEventReplay(p.Target{Protocol: to.Identity(), Direction: p.EncodeEvent, Capabilities: to.Capabilities(p.EncodeEvent)}, p.DefaultLimits())
				if err != nil {
					t.Fatal(err)
				}
				var wire []p.Value
				for _, body := range textStreams[source.name] {
					frame, err := from.DecodeFrame(t.Context(), testValue(t, body), options)
					if err != nil {
						t.Fatal(err)
					}
					for _, event := range frame.Events {
						if _, err := replay.Consume(event); err != nil {
							t.Fatal(err)
						}
					}
					frames, err := to.EncodeFrame(t.Context(), frame, options)
					if err != nil {
						t.Fatal(err)
					}
					wire = append(wire, frames...)
				}
				if err := replay.Finish(); err != nil {
					t.Fatal(err)
				}
				frames, err := to.FinishEvents(t.Context(), options)
				if err != nil {
					t.Fatal(err)
				}
				wire = append(wire, frames...)
				if source.name == target.name {
					if len(wire) != len(textStreams[source.name]) {
						t.Fatal("native frames replayed more than once")
					}
					for index, body := range wire {
						sameJSON(t, body.Bytes(), textStreams[source.name][index])
					}
				}
				collector, err := p.NewResponseCollector(p.Target{Protocol: to.Identity(), Direction: p.EncodeEvent, Capabilities: to.Capabilities(p.EncodeEvent)}, p.DefaultLimits())
				if err != nil {
					t.Fatal(err)
				}
				decodeOptions := p.EvaluationContext{State: p.NewEvaluationState()}
				var text string
				for _, body := range wire {
					frame, err := to.DecodeFrame(t.Context(), body, decodeOptions)
					if err != nil {
						t.Fatalf("%s: %v", body.Bytes(), err)
					}
					for _, event := range frame.Events {
						_, delta, err := collector.Consume(event)
						if err != nil {
							t.Fatal(err)
						}
						text += delta
					}
				}
				response, err := collector.Finish()
				if err != nil {
					t.Fatal(err)
				}
				if text != "hi" || response.Usage == nil || response.Usage.Input.Count != 20 || response.Usage.CacheRead.Count != 15 || response.Usage.Output.Count != 2 {
					t.Fatalf("text=%q usage=%+v", text, response.Usage)
				}
			})
		}
	}
}
