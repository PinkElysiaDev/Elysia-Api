package protocol

import "testing"

func TestPackageNativeObserverRetainsActualWirePosition(t *testing.T) {
	document := eventProtocolPackage(t)
	entry := document.Mapping.Directions[EncodeEvent]
	entry.Indexes = map[string]ProtocolStateIndex{"positions": {KeyMode: "string", Policy: "reject", Schema: ValueSchema{Type: IntegerType}}}
	isStart := Expression{Op: "equal", Items: []Expression{readExpression("input", "/type"), literalExpression(ItemStarted)}}
	key := readExpression("input", "/itemId")
	remember := func(position Expression) Expression {
		return Expression{Op: "if", When: &isStart, Then: &Expression{Op: "remember", Name: "positions", Source: &key, Body: &position}}
	}
	observe := remember(readExpression("runtime", "/nativeFrame/position"))
	entry.Observe = &observe
	entry.Program.Fields["position"] = Expression{Op: "if", When: &Expression{Op: "exists", Source: &key}, Then: &Expression{Op: "recall", Name: "positions", Source: &key, Policy: "reject"}}
	program := entry.Program
	registration := remember(literalExpression(0))
	entry.Program = Expression{Op: "let", Name: "registered", Source: &registration, Body: &program}
	document.Mapping.Directions[EncodeEvent] = entry
	compiled := compilePackageForTest(t, document)
	source, target := EvaluationContext{State: NewEvaluationState()}, EvaluationContext{State: NewEvaluationState()}
	defer source.State.Release()
	defer target.State.Release()
	for _, wire := range []Value{
		packageStreamFrame(ResponseStarted, 0, ""),
		packageStreamFrame(ItemStarted, 1, `,"item_id":"text","position":7`),
	} {
		frame, err := compiled.DecodeFrame(t.Context(), wire, source)
		if err != nil {
			t.Fatal(err)
		}
		frames, err := compiled.EncodeFrame(t.Context(), frame, target)
		if err != nil || len(frames) != 1 || frames[0].raw != wire.raw {
			t.Fatal("native observation rendered or duplicated the frame", frames, err)
		}
	}
	frame, err := compiled.DecodeFrame(t.Context(), packageStreamFrame(ItemDelta, 2, `,"item_id":"text","text":"original"`), source)
	if err != nil {
		t.Fatal(err)
	}
	events := append([]Event(nil), frame.Events...)
	events[0].Delta = StringValue("edited")
	frames, err := compiled.EncodeConvertedFrame(t.Context(), frame, events, target)
	if err != nil || len(frames) != 1 {
		t.Fatal(frames, err)
	}
	position, err := readValuePointer(frames[0], []string{"position"})
	if err != nil || position.raw != "7" {
		t.Fatal("semantic continuation lost the native target position", frames[0].raw, err)
	}
	entry.Observe = nil
	document.Mapping.Directions[EncodeEvent] = entry
	if err := document.Validate(DefaultLimits()); err != nil {
		// Validate shares the contract compiler; executable native replay policy
		// is checked by CompilePackage below.
		t.Fatal(err)
	}
	raw, err := EncodeValue(document)
	if err != nil {
		t.Fatal(err)
	}
	compiler, _ := NewCompiler(DefaultLimits(), nil, nil)
	if compiled, issues := compiler.CompilePackage(raw.Bytes()); compiled != nil || IssuesError(issues) == nil {
		t.Fatal("stateful native renderer compiled without an observer", issues)
	}
	entry.Observe = &observe
	entry.Program = program
	entry.Program.Fields["position"] = readExpression("runtime", "/nativeFrame/position")
	document.Mapping.Directions[EncodeEvent] = entry
	if err := document.Validate(DefaultLimits()); err == nil {
		t.Fatal("ordinary rendering read private native observation input")
	}
}
