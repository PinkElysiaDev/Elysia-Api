package protocol

import (
	"crypto/sha256"
	"slices"
	"strings"
	"testing"
)

func TestSemanticDigestMatchesValueEncoding(t *testing.T) {
	for _, payload := range []Value{
		StringValue("small"),
		StringValue(strings.Repeat("large", 32<<10)),
		parseTestValue(t, ` { "number":9007199254740993,"null":null,"escape":"<>&\u2028" } `),
	} {
		events := []Event{{SchemaVersion: SemanticSchemaVersion, Type: ItemDelta, ItemID: StringValue("id"), Delta: payload}}
		encoded, err := EncodeValue(events)
		if err != nil {
			t.Fatal(err)
		}
		got, err := digestSemanticEvents(events)
		if err != nil || got != sha256.Sum256(encoded.Bytes()) {
			t.Fatalf("single-buffer digest differs from Value encoding: %v", err)
		}
		events[0].Delta = StringValue("changed")
		changed, err := digestSemanticEvents(events)
		if err != nil || changed == got {
			t.Fatal("digest missed an external event mutation")
		}
	}
}

func TestEventFrameReplayUsesSharedWireDigest(t *testing.T) {
	definition := compoundFrameDefinition(t)
	definition.Mapping.Native.Preserve = false
	compiled := compileTestDefinition(t, definition)
	wire := parseTestValue(t, `{"events":[{"schemaVersion":2,"type":"metadata.updated","sequence":1}],"extension":9007199254740993}`)
	frame, err := compiled.DecodeFrame(t.Context(), wire, EvaluationContext{State: NewEvaluationState()})
	if err != nil {
		t.Fatal(err)
	}
	if frame.native != nil || !frame.hasWire || frame.wireDigest != sha256.Sum256(wire.Bytes()) {
		t.Fatal("wire identity depends on native replay or semantic serialization")
	}
	for range 2 {
		replay, err := NewEventReplay(replayTarget(), DefaultLimits())
		if err != nil {
			t.Fatal(err)
		}
		defer replay.Release()
		if accepted, err := frame.ConsumeEvent(replay, 0, frame.Events[0]); err != nil || !accepted {
			t.Fatal("first wire frame rejected", accepted, err)
		}
		projected := frame.Events[0]
		projected.ResponseID = StringValue("projection")
		if accepted, err := frame.ConsumeEvent(replay, 0, projected); err != nil || accepted {
			t.Fatal("wire replay depends on projected semantics", accepted, err)
		}
		for _, changed := range []string{
			` { "events":[{"schemaVersion":2,"type":"metadata.updated","sequence":1}],"extension":9007199254740993}`,
			`{"events":[{"schemaVersion":2,"type":"metadata.updated","sequence":1}],"extension":9007199254740994}`,
		} {
			other := &EventFrame{Events: frame.Events}
			other.hashWire(parseTestValue(t, changed))
			if _, err := other.ConsumeEvent(replay, 0, projected); !hasIssueCode(err, UpstreamContractViolation) {
				t.Fatal("differently spelled wire retransmission was deduplicated", err)
			}
		}
	}
}

func TestEventFrameReplayDistinguishesCompoundMembers(t *testing.T) {
	event := Event{SchemaVersion: ProtocolCoreVersion, Type: MetadataUpdated, Sequence: coreInteger(1)}
	frame := &EventFrame{Events: []Event{event, event}}
	frame.hashWire(parseTestValue(t, `{"events":[1,1]}`))
	replay, _ := NewEventReplay(replayTarget(), DefaultLimits())
	defer replay.Release()
	if accepted, err := frame.ConsumeEvent(replay, 0, event); err != nil || !accepted {
		t.Fatal(accepted, err)
	}
	if _, err := frame.ConsumeEvent(replay, 1, event); !hasIssueCode(err, UpstreamContractViolation) {
		t.Fatal("same sequence hid another compound member", err)
	}
	if accepted, err := frame.ConsumeEvent(replay, 0, event); err != nil || accepted {
		t.Fatal("identical compound member was not deduplicated", accepted, err)
	}
}

func TestConvertedFrameChecksNativeAtEncodingBoundary(t *testing.T) {
	compiled := compileTestDefinition(t, compoundFrameDefinition(t))
	value, _ := ParseValue([]byte(`{"events":[{"schemaVersion":2,"type":"item.delta","itemId":"a","delta":"hello"},{"schemaVersion":2,"type":"item.delta","itemId":"a","delta":"world"}],"vendor":9007199254740993}`))
	frame, err := compiled.DecodeFrame(t.Context(), value, EvaluationContext{State: NewEvaluationState()})
	if err != nil {
		t.Fatal(err)
	}
	events := slices.Clone(frame.Events)
	frames, err := compiled.EncodeConvertedFrame(t.Context(), frame, events, EvaluationContext{State: NewEvaluationState()})
	if err != nil || len(frames) != 1 || frames[0] != value {
		t.Fatal("unchanged native frame lost", frames, err)
	}
	events[0].Delta = StringValue("edited")
	frames, err = compiled.EncodeConvertedFrame(t.Context(), frame, events, EvaluationContext{State: NewEvaluationState()})
	if err != nil || len(frames) != 2 || frames[0] == value {
		t.Fatal("conversion edit lost", frames, err)
	}
	frame.Events[0].Delta = StringValue("externally edited")
	if _, err := compiled.EncodeFrame(t.Context(), frame, EvaluationContext{State: NewEvaluationState()}); err == nil {
		t.Fatal("earlier conversion cached a stale equality proof")
	}
}

func compoundFrameDefinition(t *testing.T) ProtocolPackage {
	t.Helper()
	definition := textVerificationDefinition(t, "compound")
	definition.Mapping.Native.Preserve = true
	definition.Mapping.Directions[DecodeEvent] = ProtocolDirectionMapping{Effects: generationFixtureEffects(DecodeEvent), Document: testDocument(DecodeEvent), IsEventBatch: true, Program: readExpression("input", "/events")}
	definition.Mapping.Directions[EncodeEvent] = ProtocolDirectionMapping{Effects: generationFixtureEffects(EncodeEvent), Document: testDocument(EncodeEvent), Program: Expression{Op: "object", Fields: map[string]Expression{"events": {Op: "array", Items: []Expression{readExpression("input", "")}}}}}
	return definition
}

func TestCompoundFrameRoundTripPreservesOneNativeFrame(t *testing.T) {
	compiled := compileTestDefinition(t, compoundFrameDefinition(t))
	value, err := ParseValue([]byte(`{"events":[{"schemaVersion":2,"type":"item.started","itemId":"a","item":{"kind":"text"}},{"schemaVersion":2,"type":"item.delta","itemId":"a","delta":"hello"},{"schemaVersion":2,"type":"response.finished"}],"unknown":[null,false,9007199254740993]}`))
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
	value, _ := ParseValue([]byte(`{"events":[{"schemaVersion":2,"type":"response.finished"}]}`))
	frame, err := source.DecodeFrame(t.Context(), value, EvaluationContext{State: NewEvaluationState()})
	if err != nil {
		t.Fatal(err)
	}
	definition.Spec.ID = "different-encoder"
	definition.Mapping.Directions[EncodeEvent] = ProtocolDirectionMapping{Effects: generationFixtureEffects(EncodeEvent), Document: testDocument(EncodeEvent), Program: Expression{Op: "object", Fields: map[string]Expression{"changed": literalExpression(true)}}}
	target := compileTestDefinition(t, definition)
	if _, err := target.EncodeFrame(t.Context(), frame, EvaluationContext{State: NewEvaluationState()}); !hasIssueCode(err, UnsupportedNative) {
		t.Fatalf("native optimization bypassed explicit encoder: %v", err)
	}
}

func TestSingleNativeEventEditPreservesUnmappedFields(t *testing.T) {
	definition := textVerificationDefinition(t, "single-edit")
	addEventVerification(t, &definition)
	definition.Mapping.Native.Preserve = true
	compiled := compileTestDefinition(t, definition)
	value, _ := ParseValue([]byte(`{"event":"item.delta","item":"i","delta":"old","vendor":[null,false,9007199254740993]}`))
	frame, err := compiled.DecodeFrame(t.Context(), value, EvaluationContext{State: NewEvaluationState()})
	if err != nil {
		t.Fatal(err)
	}
	frame.Events[0].Delta = StringValue("new")
	frames, err := compiled.EncodeFrame(t.Context(), frame, EvaluationContext{State: NewEvaluationState()})
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
	start := Event{SchemaVersion: SemanticSchemaVersion, Type: ItemStarted, ItemID: StringValue("text"), Item: &Node{Kind: TextNode}}
	end := Event{SchemaVersion: SemanticSchemaVersion, Type: ResponseFinished}
	before := []Event{start, {SchemaVersion: SemanticSchemaVersion, Type: ItemDelta, ItemID: StringValue("text"), Delta: StringValue("hello")}, end}
	after := []Event{start, {SchemaVersion: SemanticSchemaVersion, Type: ItemDelta, ItemID: StringValue("text"), Delta: StringValue("he")}, {SchemaVersion: SemanticSchemaVersion, Type: ItemDelta, ItemID: StringValue("text"), Delta: StringValue("llo")}, end}
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
