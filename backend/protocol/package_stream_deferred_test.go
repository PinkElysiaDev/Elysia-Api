package protocol

import "testing"

func TestPackageDeferredEventRetainsTerminalMetadata(t *testing.T) {
	document := statefulEventPackage(t)
	document.Spec.Documents["provider-event"].Stream.Events[string(MetadataUpdated)] = ProtocolStreamEventSpec{{Action: "observe"}}
	mapping := document.Mapping.Directions[DecodeEvent]
	mapping.IsEventBatch, mapping.CanDeferEvents = true, true
	selected := readExpression("input", "/stop_reason")
	mapping.State["finish"] = ProtocolStateSlot{Schema: ValueSchema{Type: StringType},
		Update: Expression{Op: "if", When: &Expression{Op: "exists", Source: &selected}, Then: &selected, Otherwise: expressionPointer(readExpression("state", "/finish"))}}
	terminal := Expression{Op: "equal", Items: []Expression{readExpression("input", "/kind"), literalExpression(ResponseFinished)}}
	mapping.Program.Fields["response"] = Expression{Op: "if", When: &terminal, Then: expressionPointer(objectExpression(map[string]Expression{
		"schemaVersion": literalExpression(ProtocolCoreVersion), "status": literalExpression("completed"),
		"attributes": objectExpression(map[string]Expression{"finishReason": readExpression("state", "/finish")}),
	}))}
	isMetadata := Expression{Op: "equal", Items: []Expression{readExpression("input", "/kind"), literalExpression(MetadataUpdated)}}
	mapping.Program = Expression{Op: "if", When: &isMetadata, Then: &Expression{Op: "array", Items: []Expression{}}, Otherwise: &Expression{Op: "array", Items: []Expression{mapping.Program}}}
	document.Mapping.Directions[DecodeEvent] = mapping
	compiled := compilePackageForTest(t, document)
	validation, err := compiled.NewWireInputStreamValidation(Scope{})
	if err != nil {
		t.Fatal(err)
	}
	options := EvaluationContext{State: NewEvaluationState()}
	for index, raw := range []string{
		`{"kind":"response.started","response_id":"r","seq":0}`,
		`{"kind":"metadata.updated","response_id":"r","seq":1,"stop_reason":"length"}`,
		`{"kind":"response.finished","response_id":"r","seq":2}`,
	} {
		wire := Value{raw: raw}
		if err := validation.Consume(t.Context(), wire); err != nil {
			t.Fatal(err)
		}
		frame, err := compiled.DecodeFrame(t.Context(), wire, options)
		if err != nil {
			t.Fatal(err)
		}
		if index == 1 {
			if len(frame.Events) != 0 {
				t.Fatal("deferred control invented a generation event", frame.Events)
			}
			frames, err := compiled.EncodeFrame(t.Context(), frame, EvaluationContext{State: NewEvaluationState()})
			if err != nil || len(frames) != 1 || frames[0].raw != raw {
				t.Fatal("empty semantic batch lost its original native frame", frames, err)
			}
		}
		if index == 2 && (len(frame.Events) != 1 || frame.Events[0].Response == nil || !frame.Events[0].Response.Attributes["finishReason"].IsString("length")) {
			t.Fatal("deferred terminal reason was lost", frame.Events)
		}
	}
	if err := validation.Finish(); err != nil {
		t.Fatal(err)
	}
	validation, _ = compiled.NewWireInputStreamValidation(Scope{})
	if err := validation.Consume(t.Context(), packageStreamFrame(ResponseStarted, 0, "")); err != nil {
		t.Fatal(err)
	}
	if err := validation.Consume(t.Context(), packageStreamFrame(MetadataUpdated, 1, `,"stop_reason":"stop"`)); err != nil {
		t.Fatal(err)
	}
	if err := validation.Finish(); err == nil {
		t.Fatal("deferred event was mistaken for the real wire terminal")
	}
}

func TestPackageStreamControlDoesNotStartResponse(t *testing.T) {
	spec := sparseIdentityStream(t)
	spec.Events["ping"] = ProtocolStreamEventSpec{{Action: "control"}}
	state := newSparseIdentityValidation(t, spec)
	if err := state.consume(t.Context(), Value{raw: `{"type":"ping"}`}); err != nil {
		t.Fatal(err)
	}
	if state.accepted.hasStarted || state.identityBytes != 0 {
		t.Fatal("control frame established a response")
	}
	if err := state.finish(); err == nil {
		t.Fatal("control-only stream became a completed generation")
	}
	assertPackageRuntimeIssue(t, state.consume(t.Context(), Value{raw: `{"type":"message_stop"}`}), UpstreamContractViolation, "/type")
}
