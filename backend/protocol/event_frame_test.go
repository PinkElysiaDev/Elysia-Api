package protocol

import "testing"

func compoundFrameDefinition(t *testing.T) Definition {
	t.Helper()
	definition := textVerificationDefinition(t, "compound")
	definition.Native.Preserve = true
	definition.Directions[DecodeEvent] = Mapping{Transform: expressionPointer(readExpression("input", "/events"))}
	definition.Directions[EncodeEvent] = Mapping{Transform: expressionPointer(Expression{Op: "object", Fields: map[string]Expression{"events": {Op: "array", Items: []Expression{readExpression("input", "")}}}})}
	return definition
}

func TestCompoundFrameRoundTripPreservesOneNativeFrame(t *testing.T) {
	compiled := compileTestDefinition(t, compoundFrameDefinition(t))
	value, err := ParseValue([]byte(`{"events":[{"schemaVersion":1,"type":"item.started","itemId":"a","item":{"kind":"text"}},{"schemaVersion":1,"type":"item.delta","itemId":"a","delta":"hello"},{"schemaVersion":1,"type":"response.finished"}],"unknown":[null,false,9007199254740993]}`))
	if err != nil {
		t.Fatal(err)
	}
	options := EvaluationContext{State: NewEvaluationState()}
	frame, err := compiled.DecodeFrame(t.Context(), value, options)
	if err != nil {
		t.Fatal(err)
	}
	if len(frame.Events) != 3 {
		t.Fatal("compound frame was not decoded")
	}
	output, err := compiled.EncodeFrame(t.Context(), frame, options)
	if err != nil || len(output) != 1 || !equalValues(value, output[0]) {
		t.Fatalf("native replay changed frame count or fields: %v %v", output, err)
	}
	sample := Sample{ID: "compound", Direction: DecodeEvent, Input: value}
	result, err := executeEventFixture(t.Context(), compiled, sample)
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyEventRoundTrip(t.Context(), compiled, sample, result); err != nil {
		t.Fatal("offline replay differs from live forwarding", err)
	}
	frame.Events[1].Delta = StringValue("edited")
	if _, err := compiled.EncodeFrame(t.Context(), frame, options); err == nil {
		t.Fatal("semantic edit was overwritten by original frame")
	}
}

func TestNativeFrameCannotBypassDifferentEncoder(t *testing.T) {
	definition := compoundFrameDefinition(t)
	source := compileTestDefinition(t, definition)
	value, _ := ParseValue([]byte(`{"events":[{"schemaVersion":1,"type":"response.finished"}]}`))
	frame, err := source.DecodeFrame(t.Context(), value, EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	definition.ID = "different-encoder"
	definition.Directions[EncodeEvent] = Mapping{Transform: expressionPointer(Expression{Op: "object", Fields: map[string]Expression{"changed": literalExpression(true)}})}
	target := compileTestDefinition(t, definition)
	if _, err := target.EncodeFrame(t.Context(), frame, EvaluationContext{}); !hasIssueCode(err, UnsupportedNative) {
		t.Fatalf("native optimization bypassed explicit encoder: %v", err)
	}
}

func TestSingleNativeEventEditPreservesUnmappedFields(t *testing.T) {
	definition := textVerificationDefinition(t, "single-edit")
	addEventVerification(t, &definition)
	definition.Native.Preserve = true
	compiled := compileTestDefinition(t, definition)
	value, _ := ParseValue([]byte(`{"event":"item.delta","item":"i","delta":"old","vendor":[null,false,9007199254740993]}`))
	frame, err := compiled.DecodeFrame(t.Context(), value, EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	frame.Events[0].Delta = StringValue("new")
	frames, err := compiled.EncodeFrame(t.Context(), frame, EvaluationContext{})
	if err != nil || len(frames) != 1 {
		t.Fatal(frames, err)
	}
	expected, _ := ParseValue([]byte(`{"event":"item.delta","item":"i","delta":"new","vendor":[null,false,9007199254740993]}`))
	if !equalValues(frames[0], expected) {
		t.Fatal("native fields overwrote edit", frames)
	}
}

func TestStreamVerificationComparesContentAndTerminalNotDeltaCount(t *testing.T) {
	definition := textVerificationDefinition(t, "stream-comparison")
	addEventVerification(t, &definition)
	compiled := compileTestDefinition(t, definition)
	start := Event{SchemaVersion: 1, Type: ItemStarted, ItemID: StringValue("text"), Item: &Node{Kind: TextNode}}
	end := Event{SchemaVersion: 1, Type: ResponseFinished}
	before := []Event{start, {SchemaVersion: 1, Type: ItemDelta, ItemID: StringValue("text"), Delta: StringValue("hello")}, end}
	after := []Event{start, {SchemaVersion: 1, Type: ItemDelta, ItemID: StringValue("text"), Delta: StringValue("he")}, {SchemaVersion: 1, Type: ItemDelta, ItemID: StringValue("text"), Delta: StringValue("llo")}, end}
	sample := Sample{ID: "reframed", Direction: DecodeEvent, Sequence: true}
	if err := compareEventSequence(compiled, sample, before, after); err != nil {
		t.Fatal(err)
	}
	after[2].Delta = StringValue("changed")
	if err := compareEventSequence(compiled, sample, before, after); err == nil {
		t.Fatal("changed content accepted as equivalent reframing")
	}
	if err := compareEventSequence(compiled, sample, before, before[:2]); err == nil {
		t.Fatal("missing terminal hidden by content comparison")
	}
}
