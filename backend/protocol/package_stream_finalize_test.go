package protocol

import "testing"

func deferredTerminalPackage(t *testing.T) ProtocolPackage {
	t.Helper()
	document := eventProtocolPackage(t)
	mapping := document.Mapping.Directions[EncodeEvent]
	mapping.IsFrameBatch = true
	mapping.State = map[string]ProtocolStateSlot{
		"response": {Schema: ValueSchema{Type: StringType}, Update: readExpression("input", "/responseId")},
		"sequence": {Schema: ValueSchema{Type: IntegerType}, Update: readExpression("input", "/sequence")},
	}
	terminal := Expression{Op: "equal", Items: []Expression{readExpression("input", "/type"), literalExpression(ResponseFinished)}}
	mapping.Program = Expression{Op: "if", When: &terminal, Then: &Expression{Op: "array", Items: []Expression{}}, Otherwise: &Expression{Op: "array", Items: []Expression{mapping.Program}}}
	mapping.Finalize = &Expression{Op: "array", Items: []Expression{objectExpression(map[string]Expression{
		"kind": literalExpression(ResponseFinished), "response_id": readExpression("state", "/response"),
		"seq": {Op: "integer", Operation: "add", Items: []Expression{readExpression("state", "/sequence"), literalExpression(1)}},
	})}}
	mapping.Effects = []ProtocolSemanticEffect{{ID: "deferred-terminal", Kind: "reject", Path: "/type", Target: "/kind", Reason: "finalizer fixture", When: expressionPointer(literalExpression(false))}}
	document.Mapping.Directions[EncodeEvent] = mapping
	return document
}

func TestPackageStreamFinalizerPreservesUsageTail(t *testing.T) {
	compiled := compilePackageForTest(t, deferredTerminalPackage(t))
	validation, err := compiled.NewWireStreamValidation(Scope{})
	if err != nil {
		t.Fatal(err)
	}
	options := EvaluationContext{State: NewEvaluationState()}.WithOutputStream(validation)
	consume := func(frames []Value) {
		t.Helper()
		for _, frame := range frames {
			if err := validation.Consume(t.Context(), frame); err != nil {
				t.Fatal(err)
			}
		}
		if len(frames) != 0 {
			if err := validation.CommitDelivery(); err != nil {
				t.Fatal(err)
			}
		}
	}
	for index, kind := range []EventType{ResponseStarted, ResponseFinished, UsageUpdated} {
		event := Event{SchemaVersion: ProtocolCoreVersion, Type: kind, ResponseID: StringValue("r"), Sequence: coreInteger(int64(index))}
		if kind == UsageUpdated {
			event.Usage = &Usage{Input: &Counter{Count: 7, Origin: ObservedCount}}
		}
		frames, err := compiled.EncodeFrames(t.Context(), event, options)
		if err != nil {
			t.Fatal(err)
		}
		if kind == ResponseFinished && len(frames) != 0 {
			t.Fatal("terminal was emitted before usage tail", frames)
		}
		consume(frames)
	}
	frames, err := compiled.FinishEvents(t.Context(), options)
	if err != nil || len(frames) != 1 {
		t.Fatal("finalizer did not emit one deferred terminal", frames, err)
	}
	consume(frames)
	if err := validation.Finish(); err != nil {
		t.Fatal(err)
	}
	if len(options.State.packageStates) != 0 || options.State.packageStateItems != 0 || options.State.packageStateBytes != 0 {
		t.Fatal("finalization retained mapping slots")
	}
	frames, err = compiled.FinishEvents(t.Context(), options)
	if err != nil || len(frames) != 0 {
		t.Fatal("repeated finalization emitted another terminal", frames, err)
	}
}

func TestPackageStreamFinalizerCannotInventSuccess(t *testing.T) {
	compiled := compilePackageForTest(t, deferredTerminalPackage(t))
	options := EvaluationContext{State: NewEvaluationState()}
	if _, err := compiled.EncodeFrames(t.Context(), Event{SchemaVersion: ProtocolCoreVersion, Type: ResponseStarted, ResponseID: StringValue("r"), Sequence: coreInteger(0)}, options); err != nil {
		t.Fatal(err)
	}
	if frames, err := compiled.FinishEvents(t.Context(), options); err == nil || len(frames) != 0 {
		t.Fatal("early EOF fabricated success", frames, err)
	}
	if len(options.State.packageStates) != 0 {
		t.Fatal("failed finalization retained state")
	}
	document := deferredTerminalPackage(t)
	mapping := document.Mapping.Directions[EncodeEvent]
	mapping.Finalize.Items[0].Fields["response_id"] = readExpression("input", "/responseId")
	document.Mapping.Directions[EncodeEvent] = mapping
	if err := document.Validate(DefaultLimits()); err == nil {
		t.Fatal("finalizer read a nonexistent current event")
	}
}

func TestPackageStreamNativeOnlyFinalizer(t *testing.T) {
	compiled := compilePackageForTest(t, deferredTerminalPackage(t))
	source, target := EvaluationContext{State: NewEvaluationState()}, EvaluationContext{State: NewEvaluationState()}
	for index, kind := range []EventType{ResponseStarted, ResponseFinished} {
		wire := packageStreamFrame(kind, index, "")
		frame, err := compiled.DecodeFrame(t.Context(), wire, source)
		if err != nil {
			t.Fatal(err)
		}
		frames, err := compiled.EncodeFrame(t.Context(), frame, target)
		if err != nil || len(frames) != 1 || frames[0].raw != wire.raw {
			t.Fatal("native-only flow changed original frames", frames, err)
		}
	}
	if frames, err := compiled.FinishEvents(t.Context(), target); err != nil || len(frames) != 0 {
		t.Fatal("native observation generated an extra terminal", frames, err)
	}
	if len(target.State.packageStates) != 0 {
		t.Fatal("native-only finalization retained slots")
	}
}
