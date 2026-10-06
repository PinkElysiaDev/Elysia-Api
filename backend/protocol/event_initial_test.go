package protocol

import "testing"

func initialEventDefinition(t *testing.T) Definition {
	t.Helper()
	definition := textVerificationDefinition(t, "initial-events")
	definition.Directions[DecodeEvent] = Mapping{
		Initial:   &Expression{Op: "literal", Value: fixtureValue(t, map[string]any{"type": "item.started", "itemId": "text", "item": map[string]any{"kind": "text", "payload": ""}})},
		Transform: &Expression{Op: "read", Path: "/events", Required: true},
	}
	return definition
}

func TestDeclaredInitialEventsAreOncePerStreamAndRevision(t *testing.T) {
	definition := initialEventDefinition(t)
	compiled := compileTestDefinition(t, definition)
	state := NewEvaluationState()
	options := EvaluationContext{State: state}
	empty := fixtureValue(t, map[string]any{"events": []any{}})
	if frame, err := compiled.DecodeFrame(t.Context(), empty, options); err != nil || len(frame.Events) != 0 {
		t.Fatal("explicit control frame initialized a content item", frame, err)
	}
	delta := fixtureValue(t, map[string]any{"events": map[string]any{"type": "item.delta", "itemId": "text", "delta": "hello"}})
	for index, count := range []int{2, 1} {
		frame, err := compiled.DecodeFrame(t.Context(), delta, options)
		if err != nil || len(frame.Events) != count || frame.Events[count-1].Type != ItemDelta {
			t.Fatal(index, frame, err)
		}
	}
	for _, fresh := range []struct {
		compiled *Compiled
		options  EvaluationContext
	}{
		{compiled, EvaluationContext{State: NewEvaluationState()}},
		{compileTestDefinition(t, definition), options},
	} {
		frame, err := fresh.compiled.DecodeFrame(t.Context(), delta, fresh.options)
		if err != nil || len(frame.Events) != 2 {
			t.Fatal("initialization leaked across stream or revision", frame, err)
		}
	}
	if _, err := compiled.DecodeFrame(t.Context(), delta, EvaluationContext{}); err == nil {
		t.Fatal("stateful mapping accepted no owner")
	}
}

func TestInitialEventsRejectWrongDirectionAndInvalidShape(t *testing.T) {
	compiler, err := NewCompiler(DefaultLimits(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, direction := range []Direction{DecodeRequest, EncodeRequest, DecodeResponse, EncodeResponse, EncodeEvent} {
		definition := initialEventDefinition(t)
		mapping := definition.Directions[DecodeEvent]
		delete(definition.Directions, DecodeEvent)
		definition.Directions[direction] = mapping
		if compiled, issues := compiler.Compile(fixtureValue(t, definition).Bytes()); compiled != nil || len(issues) == 0 {
			t.Fatal("initial accepted outside event decoding", direction)
		}
	}
	definition := initialEventDefinition(t)
	mapping := definition.Directions[DecodeEvent]
	mapping.Initial = &Expression{Op: "literal", Value: StringValue("not-an-event")}
	definition.Directions[DecodeEvent] = mapping
	if compiled, issues := compiler.Compile(fixtureValue(t, definition).Bytes()); compiled != nil || len(issues) == 0 {
		t.Fatal("initial accepted a string")
	}
}
