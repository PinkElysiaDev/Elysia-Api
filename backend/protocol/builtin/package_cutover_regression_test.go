package builtin

import (
	"fmt"
	"math"
	"strings"
	"testing"

	p "github.com/elysia-api/backend/protocol"
)

func TestChatPackageInferredUsageTotal(t *testing.T) {
	compiled := compileBuiltinPackage(t, "openai-chat")
	for _, fixture := range []struct {
		name, input, output string
		total               int64
		hasOverflow         bool
	}{
		{"small", "2", "3", 5, false},
		{"limit", "9223372036854775806", "1", math.MaxInt64, false},
		{"overflow", "9223372036854775807", "1", 0, true},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			options := fixtureOptions(p.EvaluationContext{State: p.NewEvaluationState()})
			defer options.State.Release()
			validation, err := compiled.NewWireInputStreamValidation(p.Scope{})
			if err != nil {
				t.Fatal(err)
			}
			body := fmt.Sprintf(`{"id":"r","model":"m","created":1,"object":"chat.completion.chunk","choices":[{"index":0,"delta":{"role":"assistant","content":"last"},"finish_reason":"stop"}],"usage":{"prompt_tokens":%s,"completion_tokens":%s}}`, fixture.input, fixture.output)
			frame, err := validation.DecodeFrame(t.Context(), testValue(t, body), options)
			if fixture.hasOverflow {
				if err == nil || !strings.Contains(err.Error(), "overflow") {
					t.Fatal("overflow accepted", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			hasTotal := false
			for _, event := range frame.Events {
				if event.Type == p.UsageUpdated && event.Usage != nil {
					if event.Usage.Total == nil || event.Usage.Total.Count != fixture.total || event.Usage.Total.Origin != p.InferredCount {
						t.Fatal("inferred total lost integer precision or provenance", event.Usage.Total)
					}
					hasTotal = true
				}
			}
			if !hasTotal {
				t.Fatal("missing inferred usage", frame.Events)
			}
			if err := validation.Finish(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestChatPackageStartsImplicitCoreStream(t *testing.T) {
	compiled := compileBuiltinPackage(t, "openai-chat")
	for _, hasContent := range []bool{false, true} {
		t.Run(fmt.Sprint(hasContent), func(t *testing.T) {
			options := fixtureOptions(p.EvaluationContext{Scope: p.Scope{Model: "m"}, State: p.NewEvaluationState()})
			defer options.State.Release()
			validation, err := compiled.NewWireStreamValidation(options.Scope)
			if err != nil {
				t.Fatal(err)
			}
			options = options.WithOutputStream(validation)
			var events []p.Event
			if hasContent {
				events = append(events,
					p.Event{Type: p.ItemStarted, ItemID: p.StringValue("text"), Item: &p.Node{Kind: p.TextNode, Payload: p.StringValue("hello")}},
					p.Event{Type: p.ItemFinished, ItemID: p.StringValue("text")})
			}
			events = append(events, p.Event{Type: p.ResponseFinished})
			var output []p.Value
			consume := func(frames []p.Value) {
				t.Helper()
				for _, frame := range frames {
					if err := validation.Consume(t.Context(), frame); err != nil {
						t.Fatal(frame.JSONString(), err)
					}
					if err := validation.CommitDelivery(); err != nil {
						t.Fatal(err)
					}
					output = append(output, frame)
				}
			}
			for _, event := range events {
				event.SchemaVersion = p.ProtocolCoreVersion
				frames, err := compiled.EncodeFrames(t.Context(), event, options)
				if err != nil {
					t.Fatal(err)
				}
				consume(frames)
			}
			frames, err := compiled.FinishEvents(t.Context(), options)
			if err != nil {
				t.Fatal(err)
			}
			consume(frames)
			if err := validation.Finish(); err != nil {
				t.Fatal(err)
			}
			if len(output) < 2 || !strings.Contains(output[0].JSONString(), `"role":"assistant"`) {
				t.Fatal("missing start or terminal", output)
			}
			starts := 0
			for _, frame := range output {
				var wire struct {
					ID, Model string
					Created   int64
				}
				if err := frame.Decode(&wire); err != nil {
					t.Fatal(err)
				}
				if wire.ID != fixtureOperationID || wire.Model != "m" || wire.Created != fixtureStartedAt {
					t.Fatal("unstable execution identity", frame)
				}
				if strings.Contains(frame.JSONString(), `"role":"assistant"`) {
					starts++
				}
			}
			if starts != 1 {
				t.Fatal("repeated stream opening", starts)
			}
		})
	}
}

func TestChatPackageRebuildsRolelessOpening(t *testing.T) {
	compiled := compileBuiltinPackage(t, "openai-chat")
	output := convertFixtureStream(t, compiled, compiled, []string{
		`{"id":"r","model":"m","created":1,"object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"hello"}}]}`,
		`{"id":"r","model":"m","created":1,"object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":" world"},"finish_reason":"stop"}]}`,
	})
	if len(output) < 3 || !strings.Contains(output[0].JSONString(), `"role":"assistant"`) {
		t.Fatal("roleless opening was replayed without the target envelope", output)
	}
	var wire []string
	for _, frame := range output {
		wire = append(wire, frame.JSONString())
	}
	_, response := decodePackageStream(t, compiled, wire, p.Scope{})
	text := fixtureNodes(response.Content, p.TextNode)
	if len(text) != 1 || !text[0].Payload.IsString("hello world") {
		t.Fatal("reconstructed opening changed text", response)
	}
}

func TestAnthropicPackageSourceFailureLifecycle(t *testing.T) {
	compiled := compileBuiltinPackage(t, "anthropic")
	for _, active := range []bool{false, true} {
		options := p.EvaluationContext{State: p.NewEvaluationState()}
		defer options.State.Release()
		validation, err := compiled.NewWireInputStreamValidation(p.Scope{})
		if err != nil {
			t.Fatal(err)
		}
		if active {
			for _, raw := range []string{`{"type":"message_start","message":{"id":"r","model":"m","content":[]}}`, `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`} {
				if _, err := validation.DecodeFrame(t.Context(), testValue(t, raw), options); err != nil {
					t.Fatal(err)
				}
			}
		}
		frame, err := validation.DecodeFrame(t.Context(), testValue(t, `{"type":"error","error":{"type":"overloaded_error","message":"retry later"}}`), options)
		if err != nil || len(frame.Events) != 1 || frame.Events[0].Type != p.OperationFailed || !strings.Contains(frame.Events[0].Error.JSONString(), `"message":"retry later"`) {
			t.Fatal("source failure lost", active, frame, err)
		}
		if err := validation.Finish(); err != nil {
			t.Fatal("error must terminate even with an open block", err)
		}
		if _, err := validation.DecodeFrame(t.Context(), testValue(t, `{"type":"message_stop"}`), options); err == nil {
			t.Fatal("success followed source failure")
		}
	}
}

func TestAnthropicPackageOptionalFinishReason(t *testing.T) {
	compiled := compileBuiltinPackage(t, "anthropic")
	for _, fixture := range []struct{ suffix, reason string }{
		{"", ""}, {`,"stop_reason":null`, "null"}, {`,"stop_reason":"end_turn"`, `"stop"`},
	} {
		body := `{"id":"r","model":"m","role":"assistant","content":[{"type":"text","text":"ok"}]` + fixture.suffix + `}`
		response, err := compiled.DecodeResponse(t.Context(), []byte(body), fixtureOptions(p.EvaluationContext{}))
		if err != nil || response.Attributes["finishReason"].JSONString() != fixture.reason {
			t.Fatal("optional finish reason changed", fixture, response, err)
		}
		response.Native = nil
		encoded, err := compiled.EncodeResponse(t.Context(), response, fixtureOptions(p.EvaluationContext{}))
		if err != nil {
			t.Fatal(err)
		}
		fields, err := testValue(t, string(encoded)).ReadObject()
		if err != nil {
			t.Fatal(err)
		}
		want := fixture.reason
		if want == `"stop"` {
			want = `"end_turn"`
		}
		if fields["stop_reason"].JSONString() != want {
			t.Fatal("target changed absence or null", string(encoded))
		}
	}
}

func TestResponsesPackageNestedToolDisposition(t *testing.T) {
	checkResponsesNestedOutputDisposition(t, Anthropic, toolStreams[Anthropic], p.ToolCallNode)
}

func TestResponsesPackageNestedReasoningDisposition(t *testing.T) {
	checkResponsesNestedOutputDisposition(t, Chat, []string{
		`{"id":"r","model":"m","created":1,"object":"chat.completion.chunk","choices":[{"index":0,"delta":{"role":"assistant","reasoning_content":"visible thought"}}]}`,
		`{"id":"r","model":"m","created":1,"object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
	}, p.ReasoningNode)
}

func TestResponsesStreamLayoutKeepsInterleavedLeafOrder(t *testing.T) {
	checkResponsesNestedOutputDisposition(t, Anthropic, []string{
		`{"type":"message_start","message":{"id":"r","model":"m","type":"message","role":"assistant","content":[],"usage":{"input_tokens":1,"output_tokens":0}}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":"before"}}`,
		`{"type":"content_block_stop","index":0}`,
		`{"type":"content_block_start","index":1,"content_block":{"type":"thinking","thinking":"visible thought"}}`,
		`{"type":"content_block_stop","index":1}`,
		`{"type":"content_block_start","index":2,"content_block":{"type":"tool_use","id":"call","name":"lookup","input":{"n":9007199254740993}}}`,
		`{"type":"content_block_stop","index":2}`,
		`{"type":"content_block_start","index":3,"content_block":{"type":"text","text":"after"}}`,
		`{"type":"content_block_stop","index":3}`,
		`{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":2}}`,
		`{"type":"message_stop"}`,
	}, p.ToolCallNode, func(t *testing.T, response *p.Response) {
		if len(response.Content) != 5 || response.Content[0].Kind != p.MessageNode || len(response.Content[0].Children) != 0 {
			t.Fatal("observed parent container disappeared", response.Content)
		}
		if !response.Content[1].Children[0].Payload.IsString("before") || response.Content[2].Kind != p.ReasoningNode || !response.Content[2].Payload.IsString("visible thought") || response.Content[3].Kind != p.ToolCallNode || !response.Content[4].Children[0].Payload.IsString("after") {
			t.Fatal("target reordered interleaved text, reasoning and tools", response.Content)
		}
		if response.Content[1].ID == response.Content[4].ID {
			t.Fatal("independent text wrappers reused a target identity")
		}
	})
}

func checkResponsesNestedOutputDisposition(t *testing.T, source string, wire []string, kind p.NodeKind, checks ...func(*testing.T, *p.Response)) {
	t.Helper()
	from, to := compileBuiltinPackage(t, packageName(source)), compileBuiltinPackage(t, "openai-responses")
	frames, _ := decodePackageStream(t, from, wire, p.Scope{})
	for _, mode := range []string{"compatible", "strict", "disabled"} {
		t.Run(mode, func(t *testing.T) {
			policy := p.DefaultConversionPolicy(to, from)
			if mode == "strict" {
				policy.Mode = mode
			}
			for i := range policy.Rules {
				if policy.Rules[i].ID == "event-output-layout" {
					policy.Rules[i].Enabled = mode != "disabled"
				}
			}
			conversion, err := p.ResolveConversion(policy)
			if err != nil {
				t.Fatal(err)
			}
			options := fixtureOptions(p.EvaluationContext{State: p.NewEvaluationState(), Diagnostics: &p.DiagnosticSink{}})
			defer options.State.Release()
			route := fixtureConversionContext(t, conversion, p.ConversionEvent, from.Identity())
			validation, err := to.NewWireStreamValidation(p.Scope{})
			if err != nil {
				t.Fatal(err)
			}
			options = options.WithConversion(conversion, route).WithOutputStream(validation)
			var output []string
			consume := func(encoded []p.Value) {
				for _, value := range encoded {
					if err := validation.Consume(t.Context(), value); err != nil {
						t.Fatal(value, err)
					}
					output = append(output, value.JSONString())
					if err := validation.CommitDelivery(); err != nil {
						t.Fatal(err)
					}
				}
			}
			for _, frame := range frames {
				var events []p.Event
				for _, event := range frame.Events {
					projected, conversionErr := conversion.Event(t.Context(), event, route, options.Diagnostics)
					if conversionErr != nil {
						t.Fatal(conversionErr)
					}
					events = append(events, projected)
				}
				var encoded []p.Value
				encoded, err = to.EncodeFrame(t.Context(), &p.EventFrame{Events: events}, options)
				if err != nil {
					break
				}
				consume(encoded)
			}
			if mode != "compatible" {
				if err == nil || !strings.Contains(err.Error(), "/parentId") {
					t.Fatal("parent grouping loss bypassed policy", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			tail, err := to.FinishEvents(t.Context(), options)
			if err != nil {
				t.Fatal(err)
			}
			consume(tail)
			if err := validation.Finish(); err != nil {
				t.Fatal(err)
			}
			_, response := decodePackageStream(t, to, output, p.Scope{})
			for _, check := range checks {
				check(t, response)
			}
			nodes := fixtureNodes(response.Content, kind)
			if len(nodes) != 1 {
				t.Fatal("standalone output identity changed", nodes)
			}
			if kind == p.ToolCallNode && (!nodes[0].CallID.IsString("call") || !nodes[0].Name.IsString("lookup") || nodes[0].Input.Value.JSONString() != `{"n":9007199254740993}`) {
				t.Fatal("tool association lost", nodes)
			}
			if kind == p.ReasoningNode && !nodes[0].Payload.IsString("visible thought") {
				t.Fatal("visible reasoning changed", nodes)
			}
			hasDisposition := false
			for _, issue := range options.Diagnostics.Issues() {
				if issue.RuleID == "event-output-layout" && issue.Fidelity == "lossy_compatible" {
					hasDisposition = true
				}
			}
			if !hasDisposition {
				t.Fatal("parent grouping changed without a declared disposition")
			}
		})
	}
}
