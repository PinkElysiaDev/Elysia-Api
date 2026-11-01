package protocol

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestNativeReplayRejectsRebuiltScope(t *testing.T) {
	document := textProtocolPackage(t)
	const nativePath = "/content/0/children/0/native"
	document.Mapping.Directions[EncodeRequest].Program.Fields["extension"] = Expression{Op: "replay_native", Source: expressionPointer(readExpression("input", nativePath))}
	document.Spec.Documents[document.Mapping.Directions[EncodeRequest].Document].Schema.AllowUnknown = true
	compiled := compilePackageForTest(t, document)
	options := EvaluationContext{Scope: Scope{Provider: "local", Account: "selected"}}
	request, err := compiled.DecodeRequestValue(t.Context(), Value{raw: `{"model":"m","prompt":"hello"}`}, options)
	if err != nil {
		t.Fatal(err)
	}
	origin := Provenance{Protocol: compiled.Identity(), Direction: DecodeRequest, Scope: Scope{Provider: "local", Account: "foreign"}, Path: "/items/0"}
	request.Content[0].Children[0].Native = &Native{Source: origin, Value: StringValue("synthetic native data")}
	_, err = compiled.EncodeRequestValue(t.Context(), request, options)
	assertPackageRuntimeIssue(t, err, ResourceScopeMismatch, origin.Path)
	_, issues := compiled.Execute(t.Context(), EncodeRequest, fixtureValue(t, request), options)
	assertPackageRuntimeIssue(t, IssuesError(issues), ResourceScopeMismatch, origin.Path)
	origin.Scope = options.Scope
	document.Mapping.Directions[EncodeRequest].Program.Fields["extension"] = Expression{Op: "replay_native", Source: expressionPointer(objectExpression(map[string]Expression{
		"source": literalExpression(origin),
		"value":  readExpression("input", nativePath+"/value"),
	}))}
	compiler, _ := NewCompiler(DefaultLimits(), nil, nil)
	rebuilt, issues := compiler.CompilePackage(fixtureValue(t, document).Bytes())
	if err := IssuesError(issues); rebuilt != nil || err == nil || !strings.Contains(err.Error(), "unchanged core Native") {
		t.Fatal("rebuilding scope authorized a foreign original Native", err)
	}
}

func TestPackageNativeTargetOperations(t *testing.T) {
	document := textProtocolPackage(t)
	const nativePath = "/content/0/children/0/native"
	document.Mapping.Directions[EncodeRequest].Program.Fields["extension"] = Expression{Op: "replay_native", Source: expressionPointer(readExpression("input", nativePath))}
	document.Mapping.Directions[EncodeRequest].Program.Fields["located"] = Expression{Op: "native_matches", Operation: "array_item", Path: "/items", Source: expressionPointer(readExpression("input", nativePath+"/source"))}
	document.Spec.Documents[document.Mapping.Directions[EncodeRequest].Document].Schema.AllowUnknown = true
	compiled := compilePackageForTest(t, document)
	options := EvaluationContext{Scope: Scope{Provider: "local", Account: "a"}, Values: Object{
		"target": fixtureValue(t, Target{Protocol: Identity{Family: "forged"}, Scope: Scope{Account: "forged"}}),
	}}
	request, err := compiled.DecodeRequestValue(t.Context(), Value{raw: `{"model":"m","prompt":"hello"}`}, options)
	if err != nil {
		t.Fatal(err)
	}
	origin := Provenance{Protocol: compiled.Identity(), Direction: DecodeRequest, Scope: options.Scope, Path: "/items/4"}
	// Node replay uses compatible wire provenance, not exact mapping revisions.
	origin.Protocol.Revision = "earlier"
	native := &Native{Source: origin, Value: Value{raw: `{"counter":9007199254740993,"payload":"<text>"}`}, RequestContext: &NativeRequestContext{Scope: options.Scope, Values: Object{"model": StringValue("m")}}}
	request.Content[0].Children[0].Native = native
	for _, isForeignScope := range []bool{false, true} {
		if isForeignScope {
			native.Source.Scope.Account = "other"
		}
		core := fixtureValue(t, request)
		raw, issues := compiled.Execute(t.Context(), EncodeRequest, core, options)
		typed, typedErr := compiled.EncodeRequestValue(t.Context(), request, options)
		if isForeignScope {
			assertPackageRuntimeIssue(t, IssuesError(issues), ResourceScopeMismatch, origin.Path)
			assertPackageRuntimeIssue(t, typedErr, ResourceScopeMismatch, origin.Path)
			continue
		}
		if err := IssuesError(issues); err != nil || typedErr != nil || raw != typed {
			t.Fatal("raw and typed native operations differ", err, typedErr)
		}
		fields, err := raw.ReadObject()
		if err != nil || !equalValues(fields["extension"], native.Value) || fields["located"] != coreBoolean(true) {
			t.Fatal("native target operations lost data or location", err, raw)
		}
	}
	unused := compilePackageForTest(t, textProtocolPackage(t))
	if unused.newNativeEvaluation(t.Context(), EncodeRequest, options) != nil {
		t.Fatal("an unused native target was allocated")
	}
}

func TestNativeReplayRetainsValueAndErrors(t *testing.T) {
	compiler := expressionCompiler{limits: DefaultLimits()}
	plan, err := compiler.compilePlan(Expression{Op: "replay_native", Source: expressionPointer(readExpression("input", ""))}, "/replay", expressionScope{canReadNative: true, coreInput: reflect.TypeFor[Native]()}, 1)
	if err != nil {
		t.Fatal(err)
	}
	target := Target{Protocol: Identity{Family: "native-test", WireVersion: "1"}, Direction: EncodeRequest, Scope: Scope{Provider: "local", Account: "a"}}
	for _, name := range []string{"exact", "family", "wire", "direction", "scope", "budget", "absent-value"} {
		t.Run(name, func(t *testing.T) {
			native := Native{Source: Provenance{Protocol: target.Protocol, Direction: DecodeRequest, Scope: target.Scope, Path: "/tools/0"}, Value: Value{raw: `{ "counter":9007199254740993, "text":"<unescaped>" }`}}
			limits := DefaultLimits()
			switch name {
			case "family":
				native.Source.Protocol.Family = "other"
			case "wire":
				native.Source.Protocol.WireVersion = "other"
			case "direction":
				native.Source.Direction = DecodeResponse
			case "scope":
				native.Source.Scope.Account = "other"
			case "budget":
				limits.BufferBytes = native.Value.Len() - 1
			case "absent-value":
				native.Value = Value{}
			}
			want, wantErr := ReplayNative(native, target, nil, limits)
			input := `{"source":` + fixtureValue(t, native.Source).raw
			if !native.Value.IsZero() {
				input += `,"value":` + native.Value.raw
			}
			state := numericTestState(t, Value{raw: input + "}"})
			state.native = &nativeEvaluation{target: target, limits: limits}
			got, gotErr := plan.evaluate(state)
			if (gotErr == nil) != (wantErr == nil) || got != want {
				t.Fatal("replay result changed", got, want, gotErr, wantErr)
			}
			if wantErr != nil {
				var before, after *ConversionError
				if !errors.As(wantErr, &before) || !errors.As(gotErr, &after) || !reflect.DeepEqual(before.Issues, after.Issues) {
					t.Fatal("replay diagnostics changed", gotErr, wantErr)
				}
				var beforeLimit, afterLimit *ResourceLimitError
				if errors.As(wantErr, &beforeLimit) != errors.As(gotErr, &afterLimit) || !reflect.DeepEqual(beforeLimit, afterLimit) {
					t.Fatal("replay resource cause changed", gotErr, wantErr)
				}
			}
		})
	}
	for _, value := range []Value{{}, {raw: "null"}} {
		state := numericTestState(t, value)
		state.native = &nativeEvaluation{target: target, limits: DefaultLimits()}
		_, err := plan.evaluate(state)
		assertPackageRuntimeIssue(t, err, UnsupportedCapability, "/native")
	}
	state := numericTestState(t, Value{raw: `{"source":{},"value":{},"unknown":true}`})
	state.native = &nativeEvaluation{target: target, limits: DefaultLimits()}
	if _, err := plan.evaluate(state); err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Fatal("native operand must be a closed core value", err)
	}
}

func TestNativeMatchesClassifiesLocations(t *testing.T) {
	compiler := expressionCompiler{limits: DefaultLimits()}
	plan, err := compiler.compilePlan(Expression{Op: "native_matches", Operation: "array_item", Path: "/items", Source: expressionPointer(readExpression("input", ""))}, "/match", expressionScope{canReadNative: true, coreInput: reflect.TypeFor[Provenance]()}, 1)
	if err != nil {
		t.Fatal(err)
	}
	target := Target{Protocol: Identity{Family: "native-test", WireVersion: "1"}, Direction: EncodeRequest, Scope: Scope{Account: "selected"}}
	for _, tc := range []struct {
		path string
		want bool
	}{
		{"/items/0", true}, {"/items/7", true}, {"/items/+1", true}, {"/items/01", true},
		{"/items/-1", false}, {"/items/", false}, {"/items", false}, {"/other/0", false}, {"/items/0/nested", false}, {"/items/99999999999999999999999", false},
	} {
		origin := Provenance{Protocol: target.Protocol, Direction: DecodeRequest, Path: tc.path, Scope: Scope{Account: "other"}}
		state := numericTestState(t, fixtureValue(t, origin))
		state.native = &nativeEvaluation{target: target}
		state.itemIndex = 99
		got, err := plan.evaluate(state)
		if err != nil || got != coreBoolean(tc.want) {
			t.Fatal("location classification changed or enforced replay authorization", tc.path, got, err)
		}
		origin.Protocol.Family = "other"
		state.input = fixtureValue(t, origin)
		got, err = plan.evaluate(state)
		if err != nil || got != coreBoolean(false) {
			t.Fatal("foreign wire location matched", got, err)
		}
	}
	for _, value := range []Value{{}, {raw: "null"}} {
		state := numericTestState(t, value)
		state.native = &nativeEvaluation{target: target}
		got, err := plan.evaluate(state)
		if err != nil || got != coreBoolean(false) {
			t.Fatal("missing native location must not match", got, err)
		}
	}
	fieldPlan, err := compiler.compilePlan(Expression{Op: "native_matches", Operation: "array_item_field", Path: "/items", Name: "wire/index~", Source: expressionPointer(readExpression("input", ""))}, "/match", expressionScope{canReadNative: true, coreInput: reflect.TypeFor[Provenance]()}, 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, fixture := range []struct {
		path string
		want bool
	}{
		{"/items/7/wire~1index~0", true},
		{"/items/7", false},
		{"/items/7/index", false},
		{"/items/7/nested/wire~1index~0", false},
		{"/items/-1/wire~1index~0", false},
	} {
		origin := Provenance{Protocol: target.Protocol, Direction: DecodeRequest, Path: fixture.path}
		state := numericTestState(t, fixtureValue(t, origin))
		state.native = &nativeEvaluation{target: target}
		got, err := fieldPlan.evaluate(state)
		if err != nil || got != coreBoolean(fixture.want) {
			t.Fatal("field location classification changed", fixture.path, got, err)
		}
	}
}

func TestNativeReplayUsesMappingNormalization(t *testing.T) {
	compiler := expressionCompiler{limits: DefaultLimits()}
	plan, err := compiler.compilePlan(Expression{Op: "replay_native", Source: expressionPointer(readExpression("input", ""))}, "/replay", expressionScope{canReadNative: true, coreInput: reflect.TypeFor[Native]()}, 1)
	if err != nil {
		t.Fatal(err)
	}
	target := Target{Protocol: Identity{Family: "native-test", WireVersion: "1"}, Direction: EncodeRequest}
	native := Native{Source: Provenance{Protocol: target.Protocol, Direction: DecodeRequest}, Value: Value{raw: `{ "counter":9007199254740993, "text":"<unescaped>" }`}}
	original := native.Value
	want := Value{raw: compactValueString(original.raw)}
	if want == original {
		t.Fatal("fixture must require semantic JSON normalization")
	}
	for _, isTyped := range []bool{false, true} {
		state := numericTestState(t, fixtureValue(t, native))
		state.native = &nativeEvaluation{target: target, limits: DefaultLimits()}
		if isTyped {
			state.inputReader = func(pointer []string) (JSONOutput, error) { return readCoreNative(native, pointer) }
		}
		got, err := plan.evaluate(state)
		if err != nil || got != want || native.Value != original {
			t.Fatal("replay changed or bypassed the mapping boundary", isTyped, got, err)
		}
	}
}

func TestNativeTargetOperationsRequireEncodeProgram(t *testing.T) {
	for _, operation := range []string{"replay_native", "native_matches"} {
		expression := Expression{Op: operation, Source: expressionPointer(readExpression("input", ""))}
		if operation == "native_matches" {
			expression.Operation, expression.Path = "array_item", "/items"
		}
		for _, scope := range []expressionScope{{}, {wireCapture: true}, {canReadRuntime: true}} {
			compiler := expressionCompiler{limits: DefaultLimits()}
			if _, err := compiler.compilePlan(expression, "/program", scope, 1); err == nil {
				t.Fatal("native target operation escaped its encode program", operation)
			}
		}
		compiler := expressionCompiler{limits: DefaultLimits()}
		inputType := reflect.TypeFor[Native]()
		if operation == "native_matches" {
			inputType = reflect.TypeFor[Provenance]()
		}
		plan, err := compiler.compilePlan(expression, "/program", expressionScope{canReadNative: true, coreInput: inputType}, 1)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := plan.evaluate(numericTestState(t, Value{raw: "null"})); err == nil || !strings.Contains(err.Error(), "not bound") {
			t.Fatal("unbound native target was accepted", operation, err)
		}
	}
	for _, expression := range []Expression{
		{Op: "native_matches", Operation: "unknown", Path: "/items", Source: expressionPointer(readExpression("input", ""))},
		{Op: "native_matches", Operation: "array_item", Path: "not-a-pointer", Source: expressionPointer(readExpression("input", ""))},
		{Op: "native_matches", Operation: "array_item_field", Path: "/items", Source: expressionPointer(readExpression("input", ""))},
		{Op: "native_matches", Operation: "array_item", Path: "/items", Name: "index", Source: expressionPointer(readExpression("input", ""))},
	} {
		compiler := expressionCompiler{limits: DefaultLimits()}
		if _, err := compiler.compilePlan(expression, "/program", expressionScope{canReadNative: true, coreInput: reflect.TypeFor[Provenance]()}, 1); err == nil {
			t.Fatal("invalid native location operation compiled")
		}
	}
}
