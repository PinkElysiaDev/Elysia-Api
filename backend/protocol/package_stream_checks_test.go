package protocol

import (
	"errors"
	"strings"
	"testing"
)

func wireSlotPackage(t *testing.T) ProtocolPackage {
	t.Helper()
	document := eventProtocolPackage(t)
	stream := document.Spec.Documents["provider-event"].Stream
	stream.Identities = []ProtocolStreamIdentitySpec{{Path: "/response_id", Type: StringType, Diagnostic: &RejectDiagnostic{Code: InvalidAssociation, Stage: "wire"}}}
	stream.State = map[string]ProtocolStateSlot{
		"previous": {Schema: ValueSchema{Type: IntegerType}, Initial: coreInteger(0), Update: readExpression("state", "/total")},
		"total": {Schema: ValueSchema{Type: IntegerType}, Initial: coreInteger(0), Update: Expression{Op: "integer", Operation: "add", Items: []Expression{
			readExpression("state", "/total"), readExpression("input", "/usage/input"),
		}}},
	}
	current := WireAssertion{Path: "/usage/input", Schema: ValueSchema{Type: IntegerType}, Code: InvalidInput, Reason: "input count must be nonnegative",
		Check:      &Expression{Op: "integer", Operation: "gte", Items: []Expression{readExpression("input", ""), literalExpression(0)}},
		Diagnostic: &RejectDiagnostic{Code: InvalidInput, Stage: "stream"}}
	accumulated := WireAssertion{Path: "/usage", Schema: ValueSchema{Type: ObjectType, AllowUnknown: true}, Code: InvalidInput, Reason: "retained total exceeds this observation",
		Check:      &Expression{Op: "integer", Operation: "lte", Items: []Expression{readExpression("state", "/total"), readExpression("root", "/maximum")}},
		Diagnostic: &RejectDiagnostic{Code: InvalidInput, Stage: "stream"}}
	stream.Events[string(UsageUpdated)] = ProtocolStreamEventSpec{
		{Action: "assert", Assertions: []WireAssertion{current}},
		{Action: "state_update"},
		{Action: "assert", Assertions: []WireAssertion{accumulated}},
		{Action: "usage", Usage: &ProtocolStreamUsageSpec{Input: "/usage/input"}},
	}
	decode := document.Mapping.Directions[DecodeEvent]
	decode.State = map[string]ProtocolStateSlot{"total": {
		Schema: ValueSchema{Type: IntegerType}, Initial: coreInteger(100),
		Update: Expression{Op: "integer", Operation: "add", Items: []Expression{readExpression("state", "/total"), literalExpression(1)}},
	}}
	document.Mapping.Directions[DecodeEvent] = decode
	return document
}

func TestPackageStreamWireSlotsCommitAndIsolation(t *testing.T) {
	compiled := compilePackageForTest(t, wireSlotPackage(t))
	validation, err := compiled.NewWireInputStreamValidation(Scope{})
	if err != nil {
		t.Fatal(err)
	}
	options := EvaluationContext{State: NewEvaluationState()}
	defer options.State.Release()
	for _, frame := range []Value{
		packageStreamFrame(ResponseStarted, 0, ""),
		packageStreamFrame(UsageUpdated, 1, `,"usage":{"input":2},"maximum":10`),
		packageStreamFrame(UsageUpdated, 2, `,"usage":{"input":3},"maximum":10`),
	} {
		if err := validation.Consume(t.Context(), frame); err != nil {
			t.Fatal(err)
		}
		if _, err := compiled.DecodeFrame(t.Context(), frame, options); err != nil {
			t.Fatal(err)
		}
	}
	state := validation.packageStream
	if state.slots[0].raw != "2" || state.slots[1].raw != "5" {
		t.Fatal("wire updates did not read one preceding snapshot", state.slots)
	}
	retained := options.State.packageStates[moduleStateKey{compiled: compiled, direction: DecodeEvent}]
	if retained.slots[0].raw != "103" {
		t.Fatal("wire slots shared mutable mapping state", retained.slots)
	}
	err = validation.Consume(t.Context(), packageStreamFrame(UsageUpdated, 3, `,"usage":{"input":7},"maximum":10`))
	var failure *ConversionError
	if !errors.As(err, &failure) || failure.Issues[0].Code != InvalidInput || failure.Issues[0].Stage != "stream" || failure.Issues[0].Path != "/usage" {
		t.Fatal("post-update assertion lost its diagnostic", err)
	}
	if state.slots[0].raw != "2" || state.slots[1].raw != "5" || state.hasPendingSlots || state.accepted.sequence != 2 {
		t.Fatal("failed assertion committed slots or sequence", state.slots, state.accepted)
	}
}

func TestPackageStreamOrderedAssertions(t *testing.T) {
	compiled := compilePackageForTest(t, wireSlotPackage(t))
	for _, fixture := range []struct{ name, raw, path string }{
		{"identity-before-sequence-and-count", `{"kind":"usage.updated","response_id":"other","seq":0,"usage":{"input":null},"maximum":10}`, "/response_id"},
		{"sequence-before-count", `{"kind":"usage.updated","response_id":"r","seq":0,"usage":{"input":null},"maximum":10}`, "/seq"},
		{"count-before-update", `{"kind":"usage.updated","response_id":"r","seq":1,"usage":{"input":null},"maximum":10}`, "/usage/input"},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			validation, err := compiled.NewWireInputStreamValidation(Scope{})
			if err != nil {
				t.Fatal(err)
			}
			if err := validation.Consume(t.Context(), packageStreamFrame(ResponseStarted, 0, "")); err != nil {
				t.Fatal(err)
			}
			err = validation.Consume(t.Context(), Value{raw: fixture.raw})
			var failure *ConversionError
			if !errors.As(err, &failure) || failure.Issues[0].Path != fixture.path || validation.packageStream.slots[1].raw != "0" {
				t.Fatal("ordered validation or rollback changed", err, validation.packageStream.slots)
			}
		})
	}
	document := wireSlotPackage(t)
	actions := document.Spec.Documents["provider-event"].Stream.Events[string(UsageUpdated)]
	document.Spec.Documents["provider-event"].Stream.Events[string(UsageUpdated)] = append(actions[:2:2], append(ProtocolStreamEventSpec{{Action: "state_update"}}, actions[2:]...)...)
	compiled = compilePackageForTest(t, document)
	validation, _ := compiled.NewWireInputStreamValidation(Scope{})
	if err := validation.Consume(t.Context(), packageStreamFrame(ResponseStarted, 0, "")); err != nil {
		t.Fatal(err)
	}
	if err := validation.Consume(t.Context(), packageStreamFrame(UsageUpdated, 1, `,"usage":{"input":2},"maximum":10`)); err == nil || validation.packageStream.slots[1].raw != "0" {
		t.Fatal("a frame committed its slots more than once", err)
	}
}

func TestPackageStreamWireSlotBudget(t *testing.T) {
	document := toolStreamPackage(t)
	stream := document.Spec.Documents["provider-event"].Stream
	stream.State = map[string]ProtocolStateSlot{"payload": {Schema: ValueSchema{Type: StringType}, Initial: StringValue(""), Update: readExpression("input", "/stash")}}
	stream.Events["reserve"] = ProtocolStreamEventSpec{{Action: "state_update"}}
	compiled := compilePackageForTest(t, document)
	limits := DefaultLimits()
	limits.BufferBytes = 256
	state, err := newPackageStreamValidation(compiled.wireContracts[DecodeEvent].packageStream, compiled.Target(DecodeEvent, Scope{}), limits)
	if err != nil {
		t.Fatal(err)
	}
	for _, frame := range []Value{
		packageStreamFrame(ResponseStarted, 0, ""),
		packageStreamFrame(packageToolStarted, 1, `,"item_id":"t","name":"f","call_id":"c"`),
		packageToolDeltaFrame(2, "t", `{"value":"`+strings.Repeat("a", 80)),
		packageStreamFrame("reserve", 3, `,"stash":"`+strings.Repeat("x", 100)+`"`),
		packageStreamFrame("reserve", 4, `,"stash":"`+strings.Repeat("y", 100)+`"`),
	} {
		if err := state.consume(t.Context(), frame); err != nil {
			t.Fatal("slot replacement was charged as another retained value", err)
		}
	}
	err = state.consume(t.Context(), packageStreamFrame("reserve", 5, `,"stash":"`+strings.Repeat("z", 180)+`"`))
	var limit *ResourceLimitError
	if !errors.As(err, &limit) || limit.Metric != "state_bytes" || !state.slots[0].IsString(strings.Repeat("y", 100)) || state.hasPendingSlots {
		t.Fatal("wire slots and tool buffer did not share their original budget", err, state.slots)
	}
}

func TestPackageStreamCheckCompilation(t *testing.T) {
	for _, fixture := range []struct {
		name string
		edit func(*ProtocolStreamSpec)
	}{
		{"update-without-slots", func(stream *ProtocolStreamSpec) { stream.State = nil }},
		{"sequence-reason-without-diagnostic", func(stream *ProtocolStreamSpec) { stream.SequenceReason = "sequence is invalid" }},
		{"nested-update", func(stream *ProtocolStreamSpec) {
			stream.Events["nested"] = ProtocolStreamEventSpec{{ForEach: "/items", Actions: []ProtocolStreamActionSpec{{Action: "state_update"}}}}
		}},
		{"assertion-on-lifecycle", func(stream *ProtocolStreamSpec) {
			stream.Events[string(ResponseStarted)][0].Assertions = stream.Events[string(UsageUpdated)][0].Assertions
		}},
		{"undeclared-slot", func(stream *ProtocolStreamSpec) {
			stream.State["total"] = ProtocolStateSlot{Schema: ValueSchema{Type: IntegerType}, Update: readExpression("state", "/absent")}
		}},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			document := wireSlotPackage(t)
			fixture.edit(document.Spec.Documents["provider-event"].Stream)
			if err := document.Validate(DefaultLimits()); err == nil {
				t.Fatal("invalid wire state contract compiled")
			}
		})
	}
}

func TestPackageStreamExplicitCheckDiagnostics(t *testing.T) {
	invalidUsage := literalExpression(map[string]any{
		"output":  map[string]any{"count": 2, "origin": "observed"},
		"details": map[string]any{"output.reasoning_tokens": map[string]any{"count": 3, "origin": "observed"}},
	})
	checked := Expression{Op: "let", Name: "checked", Source: &Expression{Op: "check_usage", Source: &invalidUsage}, Body: expressionPointer(literalExpression(true))}
	for _, fixture := range []struct {
		name, path string
		check      Expression
		code       IssueCode
		stage      string
	}{
		{"checked-arithmetic", "/usage/details/output.reasoning_tokens", checked, InvalidInput, "stream"},
		{"declared-reject", "/declared", Expression{Op: "reject", Path: "/declared", Reason: "declared boundary", Diagnostic: &RejectDiagnostic{Code: UnsupportedCapability, Stage: "wire"}}, UnsupportedCapability, "wire"},
		{"ordinary-expression", "/fallback", Expression{Op: "parse_json", Source: expressionPointer(literalExpression("not-json"))}, ConversionRejected, "wire"},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			document := eventProtocolPackage(t)
			document.Spec.Documents["provider-event"].Stream.Events["checked"] = ProtocolStreamEventSpec{{Action: "assert", Assertions: []WireAssertion{{
				Path: "", Schema: ValueSchema{Type: AnyType}, Code: ConversionRejected, Reason: "fallback boundary", Check: &fixture.check,
				Diagnostic: &RejectDiagnostic{Code: ConversionRejected, Stage: "wire", Path: "/fallback"},
			}}}}
			compiled := compilePackageForTest(t, document)
			validation, err := compiled.NewWireInputStreamValidation(Scope{})
			if err != nil {
				t.Fatal(err)
			}
			err = validation.Consume(t.Context(), packageStreamFrame("checked", 0, ""))
			var failure *ConversionError
			if !errors.As(err, &failure) || failure.Issues[0].Code != fixture.code || failure.Issues[0].Stage != fixture.stage || failure.Issues[0].Path != fixture.path {
				t.Fatal("explicit constraint or ordinary expression diagnostic changed", err)
			}
			if fixture.name == "checked-arithmetic" {
				var checked *declaredCheckError
				if !errors.As(err, &checked) {
					t.Fatal("arithmetic error lost its marked cause", err)
				}
			}
		})
	}
}
