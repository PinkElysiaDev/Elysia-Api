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
