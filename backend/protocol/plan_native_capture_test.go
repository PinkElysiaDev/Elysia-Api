package protocol

import (
	"errors"
	"strings"
	"testing"
)

func TestNativeCaptureMeasuresItsEncodedRepresentation(t *testing.T) {
	compiler := expressionCompiler{limits: DefaultLimits()}
	plan, err := compiler.compilePlan(Expression{Op: "capture_native", Source: expressionPointer(readExpression("input", ""))}, "/capture", expressionScope{wireCapture: true}, 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{`"<&>"`, ` { "text" : "<" } `} {
		state := numericTestState(t, Value{raw: raw})
		state.capture = &nativeCaptureState{}
		planned, err := plan.build(state)
		if err != nil {
			t.Fatal(err)
		}
		wire, err := planned.output.Encode(t.Context())
		if err != nil || wire.Len() != planned.bytes {
			t.Fatal("capture did not measure the normalized wire bytes", wire.Len(), planned.bytes, err)
		}
		selected, err := readOutputPointer(planned.output, []string{"value"}, nil)
		decoded, decodeErr := readValuePointer(wire, []string{"value"})
		if err != nil || decodeErr != nil || selected.value != decoded {
			t.Fatal("core writer and JSON materialization saw different snapshots", err, decodeErr)
		}
		state.budget.limits.BufferBytes = wire.Len() - 1
		_, err = plan.build(state)
		var limit *ResourceLimitError
		if !errors.As(err, &limit) || limit.Metric != "bytes" || limit.Observed != wire.Len() {
			t.Fatal("capture bypassed its exact output budget", err)
		}
	}
}

func TestPackageNativeCaptureUsesRuntimeProvenance(t *testing.T) {
	document := textProtocolPackage(t)
	document.Mapping.Native.Preserve = true
	message := document.Mapping.Directions[DecodeRequest].Program.Fields["content"].Items[0]
	message.Fields["children"].Items[0].Fields["native"] = Expression{Op: "capture_native", Source: expressionPointer(readExpression("input", "/prompt"))}
	compiled := compilePackageForTest(t, document)
	input := Value{raw: `{"model":"m", "prompt":"hello"}`}
	initial := Scope{Provider: "synthetic", Account: "account"}
	bound := initial
	bound.Model = "resolved-model"
	calls := 0
	options := EvaluationContext{Scope: initial, Values: Object{"source": fixtureValue(t, Provenance{Protocol: Identity{DefinitionID: "forged"}})}, ResolveRequestScope: func(*Request) (Scope, error) { calls++; return bound, nil }}
	request, err := compiled.DecodeRequestValue(t.Context(), input, options)
	if err != nil {
		t.Fatal(err)
	}
	leaf := request.Content[0].Children[0]
	if leaf.Native == nil || leaf.Native.Source != (Provenance{Protocol: compiled.Identity(), Direction: DecodeRequest, Scope: bound, Path: "/prompt"}) || leaf.Native.Value != StringValue("hello") || calls != 1 {
		t.Fatal("capture accepted authored provenance or repeated authorization", leaf, calls)
	}
	value, issues := compiled.Execute(t.Context(), DecodeRequest, input, options)
	var rawRequest Request
	if err := IssuesError(issues); err != nil {
		t.Fatal(err)
	}
	if err := value.Decode(&rawRequest); err != nil || rawRequest.Content[0].Children[0].Native.Source.Scope != initial || calls != 1 {
		t.Fatal("raw program did not use its invocation scope", err)
	}
	document.Mapping.Native.Preserve = false
	disabled := compilePackageForTest(t, document)
	request, err = disabled.DecodeRequestValue(t.Context(), input, EvaluationContext{})
	if err != nil || request.Content[0].Children[0].Native != nil {
		t.Fatal("disabled preservation captured wire assets", err)
	}
}

func TestNativeCaptureTracksRecursiveSourceLocations(t *testing.T) {
	item := readExpression("item", "")
	bound := bindingTestRead("node", "")
	children := bindingTestRead("node", "/content")
	body := objectExpression(map[string]Expression{
		"native":   {Op: "capture_native", Source: &bound},
		"children": {Op: "if", When: expressionPointer(Expression{Op: "exists", Source: &children}), Then: expressionPointer(Expression{Op: "recur", Source: &children})},
	})
	walk := Expression{Op: "walk", Source: expressionPointer(readExpression("input", "/content")), Body: expressionPointer(Expression{Op: "let", Name: "node", Source: &item, Body: &body})}
	compiler := expressionCompiler{limits: DefaultLimits()}
	plan, err := compiler.compilePlan(walk, "/capture", expressionScope{wireCapture: true}, 1)
	if err != nil {
		t.Fatal(err)
	}
	input := Value{raw: `{"content":[{"content":[{"label":"first"},{"content":[{"label":"nested"}]}]},{"label":"last"}]}`}
	state := numericTestState(t, input)
	state.capture = &nativeCaptureState{source: Provenance{Protocol: Identity{DefinitionID: "source"}, Direction: DecodeRequest}}
	output, err := plan.evaluate(state)
	if err != nil {
		t.Fatal(err)
	}
	var nodes []Node
	if err := output.Decode(&nodes); err != nil {
		t.Fatal(err)
	}
	for _, pair := range []struct {
		node Node
		path string
	}{
		{nodes[0], "/content/0"}, {nodes[0].Children[0], "/content/0/content/0"},
		{nodes[0].Children[1].Children[0], "/content/0/content/1/content/0"}, {nodes[1], "/content/1"},
	} {
		pointer, _ := parsePointer(pair.path)
		original, err := readValuePointer(input, pointer)
		if err != nil || pair.node.Native == nil || pair.node.Native.Source.Path != pair.path || pair.node.Native.Value != original {
			t.Fatal("recursive capture lost the actual wire location", pair.path, pair.node.Native, err)
		}
	}
	for _, location := range state.capture.bindings {
		if location != (nativeLocation{}) {
			t.Fatal("binding origin survived execution", location)
		}
	}
}

func TestNativeCaptureRejectsInventedOrigins(t *testing.T) {
	read := readExpression("item", "")
	capture := Expression{Op: "capture_native", Source: &read}
	for _, source := range []Expression{
		{Op: "filter", Source: expressionPointer(readExpression("input", "/content")), When: expressionPointer(literalExpression(true))},
		{Op: "array", Items: []Expression{literalExpression(map[string]any{"label": "same"})}},
	} {
		compiler := expressionCompiler{limits: DefaultLimits()}
		plan, err := compiler.compilePlan(Expression{Op: "map", Source: &source, Body: &capture}, "/capture", expressionScope{wireCapture: true}, 1)
		if err != nil {
			t.Fatal(err)
		}
		state := numericTestState(t, Value{raw: `{"content":[{"label":"same"}]}`})
		state.capture = &nativeCaptureState{}
		if _, err = plan.evaluate(state); err == nil || !strings.Contains(err.Error(), "proven original wire location") {
			t.Fatal("synthetic or filtered position was trusted as original", err)
		}
	}
	for _, expression := range []Expression{
		{Op: "capture_native", Source: expressionPointer(readExpression("context", "/forged"))},
		{Op: "capture_native", Source: expressionPointer(literalExpression("/content/0"))},
	} {
		compiler := expressionCompiler{limits: DefaultLimits()}
		if _, err := compiler.compilePlan(expression, "/capture", expressionScope{wireCapture: true}, 1); err == nil {
			t.Fatal("non-wire source was accepted")
		}
	}
	compiler := expressionCompiler{limits: DefaultLimits()}
	if _, err := compiler.compilePlan(Expression{Op: "capture_native", Source: expressionPointer(readExpression("input", ""))}, "/capture", expressionScope{}, 1); err == nil {
		t.Fatal("capture escaped the package decoder")
	}
}
