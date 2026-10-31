package protocol

import "testing"

func TestPackageDeclaredFrameBatch(t *testing.T) {
	document := statefulEventPackage(t)
	document.Mapping.Native.Preserve = false
	mapping := document.Mapping.Directions[EncodeEvent]
	mapping.IsFrameBatch = true
	mapping.Program = Expression{Op: "array", Items: []Expression{
		mapping.Program,
		objectExpression(map[string]Expression{
			"kind": literalExpression(UsageUpdated), "response_id": readExpression("input", "/responseId"),
			"seq": literalExpression(1), "usage": objectExpression(map[string]Expression{"input": literalExpression(7)}),
		}),
	}}
	mapping.Effects[0].Target = "/0/response_id"
	document.Mapping.Directions[EncodeEvent] = mapping
	compiled := compilePackageForTest(t, document)
	input := Event{SchemaVersion: ProtocolCoreVersion, Type: ResponseStarted, ResponseID: StringValue("r"), Sequence: coreInteger(0)}
	options := EvaluationContext{State: NewEvaluationState()}
	frames, err := compiled.EncodeFrames(t.Context(), input, options)
	if err != nil || len(frames) != 2 {
		t.Fatal("declared batch was not expanded", frames, err)
	}
	validation, err := compiled.NewWireStreamValidation(Scope{})
	if err != nil {
		t.Fatal(err)
	}
	for _, frame := range frames {
		if err := validation.Consume(t.Context(), frame); err != nil {
			t.Fatal("rendered frame violates its own wire contract", err)
		}
	}
	retained := options.State.packageStates[moduleStateKey{compiled: compiled, direction: EncodeEvent}]
	if retained.slots[0].raw != "1" {
		t.Fatal("batch updated event state once per frame", retained.slots)
	}
	encoded, err := compiled.EncodeEvent(t.Context(), input, EvaluationContext{State: NewEvaluationState()})
	want, encodeErr := EncodeValue(frames)
	if err != nil || encodeErr != nil || encoded.raw != want.raw {
		t.Fatal("array API and frame API disagree", encoded.raw, want.raw, err, encodeErr)
	}
	for _, direction := range []Direction{DecodeEvent, EncodeRequest} {
		invalid := eventProtocolPackage(t)
		entry := invalid.Mapping.Directions[direction]
		entry.IsFrameBatch = true
		invalid.Mapping.Directions[direction] = entry
		if err := invalid.Validate(DefaultLimits()); err == nil {
			t.Fatal("frame batch accepted outside an event encoder", direction)
		}
	}
}

func TestPackageFinalEventDocument(t *testing.T) {
	document := eventProtocolPackage(t)
	entry := document.Mapping.Directions[EncodeEvent]
	final := document.Spec.Documents[entry.Document]
	final.Assertions = append(final.Assertions, WireAssertion{Path: "/delivery_tag", Schema: &ValueSchema{Type: StringType}, Code: ConversionRejected, Reason: "delivery marker is required"})
	document.Spec.Documents["delivered-event"] = final
	entry.FinalDocument = "delivered-event"
	document.Mapping.Directions[EncodeEvent] = entry
	compiled := compilePackageForTest(t, document)
	event := Event{SchemaVersion: ProtocolCoreVersion, Type: ResponseStarted, ResponseID: StringValue("r"), Sequence: coreInteger(0)}
	frames, err := compiled.EncodeFrames(t.Context(), event, EvaluationContext{State: NewEvaluationState()})
	if err != nil || len(frames) != 1 {
		t.Fatal("delivery contract changed the component boundary", frames, err)
	}
	validation, err := compiled.NewWireStreamValidation(Scope{})
	if err != nil {
		t.Fatal(err)
	}
	assertPackageRuntimeIssue(t, validation.Consume(t.Context(), frames[0]), ConversionRejected, "/delivery_tag")
	fields, err := frames[0].ReadObject()
	if err != nil {
		t.Fatal(err)
	}
	fields["delivery_tag"] = StringValue("ready")
	valid, err := EncodeValue(fields)
	if err != nil {
		t.Fatal(err)
	}
	if err := validation.Consume(t.Context(), valid); err != nil {
		t.Fatal("failed final contract advanced the stream", err)
	}
}
