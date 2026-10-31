package protocol

import (
	"context"
	"errors"
	"testing"
)

func statefulEventPackage(t *testing.T) ProtocolPackage {
	t.Helper()
	document := eventProtocolPackage(t)
	schema := document.Spec.Documents["provider-event"].Schema
	schema.Required = []string{"kind", "seq"}
	document.Spec.Documents["provider-event"].Stream.InheritResponseID = true
	readID := readExpression("input", "/response_id")
	identity := Expression{Op: "if", When: &Expression{Op: "exists", Source: &readID}, Then: &readID,
		Otherwise: expressionPointer(readExpression("state", "/id"))}
	next := Expression{Op: "integer", Operation: "add", Items: []Expression{readExpression("state", "/count"), literalExpression(1)}}
	decode := document.Mapping.Directions[DecodeEvent]
	decode.State = map[string]ProtocolStateSlot{
		"id":       {Schema: ValueSchema{Type: StringType}, Update: identity},
		"count":    {Schema: ValueSchema{Type: IntegerType}, Initial: coreInteger(0), Update: next},
		"previous": {Schema: ValueSchema{Type: IntegerType}, Initial: coreInteger(0), Update: readExpression("state", "/count")},
	}
	decode.Program.Fields["responseId"] = identity
	decode.Effects = []ProtocolSemanticEffect{{ID: "declared-state", Kind: "reject", Path: "/responseId", Target: "/response_id", Reason: "state fixture", When: expressionPointer(literalExpression(false))}}
	document.Mapping.Directions[DecodeEvent] = decode
	encode := document.Mapping.Directions[EncodeEvent]
	encode.State = map[string]ProtocolStateSlot{"count": decode.State["count"]}
	document.Mapping.Directions[EncodeEvent] = encode
	return document
}

func TestPackageStreamStateSnapshotAndRollback(t *testing.T) {
	compiled := compilePackageForTest(t, statefulEventPackage(t))
	options := EvaluationContext{State: NewEvaluationState(), Scope: Scope{Provider: "local", Account: "a"}}
	for _, frame := range []Value{
		packageStreamFrame(ResponseStarted, 0, ""),
		{raw: `{"kind":"usage.updated","seq":1,"usage":{"input":3}}`},
	} {
		events, err := compiled.DecodeEvents(t.Context(), frame, options)
		if err != nil || len(events) != 1 || !events[0].ResponseID.IsString("r") {
			t.Fatal("response identity was not inherited", events, err)
		}
	}
	key := moduleStateKey{compiled: compiled, direction: DecodeEvent}
	retained := options.State.packageStates[key]
	// Slots compile in name order: count, id, previous. The last slot must see
	// the preceding count, not the update staged earlier in this same event.
	if retained.slots[0].raw != "2" || retained.slots[2].raw != "1" {
		t.Fatal("slot updates did not share a preceding snapshot", retained.slots)
	}
	if _, err := compiled.DecodeEvents(t.Context(), Value{raw: `{"kind":"not-an-event","seq":2}`}, options); err == nil {
		t.Fatal("invalid core event accepted")
	}
	if options.State.packageStates[key] != retained {
		t.Fatal("failed core validation committed staged updates")
	}
	otherScope := options
	otherScope.Scope.Account = "b"
	_, err := compiled.DecodeEvents(t.Context(), packageStreamFrame(ResponseFinished, 2, ""), otherScope)
	var failure *ConversionError
	if !errors.As(err, &failure) || failure.Issues[0].Code != ResourceScopeMismatch {
		t.Fatal("stream state crossed account scope", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := compiled.DecodeEvents(ctx, packageStreamFrame(ResponseFinished, 2, ""), options); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled decode changed its error", err)
	}
	if len(options.State.packageStates) != 0 || options.State.packageStateItems != 0 || options.State.packageStateBytes != 0 {
		t.Fatal("cancellation retained stream slots")
	}
}

func TestPackageStreamStateNativeFrameObservation(t *testing.T) {
	compiled := compilePackageForTest(t, statefulEventPackage(t))
	source := EvaluationContext{State: NewEvaluationState()}
	target := EvaluationContext{State: NewEvaluationState()}
	for _, raw := range []string{
		`{ "kind":"response.started", "response_id":"r", "seq":0 }`,
		`{"kind":"response.finished","seq":1}`,
	} {
		frame, err := compiled.DecodeFrame(t.Context(), Value{raw: raw}, source)
		if err != nil {
			t.Fatal(err)
		}
		frames, err := compiled.EncodeFrame(t.Context(), frame, target)
		if err != nil || len(frames) != 1 || frames[0].raw != raw {
			t.Fatal("stateful native observation changed frame bytes", frames, err)
		}
	}
	retained := target.State.packageStates[moduleStateKey{compiled: compiled, direction: EncodeEvent}]
	if retained == nil || retained.slots[0].raw != "2" {
		t.Fatal("native replay did not observe the semantic event sequence", retained)
	}
}

func TestPackageStreamStateCompilation(t *testing.T) {
	for _, edit := range []func(*ProtocolPackage){
		func(p *ProtocolPackage) {
			mapping := p.Mapping.Directions[DecodeRequest]
			mapping.State = p.Mapping.Directions[DecodeEvent].State
			p.Mapping.Directions[DecodeRequest] = mapping
		},
		func(p *ProtocolPackage) {
			p.Mapping.Directions[DecodeEvent].Program.Fields["responseId"] = readExpression("state", "/undeclared")
		},
		func(p *ProtocolPackage) {
			p.Mapping.Directions[DecodeEvent].State["id"] = ProtocolStateSlot{Schema: ValueSchema{Type: StringType}, Initial: coreInteger(1), Update: readExpression("state", "/id")}
		},
	} {
		document := statefulEventPackage(t)
		edit(&document)
		if err := document.Validate(DefaultLimits()); err == nil {
			t.Fatal("invalid state declaration compiled")
		}
	}
	compiled := compilePackageForTest(t, statefulEventPackage(t))
	if _, err := compiled.DecodeEvents(t.Context(), packageStreamFrame(ResponseStarted, 0, ""), EvaluationContext{}); err == nil {
		t.Fatal("unbound stateful event mapping executed")
	}
}
