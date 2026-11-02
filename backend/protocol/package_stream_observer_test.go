package protocol

import (
	"strings"
	"testing"
)

func TestPackageNativePredicateEncodingHistory(t *testing.T) {
	document := eventProtocolPackage(t)
	entry := document.Mapping.Directions[EncodeEvent]
	entry.IsFrameBatch, entry.NativeReplay = true, "original_frame"
	entry.Observe = &Expression{Op: "omit"}
	entry.Program = Expression{Op: "array", Items: []Expression{entry.Program}}
	entry.Effects[0].Target = "/0/response_id"
	history := readExpression("runtime", "/encoding/hasRendered")
	entry.NativeWhen = &Expression{Op: "any", Items: []Expression{
		{Op: "not", Source: &history},
		{Op: "equal", Items: []Expression{readExpression("input", "/kind"), literalExpression(ItemDelta)}},
	}}
	document.Mapping.Directions[EncodeEvent] = entry
	compiled := compilePackageForTest(t, document)
	source, target := EvaluationContext{State: NewEvaluationState()}, EvaluationContext{State: NewEvaluationState()}
	defer source.State.Release()
	defer target.State.Release()
	for index, fixture := range []struct {
		kind          EventType
		fields        string
		edit, rebuild bool
	}{
		{ResponseStarted, "", false, false},
		{ItemStarted, `,"item_id":"text"`, false, false},
		{ItemDelta, `,"item_id":"text","text":"original"`, true, true},
		{ItemDelta, `,"item_id":"text","text":"\u006eative"`, false, false},
		{ItemFinished, `,"item_id":"text"`, false, true},
		{ResponseFinished, "", false, true},
	} {
		wire := packageStreamFrame(fixture.kind, index, fixture.fields+`,"extension":9007199254740993`)
		frame, err := compiled.DecodeFrame(t.Context(), wire, source)
		if err != nil {
			t.Fatal(err)
		}
		events := append([]Event(nil), frame.Events...)
		if fixture.edit {
			events[0].Delta = StringValue("edited")
		}
		frames, err := compiled.EncodeConvertedFrame(t.Context(), frame, events, target)
		if err != nil || len(frames) != 1 || (frames[0] != wire) != fixture.rebuild {
			t.Fatal("replay selection lost the prior encoding fact", index, frames, err)
		}
	}
}

func TestPackageNativePredicateClientOutput(t *testing.T) {
	document := eventProtocolPackage(t)
	entry := document.Mapping.Directions[EncodeEvent]
	entry.IsFrameBatch, entry.NativeReplay = true, "original_frame"
	entry.Observe = &Expression{Op: "omit"}
	entry.Program = Expression{Op: "array", Items: []Expression{entry.Program}}
	entry.Effects[0].Target = "/0/response_id"
	document.Mapping.Expressions = map[string]Expression{"allow-native": {Op: "equal", Items: []Expression{readExpression("runtime", "/clientOutput/includeUsage"), literalExpression(true)}}}
	entry.NativeWhen = &Expression{Op: "ref", Ref: "allow-native"}
	document.Mapping.Directions[EncodeEvent] = entry
	compiled := compilePackageForTest(t, document)
	source := EvaluationContext{State: NewEvaluationState()}
	defer source.State.Release()
	wire := packageStreamFrame(ResponseStarted, 0, `,"extension":9007199254740993`)
	frame, err := compiled.DecodeFrame(t.Context(), wire, source)
	if err != nil {
		t.Fatal(err)
	}
	for _, fixture := range []struct {
		name   string
		output *ClientOutput
		replay bool
	}{
		{name: "absent"},
		{name: "unspecified", output: &ClientOutput{}},
		{name: "excluded", output: &ClientOutput{IncludeUsage: boolPtr(false)}},
		{name: "included", output: &ClientOutput{IncludeUsage: boolPtr(true)}, replay: true},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			target := EvaluationContext{State: NewEvaluationState(), ClientOutput: fixture.output}
			defer target.State.Release()
			frames, err := compiled.EncodeFrame(t.Context(), frame, target)
			if err != nil || len(frames) != 1 {
				t.Fatal("native predicate changed the event count", frames, err)
			}
			if fixture.replay {
				if frames[0] != wire {
					t.Fatal("allowed native frame was rebuilt", frames[0])
				}
			} else if !equalValues(frames[0], packageStreamFrame(ResponseStarted, 0, "")) {
				t.Fatal("excluded native frame bypassed the declared renderer", frames[0])
			}
		})
	}
	for _, path := range []string{"/scope/model", "/interleavedThinking", "/operation/id", "/frame/usage", "/nativeFrame", "/stream/sequence", "/emission/offset", "/delivered/maxItemIndex"} {
		entry.NativeWhen = &Expression{Op: "exists", Source: expressionPointer(readExpression("runtime", path))}
		document.Mapping.Directions[EncodeEvent] = entry
		if err := document.Validate(DefaultLimits()); err == nil || !strings.Contains(err.Error(), "runtime metadata requires") {
			t.Fatal("native predicate gained unrelated runtime access", path, err)
		}
	}
	entry.NativeWhen = &Expression{Op: "exists", Source: expressionPointer(readExpression("state", "/count"))}
	document.Mapping.Directions[EncodeEvent] = entry
	if err := document.Validate(DefaultLimits()); err == nil || !strings.Contains(err.Error(), "state slot is not declared") {
		t.Fatal("native predicate gained stream state access", err)
	}
}

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
