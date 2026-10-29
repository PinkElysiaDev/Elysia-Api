package protocol

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestPackageStreamContractCompilation(t *testing.T) {
	for _, test := range []struct {
		name string
		edit func(*ProtocolPackage)
		path string
	}{
		{"pointer", func(p *ProtocolPackage) { p.Spec.Stream.ResponseID = "response_id" }, "/spec/stream/responseId"},
		{"root-pointer", func(p *ProtocolPackage) { p.Spec.Stream.ItemID = "" }, "/spec/stream/itemId"},
		{"missing-action", func(p *ProtocolPackage) { delete(p.Spec.Stream.Events, "item.delta") }, "/spec/stream/events"},
		{"unknown-action", func(p *ProtocolPackage) { p.Spec.Stream.Events["future"] = ProtocolStreamEventSpec{Action: "infer"} }, "/spec/stream/events/future/action"},
		{"misplaced-text", func(p *ProtocolPackage) {
			p.Spec.Stream.Events["response.started"] = ProtocolStreamEventSpec{Action: "response_start", Text: "/text"}
		}, "/spec/stream/events/response.started"},
		{"missing-text", func(p *ProtocolPackage) {
			p.Spec.Stream.Events["item.delta"] = ProtocolStreamEventSpec{Action: "text_delta"}
		}, "/spec/stream/events/item.delta"},
		{"empty-usage", func(p *ProtocolPackage) {
			p.Spec.Stream.Events["usage.updated"] = ProtocolStreamEventSpec{Action: "usage", Usage: &ProtocolStreamUsageSpec{}}
		}, "/spec/stream/events/usage.updated/usage"},
		{"missing-error", func(p *ProtocolPackage) {
			p.Spec.Stream.Events["operation.failed"] = ProtocolStreamEventSpec{Action: "failure"}
		}, "/spec/stream/events/operation.failed"},
		{"capabilities", func(p *ProtocolPackage) {
			entry := p.Mapping.Directions[EncodeEvent]
			entry.Capabilities = CapabilitySet{TextCapability: true}
			p.Mapping.Directions[EncodeEvent] = entry
		}, "/mapping/directions/encode_event/capabilities"},
		{"unused-contract", func(p *ProtocolPackage) {
			delete(p.Mapping.Directions, DecodeEvent)
			delete(p.Mapping.Directions, EncodeEvent)
			delete(p.Spec.WireContracts, DecodeEvent)
			delete(p.Spec.WireContracts, EncodeEvent)
		}, "/spec/stream"},
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
	document.Spec.Stream.Sequence = "/other_seq"
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
