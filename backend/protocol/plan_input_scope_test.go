package protocol

import (
	"reflect"
	"testing"
)

func TestInputScopePreservesRootAndWireLocations(t *testing.T) {
	program := Expression{Op: "with_input", Source: expressionPointer(readExpression("input", "/response")), Body: expressionPointer(objectExpression(map[string]Expression{
		"local": readExpression("input", "/tag"), "root": readExpression("root", "/tag"),
		"native": {Op: "capture_native", Source: expressionPointer(readExpression("input", ""))},
		"child":  {Op: "with_input", Source: expressionPointer(readExpression("input", "/output/0")), Body: expressionPointer(Expression{Op: "capture_native", Source: expressionPointer(readExpression("input", ""))})},
	}))}
	compiler := expressionCompiler{limits: DefaultLimits()}
	plan, err := compiler.compilePlan(program, "/program", expressionScope{wireCapture: true}, 1)
	if err != nil {
		t.Fatal(err)
	}
	input := Value{raw: `{"tag":"root","response":{"tag":"inner","output":[{"n":9007199254740993}]}}`}
	state := numericTestState(t, input)
	state.capture = &nativeCaptureState{}
	output, err := plan.evaluate(state)
	if err != nil {
		t.Fatal(err)
	}
	for pointer, want := range map[string]string{"/local": `"inner"`, "/root": `"root"`, "/native/source/path": `"/response"`, "/child/source/path": `"/response/output/0"`, "/child/value/n": `9007199254740993`} {
		parts, _ := parsePointer(pointer)
		value, err := readValuePointer(output, parts)
		if err != nil || value.raw != want {
			t.Fatal("input scope changed root or source location", pointer, value, err)
		}
	}
	if state.capture.hasInputScope || state.input != input || state.root != input {
		t.Fatal("input scope escaped its lexical body")
	}
	program.Source = &Expression{Op: "parse_json", Source: expressionPointer(literalExpression(`{"tag":"inner","output":[{}]}`))}
	compiler = expressionCompiler{limits: DefaultLimits()}
	plan, err = compiler.compilePlan(program, "/program", expressionScope{wireCapture: true}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := plan.evaluate(state); err == nil {
		t.Fatal("constructed input acquired original wire provenance")
	}
}

func TestInputScopeRetainsPlannedValues(t *testing.T) {
	compiler := expressionCompiler{limits: DefaultLimits()}
	program := Expression{Op: "with_input", Source: expressionPointer(bindingTestRead("selected", "")), Body: expressionPointer(readExpression("input", ""))}
	plan, err := compiler.compilePlan(program, "/program", expressionScope{bindings: map[string]expressionBinding{"selected": {slot: 0, result: ObjectType}}, bindingDepth: 1}, 1)
	if err != nil {
		t.Fatal(err)
	}
	selected := JSONObject(map[string]JSONOutput{"value": JSONLeaf(StringValue("selected"))})
	state := numericTestState(t, Value{raw: `{"value":"original"}`})
	state.bindings = []JSONOutput{selected}
	state.coreRequest = &Request{SchemaVersion: ProtocolCoreVersion}
	state.inputReader = func([]string) (JSONOutput, error) {
		t.Fatal("read escaped the selected input")
		return JSONOutput{}, nil
	}
	output, err := plan.build(state)
	if err != nil || output.output.node != selected.node {
		t.Fatal("input scope materialized or replaced the selected output tree", err)
	}
}

func TestInputScopeCoreAuthority(t *testing.T) {
	for _, fixture := range []struct {
		name   string
		source Expression
		from   string
		path   string
		valid  bool
	}{
		{"original-child", readExpression("input", "/tools/0"), "input", "/native", true},
		{"original-root", readExpression("input", "/tools/0"), "root", "/tools/0/native", true},
		{"opaque-child", readExpression("input", "/metadata"), "input", "/native", false},
		{"root-is-not-child", readExpression("input", "/tools/0"), "root", "/input/native", false},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			program := Expression{Op: "with_input", Source: &fixture.source, Body: &Expression{Op: "replay_native", Source: expressionPointer(readExpression(fixture.from, fixture.path))}}
			compiler := expressionCompiler{limits: DefaultLimits()}
			_, err := compiler.compilePlan(program, "/program", expressionScope{coreInput: reflect.TypeFor[Request](), canReadNative: true}, 1)
			if (err == nil) != fixture.valid {
				t.Fatal("input and root gained the wrong core authority", err)
			}
		})
	}
}
