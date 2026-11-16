package builtin

import (
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestAnthropicSignatureFragmentsAndRepeatedSnapshots(t *testing.T) {
	t.Parallel()
	compiled := testCompiled(t, Anthropic)
	options := fixtureOptions(p.EvaluationContext{State: p.NewEvaluationState(), Scope: p.Scope{Provider: "provider", Account: "account", Model: "m"}})
	collector, err := p.NewResponseCollector(compiled.Target(p.EncodeEvent, options.Scope), p.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	var output []p.Value
	for _, body := range anthropicSignatureFrames() {
		frame, err := compiled.DecodeFrame(t.Context(), testValue(t, body), options)
		if err != nil {
			t.Fatal(err)
		}
		for _, event := range frame.Events {
			if _, _, err := collector.Consume(event); err != nil {
				t.Fatal(err)
			}
		}
		// Force semantic encoding; native replay would conceal duplicate deltas.
		wire, err := compiled.EncodeFrame(t.Context(), &p.EventFrame{Events: frame.Events}, options)
		if err != nil {
			t.Fatal(err)
		}
		output = append(output, wire...)
		for _, event := range frame.Events {
			if event.Type == p.ItemSnapshot {
				duplicate, err := compiled.EncodeFrame(t.Context(), &p.EventFrame{Events: []p.Event{event}}, options)
				if err != nil || len(duplicate) != 0 {
					t.Fatal("duplicate signature snapshot emitted", duplicate, err)
				}
			}
		}
	}
	response, err := collector.Finish()
	if err != nil {
		t.Fatal(err)
	}
	reasoning := fixtureNodes(response.Content, p.ReasoningNode)
	if len(reasoning) != 1 || len(reasoning[0].Resources) != 1 || reasoning[0].Resources[0].ID != p.StringValue("first-second") {
		t.Fatal("signature fragments lost", response)
	}
	var signature strings.Builder
	for _, wire := range output {
		fields, _ := wire.ReadObject()
		delta, _ := fields["delta"].ReadObject()
		var fragment string
		if delta["signature"].Decode(&fragment) == nil {
			signature.WriteString(fragment)
		}
	}
	if signature.String() != "first-second" {
		t.Fatal("wire signature duplicated or truncated", signature.String())
	}
}

func convertStream(t *testing.T, source, target string, bodies []string) []p.Value {
	t.Helper()
	return convertFixtureStream(t, testCompiled(t, source), testCompiled(t, target), bodies)
}

func TestBuiltinTerminalReasonsAndFailure(t *testing.T) {
	t.Parallel()
	for _, target := range []string{Chat, Anthropic, Gemini} {
		t.Run(target, func(t *testing.T) {
			frames := convertStream(t, Responses, target, []string{`{"type":"response.created","response":{"id":"r","status":"in_progress","output":[]}}`, `{"type":"response.incomplete","response":{"id":"r","status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"output":[]}}`})
			decoder := testCompiled(t, target)
			options := fixtureOptions(p.EvaluationContext{State: p.NewEvaluationState()})
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
	for _, target := range packageFixtures {
		t.Run("failure-"+target.name, func(t *testing.T) {
			frames := convertStream(t, Anthropic, target.name, []string{`{"type":"error","error":{"type":"overloaded_error","message":"retry later"}}`})
			if len(frames) != 1 || !strings.Contains(string(frames[0].Bytes()), "retry later") {
				t.Fatalf("failure was lost or followed by success: %v", frames)
			}
		})
	}
}

func TestBuiltinStreamExtensionsStayNative(t *testing.T) {
	t.Parallel()
	compiled := testCompiled(t, Chat)
	options, consume := bindFixtureOutput(t, compiled, fixtureOptions(p.EvaluationContext{State: p.NewEvaluationState()}))
	input := testValue(t, `{"id":"r","model":"m","object":"chat.completion.chunk","created":1,"choices":[{"index":0,"delta":{"role":"assistant","content":"hi","vendor":{"n":9007199254740993}},"finish_reason":null}],"nullable":null}`)
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
	consume(output)
	sameJSON(t, output[0].Bytes(), string(input.Bytes()))
	foreign := testCompiled(t, Anthropic)
	foreignOptions, _ := bindFixtureOutput(t, foreign, fixtureOptions(p.EvaluationContext{State: p.NewEvaluationState()}))
	if _, err := foreign.EncodeFrame(t.Context(), frame, foreignOptions); err == nil {
		t.Fatal("unknown fields silently crossed protocols")
	}
	// Tail metadata is attached to usage itself, so a native tail remains legal
	// after the response terminal without inventing a second content event.
	terminal := testValue(t, `{"id":"r","model":"m","object":"chat.completion.chunk","created":1,"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`)
	if _, err := compiled.DecodeFrame(t.Context(), terminal, options); err != nil {
		t.Fatal(err)
	}
	tail := testValue(t, `{"id":"r","model":"m","object":"chat.completion.chunk","created":1,"choices":[],"usage":{"prompt_tokens":2},"billing":false}`)
	frame, err = compiled.DecodeFrame(t.Context(), tail, options)
	if err != nil || len(frame.Events) != 1 || frame.Events[0].Type != p.UsageUpdated || frame.Events[0].Unmapped == nil {
		t.Fatal(frame, err)
	}
}

func TestResponsesHostedStreamAndSequenceGuard(t *testing.T) {
	t.Parallel()
	compiled := testCompiled(t, Responses)
	options, consume := bindFixtureOutput(t, compiled, fixtureOptions(p.EvaluationContext{State: p.NewEvaluationState()}))
	start := func(options p.EvaluationContext, consume func([]p.Value)) {
		t.Helper()
		for _, body := range []string{
			`{"type":"response.created","sequence_number":3,"response":{"id":"r","model":"m","object":"response","created_at":1,"status":"in_progress","output":[]}}`,
			`{"type":"response.output_item.added","sequence_number":5,"output_index":0,"item":{"id":"search","type":"web_search_call","status":"in_progress"}}`,
		} {
			frame, err := compiled.DecodeFrame(t.Context(), testValue(t, body), options)
			if err != nil {
				t.Fatal(err)
			}
			wire, err := compiled.EncodeFrame(t.Context(), frame, options)
			if err != nil {
				t.Fatal(err)
			}
			consume(wire)
		}
	}
	start(options, consume)
	input := testValue(t, `{"type":"response.web_search_call.searching","sequence_number":7,"item_id":"search","output_index":0,"vendor":false}`)
	frame, err := compiled.DecodeFrame(t.Context(), input, options)
	if err != nil {
		t.Fatal(err)
	}
	output, err := compiled.EncodeFrame(t.Context(), frame, options)
	if err != nil || len(output) != 1 {
		t.Fatal(output, err)
	}
	consume(output)
	sameJSON(t, output[0].Bytes(), string(input.Bytes()))
	if _, err := compiled.DecodeFrame(t.Context(), input, options); err == nil {
		t.Fatal("repeated provider sequence accepted")
	}
	fresh, consumeFresh := bindFixtureOutput(t, compiled, fixtureOptions(p.EvaluationContext{State: p.NewEvaluationState()}))
	start(fresh, consumeFresh)
	if _, err := compiled.DecodeFrame(t.Context(), input, fresh); err != nil {
		t.Fatal("codec state leaked across requests", err)
	}
}

func TestBuiltinStreamingTextDoesNotAccumulateAtIncrementalTargets(t *testing.T) {
	t.Parallel()
	// Exceed a small configured buffer rather than sending tens of MiB just to
	// prove that incremental output is not retained against that buffer.
	limits := p.DefaultLimits()
	limits.BufferBytes = 64 * 1024
	for _, target := range []string{Chat, Anthropic, Gemini} {
		t.Run(target, func(t *testing.T) {
			compiled := testCompiledWithLimits(t, target, limits)
			options := fixtureOptions(p.EvaluationContext{State: p.NewEvaluationState()})
			events := []p.Event{{SchemaVersion: p.ProtocolCoreVersion, Type: p.ResponseStarted}, {SchemaVersion: p.ProtocolCoreVersion, Type: p.ItemStarted, ItemID: p.StringValue("text"), Item: &p.Node{Kind: p.TextNode}}}
			for _, event := range events {
				if _, err := compiled.EncodeFrames(t.Context(), event, options); err != nil {
					t.Fatal(err)
				}
			}
			delta := p.StringValue(strings.Repeat("x", 4096))
			for range 2 * limits.BufferBytes / 4096 {
				if _, err := compiled.EncodeFrames(t.Context(), p.Event{SchemaVersion: p.ProtocolCoreVersion, Type: p.ItemDelta, ItemID: p.StringValue("text"), Delta: delta}, options); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := compiled.EncodeFrames(t.Context(), p.Event{SchemaVersion: p.ProtocolCoreVersion, Type: p.ResponseFinished, Response: &p.Response{SchemaVersion: p.ProtocolCoreVersion, Status: p.StringValue("completed"), Attributes: p.Object{"finishReason": p.StringValue("stop")}}}, options); err != nil {
				t.Fatal(err)
			}
			if _, err := compiled.FinishEvents(t.Context(), options); err != nil {
				t.Fatal(err)
			}
		})
	}
}

var textStreams = map[string][]string{
	Chat:      {`{"id":"r","model":"m","choices":[{"index":0,"delta":{"role":"assistant","content":"hi"},"finish_reason":null}],"object":"chat.completion.chunk","created":1}`, `{"id":"r","model":"m","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"object":"chat.completion.chunk","created":1}`, `{"id":"r","choices":[],"usage":{"prompt_tokens":20,"completion_tokens":2,"total_tokens":22,"prompt_tokens_details":{"cached_tokens":15}},"object":"chat.completion.chunk","created":1,"model":"m"}`},
	Anthropic: {`{"type":"message_start","message":{"id":"r","model":"m","content":[],"usage":{"input_tokens":5,"cache_read_input_tokens":15,"output_tokens":0},"type":"message","role":"assistant","stop_reason":null,"stop_sequence":null}}`, `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`, `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi"}}`, `{"type":"content_block_stop","index":0}`, `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":2}}`, `{"type":"message_stop"}`},
	Responses: {`{"type":"response.created","response":{"id":"r","model":"m","output":[],"status":"in_progress","created_at":1,"object":"response"},"sequence_number":0}`, `{"type":"response.output_item.added","output_index":0,"item":{"type":"message","role":"assistant","content":[],"id":"r_item_0","status":"in_progress"},"sequence_number":1}`, `{"type":"response.content_part.added","output_index":0,"content_index":0,"part":{"type":"output_text","text":""},"item_id":"r_item_0","sequence_number":2}`, `{"type":"response.output_text.delta","output_index":0,"content_index":0,"delta":"hi","item_id":"r_item_0","sequence_number":3}`, `{"type":"response.output_text.done","output_index":0,"content_index":0,"text":"hi","item_id":"r_item_0","sequence_number":4}`, `{"type":"response.content_part.done","output_index":0,"content_index":0,"part":{"type":"output_text","text":"hi"},"item_id":"r_item_0","sequence_number":5}`, `{"type":"response.output_item.done","output_index":0,"item":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hi"}],"id":"r_item_0","status":"completed"},"sequence_number":6}`, `{"type":"response.completed","response":{"id":"r","model":"m","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hi"}],"id":"r_item_0","status":"completed"}],"usage":{"input_tokens":20,"output_tokens":2,"total_tokens":22,"input_tokens_details":{"cached_tokens":15}},"created_at":1,"object":"response"},"sequence_number":7}`},
	Gemini:    {`{"responseId":"r","modelVersion":"m","candidates":[{"index":0,"content":{"role":"model","parts":[{"text":"hi"}]}}]}`, `{"responseId":"r","modelVersion":"m","candidates":[{"index":0,"content":{"role":"model","parts":[]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":20,"candidatesTokenCount":2,"totalTokenCount":22,"cachedContentTokenCount":15}}`},
}

var toolStreams = map[string][]string{
	Chat:      {`{"id":"r","model":"m","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"type":"function","id":"call","function":{"name":"lookup","arguments":""}}],"role":"assistant"}}],"object":"chat.completion.chunk","created":1}`, `{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"n\":9007199254740993}"}}]}}],"object":"chat.completion.chunk","created":1,"id":"r","model":"m"}`, `{"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}],"object":"chat.completion.chunk","created":1,"id":"r","model":"m"}`},
	Anthropic: {`{"type":"message_start","message":{"id":"r","model":"m","content":[],"type":"message","role":"assistant","stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":0,"output_tokens":0}}}`, `{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"call","name":"lookup","input":{}}}`, `{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"n\":9007199254740993}"}}`, `{"type":"content_block_stop","index":0}`, `{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":0}}`, `{"type":"message_stop"}`},
	Responses: {`{"type":"response.created","response":{"id":"r","model":"m","output":[],"status":"in_progress","created_at":1,"object":"response"}}`, `{"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","call_id":"call","name":"lookup","arguments":"","id":"r_item_0"}}`, `{"type":"response.function_call_arguments.delta","output_index":0,"delta":"{\"n\":9007199254740993}","item_id":"r_item_0"}`, `{"type":"response.function_call_arguments.done","output_index":0,"arguments":"{\"n\":9007199254740993}","item_id":"r_item_0"}`, `{"type":"response.output_item.done","output_index":0,"item":{"type":"function_call","call_id":"call","name":"lookup","arguments":"{\"n\":9007199254740993}","id":"r_item_0"}}`, `{"type":"response.completed","response":{"id":"r","model":"m","status":"completed","output":[{"type":"function_call","call_id":"call","name":"lookup","arguments":"{\"n\":9007199254740993}","id":"r_item_0"}],"created_at":1,"object":"response"}}`},
	Gemini:    {`{"responseId":"r","modelVersion":"m","candidates":[{"index":0,"content":{"role":"model","parts":[{"functionCall":{"id":"call","name":"lookup","args":{"n":9007199254740993}}}]},"finishReason":"STOP"}]}`},
}

func TestBuiltinStreamToolMatrix(t *testing.T) {
	t.Parallel()
	for _, source := range packageFixtures {
		for _, target := range packageFixtures {
			t.Run(source.name+"-"+target.name, func(t *testing.T) {
				frames := convertStream(t, source.name, target.name, toolStreams[source.name])
				compiled := testCompiled(t, target.name)
				options := fixtureOptions(p.EvaluationContext{State: p.NewEvaluationState()})
				collector, err := p.NewResponseCollector(compiled.Target(p.EncodeEvent, p.Scope{}), p.DefaultLimits())
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
				calls := fixtureNodes(response.Content, p.ToolCallNode)
				if len(calls) != 1 {
					t.Fatalf("tool count: %d", len(calls))
				}
				node := calls[0]
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
	t.Parallel()
	for _, source := range packageFixtures {
		for _, target := range packageFixtures {
			t.Run(source.name+"-"+target.name, func(t *testing.T) {
				to := testCompiled(t, target.name)
				wire := convertStream(t, source.name, target.name, textStreams[source.name])
				if source.name == target.name {
					if len(wire) != len(textStreams[source.name]) {
						t.Fatal("native frames replayed more than once")
					}
					for index, body := range wire {
						sameJSON(t, body.Bytes(), textStreams[source.name][index])
					}
				}
				collector, err := p.NewResponseCollector(to.Target(p.EncodeEvent, p.Scope{}), p.DefaultLimits())
				if err != nil {
					t.Fatal(err)
				}
				decodeOptions := fixtureOptions(p.EvaluationContext{State: p.NewEvaluationState()})
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

func anthropicSignatureFrames() []string {
	return []string{
		`{"type":"message_start","message":{"id":"r","model":"m","type":"message","role":"assistant","content":[]}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"reason"}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"first-"}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"second"}}`,
		`{"type":"content_block_stop","index":0}`,
		`{"type":"message_delta","delta":{"stop_reason":"end_turn"}}`,
		`{"type":"message_stop"}`,
	}
}
