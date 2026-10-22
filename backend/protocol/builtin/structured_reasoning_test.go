package builtin

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func structuredReasoningSequence() []string {
	return []string{
		`{"type":"response.output_item.added","output_index":0,"item":{"type":"reasoning","id":"rs","status":"in_progress","summary":[]}}`,
		`{"type":"response.content_part.added","output_index":0,"content_index":0,"part":{"type":"reasoning_text","text":""}}`,
		`{"type":"response.reasoning_text.delta","output_index":0,"content_index":0,"delta":"first"}`,
		`{"type":"response.reasoning_summary_part.added","output_index":0,"summary_index":0,"part":{"type":"summary_text","text":""}}`,
		`{"type":"response.reasoning_summary_text.delta","output_index":0,"summary_index":0,"delta":"brief"}`,
		`{"type":"response.reasoning_text.done","output_index":0,"content_index":0,"text":"first"}`,
		`{"type":"response.content_part.done","output_index":0,"content_index":0,"part":{"type":"reasoning_text","text":"first"}}`,
		`{"type":"response.content_part.added","output_index":0,"content_index":1,"part":{"type":"reasoning_text","text":""}}`,
		`{"type":"response.reasoning_text.delta","output_index":0,"content_index":1,"delta":"second"}`,
		`{"type":"response.reasoning_text.done","output_index":0,"content_index":1,"text":"second"}`,
		`{"type":"response.content_part.done","output_index":0,"content_index":1,"part":{"type":"reasoning_text","text":"second"}}`,
		`{"type":"response.reasoning_summary_text.done","output_index":0,"summary_index":0,"text":"brief"}`,
		`{"type":"response.reasoning_summary_part.done","output_index":0,"summary_index":0,"part":{"type":"summary_text","text":"brief"}}`,
		`{"type":"response.output_item.done","output_index":0,"item":` + structuredReasoningFinal + `}`,
	}
}

const structuredReasoningFinal = `{"type":"reasoning","id":"rs","status":"completed","summary":[{"type":"summary_text","text":"brief"}],"content":[{"type":"reasoning_text","text":"first"},{"type":"reasoning_text","text":"second"}]}`

const nullableReasoningItem = `{"type":"reasoning","id":"rs-synthetic","summary":[],"content":null,"encrypted_content":"synthetic-encrypted-state"}`

func TestResponsesRequestReasoningContentPresence(t *testing.T) {
	compiled := testCompiled(t, Responses)
	options := p.EvaluationContext{Scope: p.Scope{Provider: "provider", Account: "account", Model: "m"}}
	for _, tc := range []struct {
		name, item string
		form       p.ReasoningForm
		text       string
		summaries  int
	}{
		{"missing", strings.Replace(nullableReasoningItem, `,"content":null`, "", 1), p.SummaryReasoning, "", 0},
		{"null", nullableReasoningItem, p.SummaryReasoning, "", 0},
		{"empty", strings.Replace(nullableReasoningItem, `"content":null`, `"content":[]`, 1), "", "", 0},
		{"visible", strings.Replace(nullableReasoningItem, `"content":null`, `"content":[{"type":"reasoning_text","text":"visible"}]`, 1), "", "visible", 0},
		{"summary", strings.Replace(nullableReasoningItem, `"summary":[]`, `"summary":[{"type":"summary_text","text":"brief"}]`, 1), p.SummaryReasoning, "", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := `{"model":"m","store":false,"input":[` + tc.item + `]}`
			request, err := compiled.DecodeRequest(t.Context(), []byte(body), options)
			if err != nil {
				t.Fatal(err)
			}
			node := request.Content[0]
			if node.ReasoningForm != tc.form || len(node.Children) != tc.summaries || len(node.Resources) != 1 || !node.Resources[0].ID.IsString("synthetic-encrypted-state") || node.Resources[0].Scope != options.Scope {
				t.Fatal("reasoning shape or encrypted resource changed")
			}
			if tc.text == "" && !node.Payload.IsZero() || tc.text != "" && !node.Payload.IsString(tc.text) {
				t.Fatal("visible reasoning was invented or lost")
			}
			before, _ := p.EncodeValue(request)
			for _, mode := range []string{"compatible", "strict"} {
				policy := p.DefaultConversionPolicy(compiled, compiled)
				policy.Mode = mode
				conversion, err := p.ResolveConversion(policy)
				if err != nil {
					t.Fatal(err)
				}
				converted, err := conversion.Request(t.Context(), request, p.ConversionContext{Source: compiled.Identity(), Target: compiled.Identity(), Scope: options.Scope}, nil)
				if err != nil {
					t.Fatal(mode, err)
				}
				for _, model := range []string{"m", "actual-model"} {
					converted.Model = p.StringValue(model)
					wire, err := compiled.EncodeRequest(t.Context(), converted, options)
					if err != nil {
						t.Fatal(err)
					}
					sameJSON(t, wire, strings.Replace(body, `"model":"m"`, `"model":"`+model+`"`, 1))
					if err := compiled.ValidateWireOutput(p.EncodeRequest, testValue(t, string(wire))); err != nil {
						t.Fatal("final request contract rejected retained history", err)
					}
				}
			}
			after, _ := p.EncodeValue(request)
			if before != after {
				t.Fatal("conversion changed the caller's request")
			}
		})
	}
}

func TestResponsesRequestNullReasoningReconstruction(t *testing.T) {
	compiled := testCompiled(t, Responses)
	options := p.EvaluationContext{Scope: p.Scope{Provider: "provider", Account: "account", Model: "m"}}
	const extension = `{"decimal":1.00,"integer":9007199254740993,"escaped":"\u0061\/\u003c\u003e\u0026"}`
	item := strings.TrimSuffix(nullableReasoningItem, "}") + `,"vendor":` + extension + `}`
	body := `{"model":"m","input":[` + item + `]}`
	request, err := compiled.DecodeRequest(t.Context(), []byte(body), options)
	if err != nil {
		t.Fatal(err)
	}
	for _, model := range []string{"m", "actual-model"} {
		request.Model = p.StringValue(model)
		wire, err := compiled.EncodeRequest(t.Context(), request, options)
		if err != nil {
			t.Fatal(err)
		}
		fields, err := testValue(t, string(wire)).ReadObject()
		if err != nil {
			t.Fatal(err)
		}
		items, err := readArray(fields["input"])
		if err != nil || len(items) != 1 {
			t.Fatal("history item changed", err)
		}
		fields, err = items[0].ReadObject()
		if err != nil || !fields["content"].IsNull() || fields["vendor"].JSONString() != extension {
			t.Fatalf("native null or unknown field spelling changed (model=%s): content=%s vendor=%q error=%v", model, fields["content"].JSONString(), fields["vendor"].JSONString(), err)
		}
	}
	request.Model = p.StringValue("m")
	// Removing root Native requests semantic reconstruction; absence stays nil,
	// while the actual encrypted state and item identity remain semantic fields.
	rebuilt := request.Clone()
	rebuilt.Native = nil
	wire, err := compiled.EncodeRequest(t.Context(), rebuilt, options)
	if err != nil {
		t.Fatal(err)
	}
	sameJSON(t, wire, strings.Replace(body, `,"content":null`, "", 1))
	request.Content[0].ReasoningForm = p.StructuredReasoning
	request.Content[0].ReasoningContent = []p.Node{{Kind: p.TextNode, Payload: p.StringValue("edited visible part")}}
	wire, err = compiled.EncodeRequest(t.Context(), request, options)
	if err != nil {
		t.Fatal(err)
	}
	sameJSON(t, wire, strings.Replace(body, `"content":null`, `"content":[{"type":"reasoning_text","text":"edited visible part"}]`, 1))
}

func TestResponsesRequestNullReasoningRetainsLegacyHTMLNormalization(t *testing.T) {
	compiled := testCompiled(t, Responses)
	options := p.EvaluationContext{Scope: p.Scope{Provider: "provider", Account: "account", Model: "m"}}
	const extension = `{"decimal":1.00,"integer":9007199254740993,"escaped":"\u0061\/<>&"}`
	const normalized = `{"decimal":1.00,"integer":9007199254740993,"escaped":"\u0061\/\u003c\u003e\u0026"}`
	for _, item := range []string{nullableReasoningItem, strings.Replace(nullableReasoningItem, `,"content":null`, "", 1)} {
		item = strings.TrimSuffix(item, "}") + `,"vendor":` + extension + `}`
		request, err := compiled.DecodeRequest(t.Context(), []byte(`{"model":"m","input":[`+item+`]}`), options)
		if err != nil {
			t.Fatal(err)
		}
		request.Model = p.StringValue("actual-model")
		wire, err := compiled.EncodeRequest(t.Context(), request, options)
		if err != nil {
			t.Fatal(err)
		}
		fields, _ := testValue(t, string(wire)).ReadObject()
		items, err := readArray(fields["input"])
		if err != nil || len(items) != 1 {
			t.Fatal("history item changed", err)
		}
		fields, _ = items[0].ReadObject()
		// Editing the root already applies the container encoder's HTML escapes;
		// accepting null must behave like the existing missing-content path.
		if fields["vendor"].JSONString() != normalized || request.Content[0].Native.Value.JSONString() != item {
			t.Fatal("HTML normalization or the original node snapshot changed")
		}
	}
}

func TestResponsesRequestReasoningRejectsInvalidShapes(t *testing.T) {
	compiled := testCompiled(t, Responses)
	for _, tc := range []struct{ name, field, value, path string }{
		{"summary-null", "summary", "null", "/input/0/summary"},
		{"content-string", "content", `"visible"`, "/input/0/content"},
		{"content-object", "content", `{}`, "/input/0/content"},
		{"content-number", "content", `42`, "/input/0/content"},
		{"content-bool", "content", `false`, "/input/0/content"},
		{"part-null", "content", `[null]`, "/input/0/content/0"},
		{"part-kind", "content", `[{"type":"summary_text","text":"wrong"}]`, "/input/0/content/0/type"},
		{"part-text", "content", `[{"type":"reasoning_text","text":null}]`, "/input/0/content/0/text"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			field := `"content":null`
			if tc.field == "summary" {
				field = `"summary":[]`
			}
			item := strings.Replace(nullableReasoningItem, field, `"`+tc.field+`":`+tc.value, 1)
			_, err := compiled.DecodeRequest(t.Context(), []byte(`{"model":"m","input":[`+item+`]}`), p.EvaluationContext{})
			var conversion *p.ConversionError
			if !errors.As(err, &conversion) || len(conversion.Issues) != 1 {
				t.Fatal("missing structured failure", err)
			}
			issue := conversion.Issues[0]
			if issue.Code != p.InvalidInput || issue.Path != tc.path || issue.Direction != p.DecodeRequest || issue.Protocol != compiled.Identity() {
				t.Fatal(issue)
			}
			if !strings.HasPrefix(tc.value, "[") && issue.Stage != string(p.DecodeRequest) {
				t.Fatal("array failure lost its request decode stage", issue)
			}
		})
	}
}

func TestResponsesNullReasoningRemainsRequestOnly(t *testing.T) {
	compiled := testCompiled(t, Responses)
	_, err := compiled.DecodeResponse(t.Context(), []byte(`{"id":"r","model":"m","status":"completed","output":[`+nullableReasoningItem+`]}`), p.EvaluationContext{})
	if err == nil || !strings.Contains(err.Error(), "/output/0/content") {
		t.Fatal("response null content became accepted", err)
	}
	for _, kind := range []string{"response.output_item.added", "response.output_item.done"} {
		t.Run(kind, func(t *testing.T) {
			options := p.EvaluationContext{State: p.NewEvaluationState(), Scope: p.Scope{Provider: "provider", Account: "account", Model: "m"}}
			if _, err := compiled.DecodeFrame(t.Context(), testValue(t, `{"type":"response.created","response":{"id":"r","object":"response","model":"m","status":"in_progress","created_at":1,"output":[]}}`), options); err != nil {
				t.Fatal(err)
			}
			if kind == "response.output_item.done" {
				initial := strings.Replace(nullableReasoningItem, `,"content":null`, "", 1)
				if _, err := compiled.DecodeFrame(t.Context(), testValue(t, `{"type":"response.output_item.added","output_index":0,"item":`+initial+`}`), options); err != nil {
					t.Fatal(err)
				}
			}
			_, err := compiled.DecodeFrame(t.Context(), testValue(t, `{"type":"`+kind+`","output_index":0,"item":`+nullableReasoningItem+`}`), options)
			var conversion *p.ConversionError
			if !errors.As(err, &conversion) || len(conversion.Issues) == 0 || conversion.Issues[0].Path != "/item/content" || conversion.Issues[0].Stage == string(p.DecodeRequest) {
				t.Fatal("stream null content changed validation or ownership", err)
			}
		})
	}
}

func TestResponsesRequestNullReasoningMatchesMissingAcrossProtocols(t *testing.T) {
	from := testCompiled(t, Responses)
	options := p.EvaluationContext{Scope: p.Scope{Provider: "provider", Account: "account", Model: "m"}}
	for _, codec := range []string{Chat, Anthropic, Gemini} {
		for _, mode := range []string{"compatible", "strict"} {
			t.Run(codec+"/"+mode, func(t *testing.T) {
				to := testCompiled(t, codec)
				policy := p.DefaultConversionPolicy(from, to)
				policy.Mode = mode
				conversion, err := p.ResolveConversion(policy)
				if err != nil {
					t.Fatal(err)
				}
				type observation struct {
					wire, failure string
					issues        []p.ConversionIssue
				}
				observe := func(item string) observation {
					request, err := from.DecodeRequest(t.Context(), []byte(`{"model":"m","store":false,"input":[`+item+`]}`), options)
					if err != nil {
						t.Fatal(err)
					}
					sink := &p.DiagnosticSink{}
					projected, err := conversion.Request(t.Context(), request, p.ConversionContext{Source: from.Identity(), Target: to.Identity(), Scope: options.Scope}, sink)
					var wire []byte
					if err == nil {
						wire, err = to.EncodeRequest(t.Context(), projected, options)
					}
					return observation{string(wire), fmt.Sprint(err), sink.Issues()}
				}
				missing := observe(strings.Replace(nullableReasoningItem, `,"content":null`, "", 1))
				null := observe(nullableReasoningItem)
				if !reflect.DeepEqual(missing, null) || null.failure == "<nil>" {
					t.Fatalf("null history changed existing encrypted-summary protection: missing=%+v null=%+v", missing, null)
				}
			})
		}
	}
}

func TestStructuredReasoningJSONEditsKeepIndependentParts(t *testing.T) {
	c := shippedProjectionProtocol(t, Responses)
	r, err := c.DecodeRequest(t.Context(), []byte(`{"model":"m","input":[`+structuredReasoningFinal+`]}`), p.EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	node := &r.Content[0]
	if node.ReasoningForm != p.StructuredReasoning || len(node.Children) != 1 || len(node.ReasoningContent) != 2 || !node.Payload.IsZero() {
		t.Fatal(node)
	}
	cloned := r.Clone()
	cloned.Content[0].ReasoningContent[1].Payload = p.StringValue("changed")
	if r.Content[0].ReasoningContent[1].Payload == p.StringValue("changed") {
		t.Fatal("reasoning parts aliased source")
	}
	cloned.Native = nil
	wire, err := c.EncodeRequest(t.Context(), cloned, p.EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	fields, _ := testValue(t, string(wire)).ReadObject()
	items, _ := readArray(fields["input"])
	sameJSON(t, items[0].Bytes(), strings.Replace(structuredReasoningFinal, `"second"`, `"changed"`, 1))
	if p.ContinuationNodeDigest(r.Content[0]) == p.ContinuationNodeDigest(cloned.Content[0]) {
		t.Fatal("part edit did not change recovery digest")
	}
}

func TestStructuredReasoningSSESeparatesSummaryAndVisibleParts(t *testing.T) {
	c := shippedProjectionProtocol(t, Responses)
	options := p.EvaluationContext{State: p.NewEvaluationState()}
	collector, err := p.NewResponseCollector(p.Target{Protocol: c.Identity(), Direction: p.EncodeEvent, Capabilities: c.Capabilities(p.EncodeEvent)}, p.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	frames := append([]string{`{"type":"response.created","response":{"id":"r","object":"response","model":"m","status":"in_progress","created_at":1,"output":[]}}`}, structuredReasoningSequence()...)
	frames = append(frames, `{"type":"response.completed","response":{"id":"r","object":"response","model":"m","status":"completed","created_at":1,"output":[`+structuredReasoningFinal+`]}}`)
	var events []p.Event
	for _, raw := range frames {
		frame, err := c.DecodeFrame(t.Context(), testValue(t, raw), options)
		if err != nil {
			t.Fatal(err, raw)
		}
		for _, event := range frame.Events {
			if event.Unmapped != nil {
				t.Fatal("structured reasoning became opaque")
			}
			if _, _, err := collector.Consume(event); err != nil {
				t.Fatal(err, raw)
			}
			events = append(events, event)
		}
	}
	response, err := collector.Finish()
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Content) != 1 || response.Content[0].ReasoningForm != p.StructuredReasoning || len(response.Content[0].ReasoningContent) != 2 {
		t.Fatal(response.Content)
	}
	encoder := p.EvaluationContext{State: p.NewEvaluationState()}
	var encoded []p.Value
	for _, event := range events {
		batch, err := c.EncodeFrames(t.Context(), event, encoder)
		if err != nil {
			t.Fatal(err)
		}
		encoded = append(encoded, batch...)
	}
	tail, err := c.FinishEvents(t.Context(), encoder)
	if err != nil {
		t.Fatal(err)
	}
	encoded = append(encoded, tail...)
	starts := map[int]int{}
	for _, frame := range encoded {
		if err := c.ValidateWireOutput(p.EncodeEvent, frame); err != nil {
			t.Fatal(err, string(frame.Bytes()))
		}
		fields, _ := frame.ReadObject()
		if fields["type"] == p.StringValue("response.content_part.added") {
			i, _ := frameIndex(fields["content_index"])
			starts[i]++
		}
	}
	if starts[0] != 1 || starts[1] != 1 || len(starts) != 2 {
		t.Fatal(starts)
	}
	final, _ := encoded[len(encoded)-1].ReadObject()
	body, _ := final["response"].ReadObject()
	items, _ := readArray(body["output"])
	sameJSON(t, items[0].Bytes(), structuredReasoningFinal)
	// Re-consume rebuilt frames. JSON and cumulative streaming now agree.
	options = p.EvaluationContext{State: p.NewEvaluationState()}
	collector, _ = p.NewResponseCollector(p.Target{Protocol: c.Identity(), Direction: p.EncodeEvent, Capabilities: c.Capabilities(p.EncodeEvent)}, p.DefaultLimits())
	for _, wire := range encoded {
		frame, err := c.DecodeFrame(t.Context(), wire, options)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range frame.Events {
			if _, _, err := collector.Consume(e); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := collector.Finish(); err != nil {
		t.Fatal(err)
	}
}

func TestStructuredReasoningRejectsInvalidPartsAndFinalRewrite(t *testing.T) {
	c := shippedProjectionProtocol(t, Responses)
	for _, part := range []string{`42`, `{"type":"summary_text","text":"wrong"}`, `{"type":"reasoning_text","text":null}`, `{"type":"reasoning_text","text":42}`} {
		_, err := c.DecodeRequest(t.Context(), []byte(`{"model":"m","input":[{"type":"reasoning","summary":[],"content":[`+part+`]}]}`), p.EvaluationContext{})
		if err == nil || !strings.Contains(err.Error(), "/input/0/content/0") {
			t.Fatal(err)
		}
	}
	for _, replacement := range []string{`"rewritten"`, `"first with suffix"`} {
		options := p.EvaluationContext{State: p.NewEvaluationState()}
		frames := structuredReasoningSequence()
		frames[len(frames)-1] = strings.Replace(frames[len(frames)-1], `"first"`, replacement, 1)
		for i, raw := range frames {
			_, err := c.DecodeFrame(t.Context(), testValue(t, raw), options)
			if i == len(frames)-1 {
				if err == nil {
					t.Fatal("finished part rewritten")
				}
			} else if err != nil {
				t.Fatal(fmt.Sprint(i), err)
			}
		}
	}
}

func TestStructuredSingleVisiblePartProjectsToOtherCodecs(t *testing.T) {
	from := shippedProjectionProtocol(t, Responses)
	for _, codec := range []string{Chat, Anthropic, Gemini} {
		for _, initialContent := range []string{"", `,"content":[]`} {
			t.Run(codec+initialContent, func(t *testing.T) {
				to := shippedProjectionProtocol(t, codec)
				conversion, _ := p.ResolveConversion(p.DefaultConversionPolicy(to, from))
				route := p.ConversionContext{Source: from.Identity(), Target: to.Identity(), Model: "m", Delivery: &p.DeliveryState{ID: p.StringValue("r"), Created: testValue(t, "1")}}
				decodeOptions := p.EvaluationContext{State: p.NewEvaluationState()}
				encodeOptions := p.EvaluationContext{State: p.NewEvaluationState()}
				final := `{"type":"reasoning","id":"rs","status":"completed","summary":[],"content":[{"type":"reasoning_text","text":"full thought"}]}`
				frames := []string{
					`{"type":"response.created","response":{"id":"r","model":"m","status":"in_progress","output":[]}}`,
					`{"type":"response.output_item.added","output_index":0,"item":{"type":"reasoning","id":"rs","status":"in_progress","summary":[]` + initialContent + `}}`,
					`{"type":"response.content_part.added","output_index":0,"content_index":0,"part":{"type":"reasoning_text","text":""}}`,
					`{"type":"response.reasoning_text.delta","output_index":0,"content_index":0,"delta":"full thought"}`,
					`{"type":"response.reasoning_text.done","output_index":0,"content_index":0,"text":"full thought"}`,
					`{"type":"response.content_part.done","output_index":0,"content_index":0,"part":{"type":"reasoning_text","text":"full thought"}}`,
					`{"type":"response.output_item.done","output_index":0,"item":` + final + `}`,
					`{"type":"response.completed","response":{"id":"r","model":"m","status":"completed","output":[` + final + `],"usage":{"input_tokens":2,"output_tokens":1,"total_tokens":3}}}`,
				}
				var text strings.Builder
				for _, raw := range frames {
					frame, err := from.DecodeFrame(t.Context(), testValue(t, raw), decodeOptions)
					if err != nil {
						t.Fatal(err)
					}
					for _, event := range frame.Events {
						e, err := conversion.Event(t.Context(), event, route, nil)
						if err != nil {
							t.Fatal(err)
						}
						batch, err := to.EncodeFrames(t.Context(), e, encodeOptions)
						if err != nil {
							t.Fatal(err)
						}
						for _, wire := range batch {
							if err := to.ValidateWireOutput(p.EncodeEvent, wire); err != nil {
								t.Fatal(err)
							}
							text.Write(wire.Bytes())
						}
					}
				}
				tail, err := to.FinishEvents(t.Context(), encodeOptions)
				if err != nil {
					t.Fatal(err)
				}
				for _, wire := range tail {
					if err := to.ValidateWireOutput(p.EncodeEvent, wire); err != nil {
						t.Fatal(err)
					}
					text.Write(wire.Bytes())
				}
				if strings.Count(text.String(), "full thought") != 1 {
					t.Fatal("visible text lost or duplicated", text.String())
				}
			})
		}
	}
}

func TestUnprojectedStructuredReasoningCannotDisappearInForeignCodecs(t *testing.T) {
	from := shippedProjectionProtocol(t, Responses)
	for _, item := range []string{
		structuredReasoningFinal,
		`{"type":"reasoning","summary":[],"content":[{"type":"reasoning_text","text":"first"},{"type":"reasoning_text","text":"second"}]}`,
		`{"type":"reasoning","summary":[],"content":[{"type":"reasoning_text","text":"first","vendor_detail":true}]}`,
		`{"type":"reasoning","summary":[]}`,
	} {
		for _, codec := range []string{Chat, Anthropic, Gemini} {
			t.Run(codec+item, func(t *testing.T) {
				to := shippedProjectionProtocol(t, codec)
				r, err := from.DecodeResponse(t.Context(), []byte(`{"id":"r","model":"m","status":"completed","output":[`+item+`]}`), p.EvaluationContext{})
				if err != nil {
					t.Fatal(err)
				}
				conversion, _ := p.ResolveConversion(p.DefaultConversionPolicy(to, from))
				projected, err := conversion.Response(t.Context(), r, p.ConversionContext{Source: from.Identity(), Target: to.Identity()}, nil)
				if err != nil {
					t.Fatal(err)
				}
				_, err = to.EncodeResponse(t.Context(), projected, p.EvaluationContext{})
				if err == nil || !strings.Contains(err.Error(), "/reasoningForm") {
					t.Fatalf("unmapped structured response was not rejected: %v", err)
				}
				request := &p.Request{SchemaVersion: 1, Model: p.StringValue("m"), Content: projected.Content}
				if _, err := to.EncodeRequest(t.Context(), request, p.EvaluationContext{}); err == nil || !strings.Contains(err.Error(), "/reasoningForm") {
					t.Fatalf("unmapped structured history was not rejected: %v", err)
				}
				// Direct event encoding must retain the same guard even if the
				// caller disabled conversion or supplied its own semantic mapping.
				thought := projected.Content[0]
				if thought.Kind == p.MessageNode {
					thought = thought.Children[0]
				}
				if thought.Kind != p.ReasoningNode {
					t.Fatal("projection lost reasoning node")
				}
				event := p.Event{SchemaVersion: 1, Type: p.ItemStarted, ItemID: p.StringValue("thought"), Item: &thought}
				if _, err := to.EncodeFrames(t.Context(), event, p.EvaluationContext{State: p.NewEvaluationState()}); err == nil || !strings.Contains(err.Error(), "/reasoningForm") {
					t.Fatalf("unmapped structured event was not rejected: %v", err)
				}
			})
		}
	}
}
