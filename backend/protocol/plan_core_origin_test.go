package protocol

import (
	"reflect"
	"strings"
	"testing"
)

func TestNativeReplayPreservesOriginalCoreReads(t *testing.T) {
	replay := func(source Expression) Expression { return Expression{Op: "replay_native", Source: &source} }
	tools := readExpression("input", "/tools")
	identityMap := Expression{Op: "map", Source: &tools, Body: expressionPointer(readExpression("item", ""))}
	filtered := Expression{Op: "filter", Source: &identityMap, When: expressionPointer(literalExpression(true))}
	optional := Expression{Op: "if", When: &Expression{Op: "present", Source: &tools}, Then: &filtered, Otherwise: &Expression{Op: "array", Items: []Expression{}}}
	for _, tc := range []struct {
		name    string
		program Expression
		isArray bool
	}{
		{"read", replay(readExpression("input", "/tools/0/native")), false},
		{"root", replay(readExpression("root", "/tools/0/native")), false},
		{"binding", Expression{Op: "let", Name: "original", Source: expressionPointer(readExpression("input", "/tools/0/native")), Body: expressionPointer(replay(bindingTestRead("original", "")))}, false},
		{"iteration", Expression{Op: "map", Source: &optional, Body: expressionPointer(Expression{Op: "let", Name: "tool", Source: expressionPointer(readExpression("item", "")), Body: expressionPointer(replay(bindingTestRead("tool", "/native")))})}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			document := textProtocolPackage(t)
			document.Mapping.Capabilities[ServerToolsCapability] = true
			document.Spec.WireContracts[EncodeRequest].Schema.AllowUnknown = true
			document.Mapping.Directions[EncodeRequest].Program.Fields["extension"] = tc.program
			compiled := compilePackageForTest(t, document)
			options := EvaluationContext{Scope: Scope{Provider: "local", Account: "a"}}
			request, err := compiled.DecodeRequestValue(t.Context(), Value{raw: `{"model":"m","prompt":"hello"}`}, options)
			if err != nil {
				t.Fatal(err)
			}
			snapshot := Value{raw: `{"kind":"synthetic","counter":9007199254740993}`}
			request.Tools = []Tool{{Kind: ServerTool, Native: &Native{Source: Provenance{Protocol: compiled.Identity(), Direction: DecodeRequest, Scope: options.Scope, Path: "/tools/0"}, Value: snapshot}}}
			raw, issues := compiled.Execute(t.Context(), EncodeRequest, fixtureValue(t, request), options)
			typed, err := compiled.EncodeRequestValue(t.Context(), request, options)
			if failure := IssuesError(issues); failure != nil || err != nil || typed != raw {
				t.Fatal("original Native read changed across boundaries", failure, err)
			}
			fields, err := raw.ReadObject()
			want := snapshot
			if tc.isArray {
				want = fixtureValue(t, []Value{snapshot})
			}
			if err != nil || !equalValues(fields["extension"], want) {
				t.Fatal("original Native value changed", err, raw)
			}
			if tc.isArray {
				request.Tools = nil
				raw, issues = compiled.Execute(t.Context(), EncodeRequest, fixtureValue(t, request), options)
				typed, err = compiled.EncodeRequestValue(t.Context(), request, options)
				if err != nil || IssuesError(issues) != nil || raw != typed {
					t.Fatal("empty array alternative changed across boundaries", issues, err)
				}
				fields, _ = raw.ReadObject()
				if fields["extension"].raw != "[]" {
					t.Fatal("empty branch fabricated an original member")
				}
			}
		})
	}
}

func TestNativeReplayRejectsConstructedAndOpaqueSources(t *testing.T) {
	read := readExpression("input", "/tools/0/native")
	rebuilt := objectExpression(map[string]Expression{
		"source": readExpression("input", "/tools/0/native/source"),
		"value":  readExpression("input", "/tools/0/native/value"),
	})
	for _, tc := range []struct {
		name   string
		source Expression
	}{
		{"literal", literalExpression(map[string]any{"source": map[string]any{}, "value": "synthetic"})},
		{"object", rebuilt},
		{"merge", Expression{Op: "merge", Policy: "last", Items: []Expression{read, objectExpression(map[string]Expression{})}}},
		{"project", Expression{Op: "project", Source: &read, Policy: "include", Keys: []string{"source", "value"}}},
		{"JSON-roundtrip", Expression{Op: "parse_json", Source: &Expression{Op: "stringify_json", Source: &read}}},
		{"constructed-binding", Expression{Op: "let", Name: "copy", Source: &rebuilt, Body: expressionPointer(bindingTestRead("copy", ""))}},
		{"context", readExpression("context", "/native")},
		{"payload", readExpression("input", "/content/0/payload/native")},
		{"attributes", readExpression("input", "/content/0/attributes/native")},
		{"parameters", readExpression("input", "/parameters/native")},
		{"tool-input-schema", readExpression("input", "/tools/0/inputSchema/native")},
		{"native-value", readExpression("input", "/tools/0/native/value/native")},
		{"recall", Expression{Op: "with_index", Name: "saved", KeyMode: "raw_json", Policy: "last", Body: &Expression{Op: "recall", Name: "saved", Source: expressionPointer(literalExpression("key")), Policy: "omit"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			compiler := expressionCompiler{limits: DefaultLimits()}
			program := Expression{Op: "replay_native", Source: &tc.source}
			if _, err := compiler.compilePlan(program, "/program", expressionScope{canReadNative: true, coreInput: reflect.TypeFor[Request]()}, 1); err == nil || !strings.Contains(err.Error(), "unchanged core Native") {
				t.Fatal("constructed or opaque data acquired Native authority", err)
			}
		})
	}
	compiler := expressionCompiler{limits: DefaultLimits()}
	program := Expression{Op: "walk", Source: expressionPointer(readExpression("input", "/content")), Body: &Expression{Op: "replay_native", Source: expressionPointer(readExpression("item", "/native"))}}
	if _, err := compiler.compilePlan(program, "/program", expressionScope{canReadNative: true, coreInput: reflect.TypeFor[Request]()}, 1); err == nil || !strings.Contains(err.Error(), "unchanged core Native") {
		t.Fatal("recursive items acquired an unproved core type", err)
	}
	compiler = expressionCompiler{limits: DefaultLimits()}
	program = Expression{Op: "map", Source: &Expression{
		Op: "if", When: expressionPointer(literalExpression(true)), Then: expressionPointer(readExpression("input", "/tools")),
		Otherwise: &Expression{Op: "array", Items: []Expression{objectExpression(map[string]Expression{"native": rebuilt})}},
	}, Body: &Expression{Op: "replay_native", Source: expressionPointer(readExpression("item", "/native"))}}
	if _, err := compiler.compilePlan(program, "/program", expressionScope{canReadNative: true, coreInput: reflect.TypeFor[Request]()}, 1); err == nil || !strings.Contains(err.Error(), "unchanged core Native") {
		t.Fatal("a constructed alternative acquired an original array's member type", err)
	}
}
