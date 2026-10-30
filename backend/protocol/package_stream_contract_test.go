package protocol

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

const (
	packageToolStarted  EventType = "tool.started"
	packageToolDelta    EventType = "tool.delta"
	packageToolFinished EventType = "tool.finished"
)

func toolStreamPackage(t *testing.T) ProtocolPackage {
	t.Helper()
	document := eventProtocolPackage(t)
	document.Mapping.Capabilities[FunctionToolsCapability] = true
	document.Spec.Documents["provider-event"].Stream.Events[string(packageToolStarted)] = ProtocolStreamEventSpec{Action: "tool_start", Tool: &ProtocolStreamToolSpec{Name: "/name", CallID: "/call_id"}}
	document.Spec.Documents["provider-event"].Stream.Events[string(packageToolDelta)] = ProtocolStreamEventSpec{Action: "tool_delta", Arguments: "/arguments"}
	document.Spec.Documents["provider-event"].Stream.Events[string(packageToolFinished)] = ProtocolStreamEventSpec{Action: "tool_finish"}
	isTag := func(tag EventType) Expression {
		return Expression{Op: "equal", Items: []Expression{readExpression("input", "/kind"), literalExpression(tag)}}
	}
	decode := document.Mapping.Directions[DecodeEvent].Program.Fields
	decode["type"] = Expression{Op: "choose", Source: expressionPointer(readExpression("input", "/kind")), Cases: map[string]Expression{
		string(packageToolStarted): literalExpression(ItemStarted), string(packageToolDelta): literalExpression(ItemDelta), string(packageToolFinished): literalExpression(ItemFinished),
	}, Otherwise: expressionPointer(readExpression("input", "/kind"))}
	decode["delta"] = Expression{Op: "if", When: expressionPointer(isTag(packageToolDelta)), Then: expressionPointer(readExpression("input", "/arguments")), Otherwise: expressionPointer(readExpression("input", "/text"))}
	partialTool := objectExpression(map[string]Expression{
		"kind": literalExpression(ToolCallNode), "name": readExpression("input", "/name"), "callId": readExpression("input", "/call_id"),
		"input": objectExpression(map[string]Expression{"kind": literalExpression(JSONInput)}),
	})
	decode["item"] = Expression{Op: "if", When: &Expression{Op: "any", Items: []Expression{isTag(packageToolStarted), isTag(packageToolDelta)}}, Then: &partialTool, Otherwise: expressionPointer(decode["item"])}
	isTool := Expression{Op: "if", When: &Expression{Op: "present", Source: expressionPointer(readExpression("input", "/item"))},
		Then: &Expression{Op: "equal", Items: []Expression{readExpression("input", "/item/kind"), literalExpression(ToolCallNode)}}, Otherwise: expressionPointer(literalExpression(false))}
	encode := document.Mapping.Directions[EncodeEvent].Program.Fields
	encode["kind"] = Expression{Op: "choose", Source: expressionPointer(readExpression("input", "/type")), Cases: map[string]Expression{
		string(ItemStarted): {Op: "if", When: &isTool, Then: expressionPointer(literalExpression(packageToolStarted)), Otherwise: expressionPointer(literalExpression(ItemStarted))},
		string(ItemDelta):   {Op: "if", When: &isTool, Then: expressionPointer(literalExpression(packageToolDelta)), Otherwise: expressionPointer(literalExpression(ItemDelta))},
	}, Otherwise: expressionPointer(readExpression("input", "/type"))}
	encode["text"] = Expression{Op: "if", When: &isTool, Then: &Expression{Op: "omit"}, Otherwise: expressionPointer(readExpression("input", "/delta"))}
	for wire, source := range map[string]string{"name": "/item/name", "call_id": "/item/callId", "arguments": "/delta"} {
		encode[wire] = Expression{Op: "if", When: &isTool, Then: expressionPointer(readExpression("input", source))}
	}
	return document
}

func packageToolDeltaFrame(sequence int, id, arguments string) Value {
	return packageStreamFrame(packageToolDelta, sequence, fmt.Sprintf(`,"item_id":%q,"arguments":%s`, id, StringValue(arguments).raw))
}

func TestPackageStreamContractCompilation(t *testing.T) {
	for _, test := range []struct {
		name string
		edit func(*ProtocolPackage)
		path string
	}{
		{"pointer", func(p *ProtocolPackage) { p.Spec.Documents["provider-event"].Stream.ResponseID = "response_id" }, "/spec/documents/client-event/stream/responseId"},
		{"root-pointer", func(p *ProtocolPackage) { p.Spec.Documents["provider-event"].Stream.ItemID = "" }, "/spec/documents/client-event/stream/itemId"},
		{"missing-action", func(p *ProtocolPackage) { delete(p.Spec.Documents["provider-event"].Stream.Events, "item.delta") }, "/spec/documents/client-event/stream/events"},
		{"unknown-action", func(p *ProtocolPackage) {
			p.Spec.Documents["provider-event"].Stream.Events["future"] = ProtocolStreamEventSpec{Action: "infer"}
		}, "/spec/documents/client-event/stream/events/future/action"},
		{"misplaced-text", func(p *ProtocolPackage) {
			p.Spec.Documents["provider-event"].Stream.Events["response.started"] = ProtocolStreamEventSpec{Action: "response_start", Text: "/text"}
		}, "/spec/documents/client-event/stream/events/response.started"},
		{"missing-text", func(p *ProtocolPackage) {
			p.Spec.Documents["provider-event"].Stream.Events["item.delta"] = ProtocolStreamEventSpec{Action: "text_delta"}
		}, "/spec/documents/client-event/stream/events/item.delta"},
		{"empty-usage", func(p *ProtocolPackage) {
			p.Spec.Documents["provider-event"].Stream.Events["usage.updated"] = ProtocolStreamEventSpec{Action: "usage", Usage: &ProtocolStreamUsageSpec{}}
		}, "/spec/documents/client-event/stream/events/usage.updated/usage"},
		{"missing-error", func(p *ProtocolPackage) {
			p.Spec.Documents["provider-event"].Stream.Events["operation.failed"] = ProtocolStreamEventSpec{Action: "failure"}
		}, "/spec/documents/client-event/stream/events/operation.failed"},
		{"capabilities", func(p *ProtocolPackage) {
			entry := p.Mapping.Directions[EncodeEvent]
			entry.Capabilities = CapabilitySet{TextCapability: true}
			p.Mapping.Directions[EncodeEvent] = entry
		}, "/mapping/directions/encode_event/capabilities"},
		{"missing-event-document", func(p *ProtocolPackage) {
			delete(p.Spec.Documents, p.Mapping.Directions[DecodeEvent].Document)
		}, "/mapping/directions/decode_event/document"},
	} {
		t.Run(test.name, func(t *testing.T) {
			document := eventProtocolPackage(t)
			test.edit(&document)
			if err := document.Validate(DefaultLimits()); err == nil || !strings.Contains(err.Error(), test.path) {
				t.Fatalf("expected declaration error at %s, got %v", test.path, err)
			}
		})
	}
	document := eventProtocolPackage(t)
	before := compilePackageForTest(t, document)
	document.Spec.Documents["provider-event"].Stream.Sequence = "/other_seq"
	after := compilePackageForTest(t, document)
	if before.hash == after.hash || before.mappings[DecodeEvent].definitionHash == after.mappings[DecodeEvent].definitionHash || before.mappings[EncodeEvent].definitionHash == after.mappings[EncodeEvent].definitionHash {
		t.Fatal("stream contract was omitted from package or event mapping identity")
	}
	if before.mappings[DecodeRequest].definitionHash != after.mappings[DecodeRequest].definitionHash {
		t.Fatal("event contract changed request mapping identity")
	}
}

func packageStreamFrame(kind EventType, sequence int, fields string) Value {
	return Value{raw: fmt.Sprintf(`{"kind":%q,"response_id":"r","seq":%d%s}`, kind, sequence, fields)}
}

func TestPackageStreamLifecycle(t *testing.T) {
	start := packageStreamFrame(ResponseStarted, 0, "")
	open := packageStreamFrame(ItemStarted, 1, `,"item_id":"text"`)
	delta := packageStreamFrame(ItemDelta, 2, `,"item_id":"text","text":"a"`)
	close := packageStreamFrame(ItemFinished, 3, `,"item_id":"text"`)
	finish := packageStreamFrame(ResponseFinished, 4, "")
	for _, test := range []struct {
		name   string
		frames []Value
		path   string
	}{
		{"before-start", []Value{open}, "/kind"},
		{"failure-before-start", []Value{packageStreamFrame(OperationFailed, 0, `,"error":{}`)}, "/kind"},
		{"duplicate-start", []Value{start, packageStreamFrame(ResponseStarted, 1, "")}, "/kind"},
		{"unknown-tag", []Value{start, packageStreamFrame("future", 1, "")}, "/kind"},
		{"changed-response", []Value{start, {raw: `{"kind":"response.finished","response_id":"other","seq":1}`}}, "/response_id"},
		{"empty-response", []Value{{raw: `{"kind":"response.started","response_id":"","seq":0}`}}, "/response_id"},
		{"repeated-sequence", []Value{start, packageStreamFrame(ResponseFinished, 0, "")}, "/seq"},
		{"decreasing-sequence", []Value{start, packageStreamFrame(UsageUpdated, 4, `,"usage":{"input":1}`), packageStreamFrame(ResponseFinished, 3, "")}, "/seq"},
		{"negative-sequence", []Value{packageStreamFrame(ResponseStarted, -1, "")}, "/seq"},
		{"orphan-delta", []Value{start, delta}, "/item_id"},
		{"repeated-item", []Value{start, open, packageStreamFrame(ItemStarted, 2, `,"item_id":"text"`)}, "/item_id"},
		{"missing-item-id", []Value{start, packageStreamFrame(ItemStarted, 1, "")}, "/item_id"},
		{"non-string-text", []Value{start, open, packageStreamFrame(ItemDelta, 2, `,"item_id":"text","text":null`)}, "/text"},
		{"repeated-close", []Value{start, open, close, packageStreamFrame(ItemFinished, 4, `,"item_id":"text"`)}, "/item_id"},
		{"delta-after-close", []Value{start, open, close, packageStreamFrame(ItemDelta, 4, `,"item_id":"text","text":"b"`)}, "/item_id"},
		{"unfinished-item", []Value{start, open, finish}, "/kind"},
		{"content-after-terminal", []Value{start, finish, packageStreamFrame(ItemStarted, 5, `,"item_id":"text"`)}, "/kind"},
		{"missing-failure-error", []Value{start, packageStreamFrame(OperationFailed, 1, "")}, "/error"},
		{"null-failure-error", []Value{start, packageStreamFrame(OperationFailed, 1, `,"error":null`)}, "/error"},
	} {
		for _, direction := range []Direction{DecodeEvent, EncodeEvent} {
			t.Run(test.name+"/"+string(direction), func(t *testing.T) {
				compiled := compilePackageForTest(t, eventProtocolPackage(t))
				validation, err := compiled.newWireStreamValidation(direction, Scope{})
				if err != nil {
					t.Fatal(err)
				}
				for index, frame := range test.frames {
					err = validation.Consume(t.Context(), frame)
					if index < len(test.frames)-1 && err != nil {
						t.Fatal("valid prefix rejected", err)
					}
				}
				code := UpstreamContractViolation
				if direction == EncodeEvent {
					code = ConversionRejected
				}
				assertPackageRuntimeIssue(t, err, code, test.path)
				var failure *ConversionError
				if !errors.As(err, &failure) || failure.Issues[0].Direction != direction || failure.Issues[0].Protocol != compiled.Identity() || failure.Issues[0].Stage != "wire.stream" {
					t.Fatal("wire lifecycle lost its direction or contract identity", err)
				}
			})
		}
	}
}

func TestPackageStreamUsageAndTermination(t *testing.T) {
	compiled := compilePackageForTest(t, eventProtocolPackage(t))
	for _, value := range []string{"null", `"1"`, "-1", "1.5", "9223372036854775808", "true", "[]", "{}"} {
		t.Run(value, func(t *testing.T) {
			validation, _ := compiled.NewWireInputStreamValidation(Scope{})
			if err := validation.Consume(t.Context(), packageStreamFrame(ResponseStarted, 0, "")); err != nil {
				t.Fatal(err)
			}
			err := validation.Consume(t.Context(), packageStreamFrame(UsageUpdated, 1, `,"usage":{"input":`+value+`}`))
			assertPackageRuntimeIssue(t, err, UpstreamContractViolation, "/usage/input")
		})
	}
	for _, terminal := range []EventType{ResponseFinished, OperationFailed, OperationCancelled} {
		t.Run(string(terminal), func(t *testing.T) {
			validation, _ := compiled.NewWireInputStreamValidation(Scope{})
			frames := []Value{packageStreamFrame(ResponseStarted, 0, ""), packageStreamFrame(UsageUpdated, 1, `,"usage":{"input":0}`)}
			if terminal != ResponseFinished {
				frames = append(frames, packageStreamFrame(ItemStarted, 2, `,"item_id":"partial"`))
			}
			fields := ""
			if terminal == OperationFailed {
				fields = `,"error":{"code":"synthetic"}`
			}
			frames = append(frames, packageStreamFrame(terminal, 3, fields), packageStreamFrame(UsageUpdated, 4, `,"usage":{"output":2}`))
			for _, frame := range frames {
				if err := validation.Consume(t.Context(), frame); err != nil {
					t.Fatal(err)
				}
			}
			usage := validation.packageStream.replay.Usage()
			if err := validation.Finish(); err != nil || usage.Input.Count != 0 || usage.Output.Count != 2 {
				t.Fatal("terminal or late partial usage rejected", err, usage)
			}
		})
	}
	validation, _ := compiled.NewWireInputStreamValidation(Scope{})
	if err := validation.Consume(t.Context(), packageStreamFrame(ResponseStarted, 0, "")); err != nil {
		t.Fatal(err)
	}
	assertPackageRuntimeIssue(t, validation.Finish(), UpstreamContractViolation, "/kind")
	err := validation.Consume(t.Context(), packageStreamFrame(UsageUpdated, 1, `,"usage":{}`))
	assertPackageRuntimeIssue(t, err, UpstreamContractViolation, "/kind")
}

func TestPackageStreamIndependentValidationAndLimits(t *testing.T) {
	document := eventProtocolPackage(t)
	document.Mapping.Directions[DecodeEvent].Program.Fields["sequence"] = literalExpression(99)
	compiled := compilePackageForTest(t, document)
	validation, _ := compiled.NewWireInputStreamValidation(Scope{})
	start := packageStreamFrame(ResponseStarted, 0, "")
	if err := validation.Consume(t.Context(), start); err != nil {
		t.Fatal(err)
	}
	repeated := packageStreamFrame(ResponseFinished, 0, "")
	if _, err := compiled.DecodeEvents(t.Context(), repeated, EvaluationContext{}); err != nil {
		t.Fatal("mapping fixture should hide the actual sequence", err)
	}
	assertPackageRuntimeIssue(t, validation.Consume(t.Context(), repeated), UpstreamContractViolation, "/seq")

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	validation, _ = compiled.NewWireInputStreamValidation(Scope{})
	if err := validation.Consume(ctx, start); !errors.Is(err, context.Canceled) {
		t.Fatal("wire validation lost cancellation", err)
	}

	compiled.limits.BufferBytes = 512
	validation, _ = compiled.NewWireInputStreamValidation(Scope{})
	if err := validation.Consume(t.Context(), start); err != nil {
		t.Fatal(err)
	}
	for index := 1; index <= 5; index++ {
		frame := packageStreamFrame(ItemStarted, index, fmt.Sprintf(`,"item_id":%q`, strings.Repeat("i", 119)+fmt.Sprint(index)))
		err := validation.Consume(t.Context(), frame)
		if index < 5 && err != nil {
			t.Fatal("bounded identity prefix rejected", err)
		}
		if index == 5 {
			var limit *ResourceLimitError
			if !errors.As(err, &limit) || limit.Metric != "identity_bytes" {
				t.Fatal("retained identities escaped the buffer budget or lost its cause", err)
			}
		}
	}
}

func TestPackageStreamToolContractCompilation(t *testing.T) {
	for _, test := range []struct {
		name string
		edit func(*ProtocolPackage)
		path string
	}{
		{"missing-name", func(p *ProtocolPackage) {
			p.Spec.Documents["provider-event"].Stream.Events[string(packageToolStarted)].Tool.Name = ""
		}, "/spec/documents/client-event/stream/events/tool.started/tool/name"},
		{"missing-call-id", func(p *ProtocolPackage) {
			p.Spec.Documents["provider-event"].Stream.Events[string(packageToolStarted)].Tool.CallID = ""
		}, "/spec/documents/client-event/stream/events/tool.started/tool/callId"},
		{"missing-arguments", func(p *ProtocolPackage) {
			p.Spec.Documents["provider-event"].Stream.Events[string(packageToolDelta)] = ProtocolStreamEventSpec{Action: "tool_delta"}
		}, "/spec/documents/client-event/stream/events/tool.delta"},
		{"misplaced-tool", func(p *ProtocolPackage) {
			p.Spec.Documents["provider-event"].Stream.Events[string(packageToolFinished)] = ProtocolStreamEventSpec{Action: "tool_finish", Tool: &ProtocolStreamToolSpec{Name: "/name", CallID: "/call_id"}}
		}, "/spec/documents/client-event/stream/events/tool.finished"},
		{"tools-cannot-replace-text", func(p *ProtocolPackage) { delete(p.Spec.Documents["provider-event"].Stream.Events, string(ItemDelta)) }, "/spec/documents/client-event/stream/events"},
		{"snapshot-not-supported", func(p *ProtocolPackage) {
			p.Spec.Documents["provider-event"].Stream.Events["tool.snapshot"] = ProtocolStreamEventSpec{Action: "tool_snapshot"}
		}, "/spec/documents/client-event/stream/events/tool.snapshot/action"},
		{"missing-capability", func(p *ProtocolPackage) {
			entry := p.Mapping.Directions[EncodeEvent]
			entry.Capabilities = CapabilitySet{TextCapability: true, UsageCapability: true}
			p.Mapping.Directions[EncodeEvent] = entry
		}, "/mapping/directions/encode_event/capabilities"},
	} {
		t.Run(test.name, func(t *testing.T) {
			document := toolStreamPackage(t)
			test.edit(&document)
			if err := document.Validate(DefaultLimits()); err == nil || !strings.Contains(err.Error(), test.path) {
				t.Fatalf("expected tool declaration error at %s, got %v", test.path, err)
			}
		})
	}
}

func TestPackageStreamJSONToolLifecycle(t *testing.T) {
	compiled := compilePackageForTest(t, toolStreamPackage(t))
	for _, direction := range []Direction{DecodeEvent, EncodeEvent} {
		t.Run(string(direction), func(t *testing.T) {
			validation, _ := compiled.newWireStreamValidation(direction, Scope{})
			frames := []Value{
				packageStreamFrame(ResponseStarted, 0, ""),
				packageStreamFrame(packageToolStarted, 1, `,"item_id":"tool","name":"lookup","call_id":"call"`),
				packageToolDeltaFrame(2, "tool", `{"n":`),
				packageToolDeltaFrame(3, "tool", `9007199254740993}`),
				packageStreamFrame(packageToolFinished, 4, `,"item_id":"tool"`),
				packageStreamFrame(ResponseFinished, 5, ""),
				packageStreamFrame(UsageUpdated, 6, `,"usage":{"output":0}`),
			}
			for index, frame := range frames {
				if err := validation.Consume(t.Context(), frame); err != nil {
					t.Fatal(err)
				}
				if index == 3 && validation.packageStream.replay.state.buffered != len(`{"n":9007199254740993}`) {
					t.Fatal("tool fragments bypassed bounded accumulation")
				}
				if index >= 4 && validation.packageStream.replay.state.buffered != 0 {
					t.Fatal("completed tool retained argument bytes")
				}
			}
			if err := validation.Finish(); err != nil || validation.packageStream.identityBytes != len("rtoollookupcall") || validation.packageStream.replay.Usage().Output.Count != 0 {
				t.Fatal("tool completion changed identities, usage or terminal", err)
			}
		})
	}
}

func TestPackageStreamToolMappingRoundTrip(t *testing.T) {
	sourceDocument := toolStreamPackage(t)
	sourceDocument.Spec.ID = "tool-source"
	sourceDocument.Mapping.Native.Preserve = false
	targetDocument := toolStreamPackage(t)
	targetDocument.Spec.ID = "tool-target"
	targetDocument.Mapping.Native.Preserve = false
	// The authored target closes both kinds with item_finish. It needs no
	// tool-specific end tag, remembered item kind or fabricated input snapshot.
	delete(targetDocument.Spec.Documents["provider-event"].Stream.Events, string(packageToolFinished))
	source, target := compilePackageForTest(t, sourceDocument), compilePackageForTest(t, targetDocument)
	sourceValidation, err := source.NewWireInputStreamValidation(Scope{})
	if err != nil {
		t.Fatal(err)
	}
	finalValidation, err := target.NewWireStreamValidation(Scope{})
	if err != nil {
		t.Fatal(err)
	}
	collector, err := NewResponseCollector(target.Target(EncodeEvent, Scope{}), DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	fragments := []string{` { "n" : `, `9007199254740993 } `}
	frames := []Value{
		packageStreamFrame(ResponseStarted, 0, ""),
		packageStreamFrame(packageToolStarted, 1, `,"item_id":"tool","name":"lookup","call_id":"call"`),
		packageToolDeltaFrame(2, "tool", fragments[0]),
		packageToolDeltaFrame(3, "tool", fragments[1]),
		packageStreamFrame(packageToolFinished, 4, `,"item_id":"tool"`),
		packageStreamFrame(ResponseFinished, 5, ""),
		packageStreamFrame(UsageUpdated, 6, `,"usage":{"input":5,"output":2,"total":7}`),
	}
	for index, wire := range frames {
		if err := sourceValidation.Consume(t.Context(), wire); err != nil {
			t.Fatal(err)
		}
		decoded, err := source.DecodeFrame(t.Context(), wire, EvaluationContext{})
		if err != nil || len(decoded.Events) != 1 {
			t.Fatal("tool mapping failed to decode one event", err)
		}
		if index == 4 && decoded.Events[0].Item != nil {
			t.Fatal("tool completion invented a snapshot")
		}
		encoded, err := target.EncodeFrame(t.Context(), decoded, EvaluationContext{})
		if err != nil || len(encoded) != 1 {
			t.Fatal("tool mapping failed to encode one frame", err)
		}
		fields, err := wire.ReadObject()
		if err != nil {
			t.Fatal(err)
		}
		if index == 4 {
			fields["kind"] = StringValue(string(ItemFinished))
		}
		// Compare the declared canonical output, not the source frame's raw
		// bytes: the final tag intentionally differs and Native is disabled.
		if encoded[0] != fixtureValue(t, fields) {
			t.Fatal("mapping changed fields outside its declared end tag", encoded[0])
		}
		if err := finalValidation.Consume(t.Context(), encoded[0]); err != nil {
			t.Fatal(err)
		}
		if err := finalValidation.CommitDelivery(); err != nil {
			t.Fatal(err)
		}
		delivered, err := target.DecodeFrame(t.Context(), encoded[0], EvaluationContext{})
		if err != nil || len(delivered.Events) != 1 {
			t.Fatal("delivered tool mapping failed", err)
		}
		if index == 2 || index == 3 {
			if !delivered.Events[0].Delta.IsString(fragments[index-2]) {
				t.Fatal("tool fragment spelling or boundary changed")
			}
		}
		if _, _, err := collector.Consume(delivered.Events[0]); err != nil {
			t.Fatal(err)
		}
	}
	if err := errors.Join(sourceValidation.Finish(), finalValidation.Finish()); err != nil {
		t.Fatal(err)
	}
	response, err := collector.Finish()
	if err != nil || len(response.Content) != 1 || collector.replay.terminal != ResponseFinished || !finalValidation.packageStream.delivered.isTerminal || response.Usage.Input.Count != 5 || response.Usage.Output.Count != 2 || response.Usage.Total.Count != 7 {
		t.Fatal("tool collection lost its terminal or usage", response, err)
	}
	tool := response.Content[0]
	if tool.Kind != ToolCallNode || !tool.Name.IsString("lookup") || !tool.CallID.IsString("call") || tool.Input == nil || tool.Input.Kind != JSONInput || tool.Input.Value.raw != strings.Join(fragments, "") {
		t.Fatal("tool collection changed identities, exact argument bytes or input kind", tool)
	}
}

func TestPackageStreamRejectsInvalidToolLifecycle(t *testing.T) {
	compiled := compilePackageForTest(t, toolStreamPackage(t))
	start := packageStreamFrame(ResponseStarted, 0, "")
	tool := packageStreamFrame(packageToolStarted, 1, `,"item_id":"tool","name":"lookup","call_id":"call"`)
	text := packageStreamFrame(ItemStarted, 1, `,"item_id":"text"`)
	delta := packageToolDeltaFrame(2, "tool", `{}`)
	finish := packageStreamFrame(packageToolFinished, 3, `,"item_id":"tool"`)
	for _, test := range []struct {
		name   string
		frames []Value
		path   string
	}{
		{"missing-name", []Value{start, packageStreamFrame(packageToolStarted, 1, `,"item_id":"tool","call_id":"call"`)}, "/name"},
		{"empty-call-id", []Value{start, packageStreamFrame(packageToolStarted, 1, `,"item_id":"tool","name":"lookup","call_id":""`)}, "/call_id"},
		{"arguments-must-be-string", []Value{start, tool, packageStreamFrame(packageToolDelta, 2, `,"item_id":"tool","arguments":null`)}, "/arguments"},
		{"text-delta-on-tool", []Value{start, tool, packageStreamFrame(ItemDelta, 2, `,"item_id":"tool","text":"{}"`)}, "/item_id"},
		{"tool-delta-on-text", []Value{start, text, packageToolDeltaFrame(2, "text", `{}`)}, "/item_id"},
		{"tool-finish-on-text", []Value{start, text, packageStreamFrame(packageToolFinished, 2, `,"item_id":"text"`)}, "/item_id"},
		{"duplicate-call-id", []Value{start, tool, packageStreamFrame(packageToolStarted, 2, `,"item_id":"other","name":"lookup","call_id":"call"`)}, "/call_id"},
		{"unfinished-json", []Value{start, tool, packageToolDeltaFrame(2, "tool", `{"n":`), packageStreamFrame(ItemFinished, 3, `,"item_id":"tool"`)}, "/item_id"},
		{"duplicate-finish", []Value{start, tool, delta, finish, packageStreamFrame(packageToolFinished, 4, `,"item_id":"tool"`)}, "/item_id"},
		{"terminal-with-open-tool", []Value{start, tool, delta, packageStreamFrame(ResponseFinished, 3, "")}, "/kind"},
	} {
		t.Run(test.name, func(t *testing.T) {
			validation, _ := compiled.NewWireInputStreamValidation(Scope{})
			for index, frame := range test.frames {
				err := validation.Consume(t.Context(), frame)
				if index < len(test.frames)-1 {
					if err != nil {
						t.Fatal("valid prefix rejected", err)
					}
				} else {
					assertPackageRuntimeIssue(t, err, UpstreamContractViolation, test.path)
				}
			}
		})
	}
}

func TestPackageStreamToolTerminationAndBudgets(t *testing.T) {
	compiled := compilePackageForTest(t, toolStreamPackage(t))
	for _, terminal := range []EventType{OperationCancelled, OperationFailed} {
		t.Run(string(terminal), func(t *testing.T) {
			validation, _ := compiled.NewWireInputStreamValidation(Scope{})
			for _, frame := range []Value{
				packageStreamFrame(ResponseStarted, 0, ""),
				packageStreamFrame(packageToolStarted, 1, `,"item_id":"tool","name":"lookup","call_id":"call"`),
				packageToolDeltaFrame(2, "tool", `{"partial":`),
				packageStreamFrame(terminal, 3, `,"error":{"code":"synthetic"}`),
				packageStreamFrame(UsageUpdated, 4, `,"usage":{"output":1}`),
			} {
				if err := validation.Consume(t.Context(), frame); err != nil {
					t.Fatal(err)
				}
			}
			if err := validation.Finish(); err != nil || validation.packageStream.replay.state.buffered != 0 {
				t.Fatal("unsuccessful terminal kept partial JSON or required completion", err)
			}
		})
	}
	compiled.limits.BufferBytes = 512
	arguments := `{"text":"` + strings.Repeat("x", 300) + `"}`
	for _, shouldFinish := range []bool{false, true} {
		t.Run(fmt.Sprintf("release-before-next=%t", shouldFinish), func(t *testing.T) {
			validation, _ := compiled.NewWireInputStreamValidation(Scope{})
			frames := []Value{
				packageStreamFrame(ResponseStarted, 0, ""),
				packageStreamFrame(packageToolStarted, 1, `,"item_id":"one","name":"lookup","call_id":"first"`),
				packageToolDeltaFrame(2, "one", arguments),
			}
			if shouldFinish {
				frames = append(frames, packageStreamFrame(packageToolFinished, 3, `,"item_id":"one"`))
			}
			frames = append(frames, packageStreamFrame(packageToolStarted, 4, `,"item_id":"two","name":"lookup","call_id":"second"`))
			for _, frame := range frames {
				if err := validation.Consume(t.Context(), frame); err != nil {
					t.Fatal(err)
				}
			}
			err := validation.Consume(t.Context(), packageToolDeltaFrame(5, "two", arguments))
			if !shouldFinish {
				assertPackageRuntimeIssue(t, err, LimitExceeded, "/arguments")
			} else if err != nil || validation.packageStream.replay.state.buffered != len(arguments) {
				t.Fatal("completed argument budget was not reusable", err)
			}
		})
	}
	validation, _ := compiled.NewWireInputStreamValidation(Scope{})
	if err := validation.Consume(t.Context(), packageStreamFrame(ResponseStarted, 0, "")); err != nil {
		t.Fatal(err)
	}
	for index := 1; index <= 2; index++ {
		frame := packageStreamFrame(packageToolStarted, index, fmt.Sprintf(`,"item_id":%q,"name":%q,"call_id":%q`, fmt.Sprint(index), strings.Repeat("n", 150), strings.Repeat("c", 150)+fmt.Sprint(index)))
		err := validation.Consume(t.Context(), frame)
		if index == 1 && err != nil {
			t.Fatal(err)
		}
		if index == 2 {
			var failure *ResourceLimitError
			if !errors.As(err, &failure) || failure.Metric != "identity_bytes" {
				t.Fatal("tool name and call ID bypassed the identity budget", err)
			}
		}
	}
}
