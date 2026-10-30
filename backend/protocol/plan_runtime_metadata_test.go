package protocol

import (
	"errors"
	"strings"
	"testing"
)

func TestPackageRuntimeMetadataAcrossBoundaries(t *testing.T) {
	document := textProtocolPackage(t)
	runtimeRead := readExpression("runtime", "/interleavedThinking")
	runtimeRead.Required = true
	document.Mapping.Expressions = map[string]Expression{"runtime-thinking": runtimeRead}
	document.Mapping.Directions[DecodeRequest].Program.Fields["parameters"] = objectExpression(map[string]Expression{
		"runtimeThinking": {Op: "ref", Ref: "runtime-thinking"},
	})
	document.Mapping.Directions[EncodeRequest].Program.Fields["runtimeThinking"] = Expression{Op: "ref", Ref: "runtime-thinking"}
	document.Spec.Documents[document.Mapping.Directions[DecodeRequest].Document].Schema.AllowUnknown = true
	compiled := compilePackageForTest(t, document)
	if !compiled.mappings[DecodeRequest].hasCoreOutputPlan || !compiled.mappings[EncodeRequest].hasCoreInputPlan {
		t.Fatal("fixture must use typed mapping plans")
	}
	for _, tc := range []struct {
		name    string
		options EvaluationContext
		target  EvaluationContext
	}{
		{name: "zero-options"},
		{name: "false-with-spoofed-true", options: EvaluationContext{Values: Object{"interleavedThinking": coreBoolean(true)}}, target: EvaluationContext{InterleavedThinking: true, Values: Object{"interleavedThinking": coreBoolean(false)}}},
		{name: "true-with-spoofed-false", options: EvaluationContext{InterleavedThinking: true, Values: Object{"interleavedThinking": coreBoolean(false)}}, target: EvaluationContext{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := Value{raw: `{"model":"m","prompt":"hello","interleavedThinking":` + coreBoolean(!tc.options.InterleavedThinking).raw + `}`}
			core, issues := compiled.Execute(t.Context(), DecodeRequest, input, tc.options)
			if err := IssuesError(issues); err != nil {
				t.Fatal(err)
			}
			request, err := compiled.DecodeRequestValue(t.Context(), input, tc.options)
			if err != nil {
				t.Fatal(err)
			}
			decoded, err := readValuePointer(core, []string{"parameters", "runtimeThinking"})
			if err != nil || decoded != coreBoolean(tc.options.InterleavedThinking) || request.Parameters["runtimeThinking"] != decoded {
				t.Fatal("raw and typed decoding changed the runtime fact", decoded, err)
			}
			rawWire, issues := compiled.Execute(t.Context(), EncodeRequest, core, tc.target)
			if err := IssuesError(issues); err != nil {
				t.Fatal(err)
			}
			typedWire, err := compiled.EncodeRequestValue(t.Context(), request, tc.target)
			if err != nil || typedWire != rawWire {
				t.Fatal("raw and typed encoding disagreed", typedWire, rawWire, err)
			}
			encoded, err := readValuePointer(typedWire, []string{"runtimeThinking"})
			if err != nil || encoded != coreBoolean(tc.target.InterleavedThinking) {
				t.Fatal("encoding did not use the target runtime fact", encoded, err)
			}
		})
	}
}

func TestPackageRuntimeScopeModel(t *testing.T) {
	document := textProtocolPackage(t)
	model := readExpression("runtime", "/scope/model")
	contextModel := readExpression("context", "/model")
	document.Mapping.Directions[DecodeRequest].Program.Fields["parameters"] = objectExpression(map[string]Expression{
		"scopeModel":   model,
		"contextFirst": {Op: "if", When: &Expression{Op: "exists", Source: &contextModel}, Then: &contextModel, Otherwise: &model},
	})
	document.Mapping.Directions[EncodeRequest].Program.Fields["scopeModel"] = model
	document.Spec.Documents[document.Mapping.Directions[DecodeRequest].Document].Schema.AllowUnknown = true
	compiled := compilePackageForTest(t, document)
	for _, tc := range []struct {
		name, scopeModel string
		contextModel     Value
	}{
		{name: "missing-context", scopeModel: "bound-model"},
		{name: "explicit-context", scopeModel: "bound-model", contextModel: StringValue("declared-priority")},
		{name: "null-context", scopeModel: "bound-model", contextModel: Value{raw: "null"}},
		{name: "empty-context", scopeModel: "bound-model", contextModel: StringValue("")},
		{name: "empty-scope"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			options := EvaluationContext{Scope: Scope{Model: tc.scopeModel}, Values: Object{
				"scope": fixtureValue(t, Scope{Model: "spoofed-values-model"}),
			}}
			wantContext := StringValue(tc.scopeModel)
			if !tc.contextModel.IsZero() {
				options.Values["model"], wantContext = tc.contextModel, tc.contextModel
			}
			input := Value{raw: `{"model":"m","prompt":"hello","scope":{"model":"spoofed-wire-model"}}`}
			core, issues := compiled.Execute(t.Context(), DecodeRequest, input, options)
			request, err := compiled.DecodeRequestValue(t.Context(), input, options)
			if err != nil || IssuesError(issues) != nil {
				t.Fatal("runtime model decoding failed", issues, err)
			}
			for key, want := range map[string]Value{"scopeModel": StringValue(tc.scopeModel), "contextFirst": wantContext} {
				actual, err := readValuePointer(core, []string{"parameters", key})
				if err != nil || actual != want || request.Parameters[key] != want {
					t.Fatal("runtime model or declared context priority changed", key, actual, err)
				}
			}
			options.Scope.Model = "encoding-model"
			raw, issues := compiled.Execute(t.Context(), EncodeRequest, core, options)
			typed, err := compiled.EncodeRequestValue(t.Context(), request, options)
			if err != nil || IssuesError(issues) != nil || raw != typed {
				t.Fatal("runtime model encoding differs", issues, err)
			}
			actual, err := readValuePointer(raw, []string{"scopeModel"})
			if err != nil || actual != StringValue("encoding-model") {
				t.Fatal("encoding did not use its own bound model", actual, err)
			}
		})
	}
}

func TestPackageRuntimeMetadataEffectConditions(t *testing.T) {
	document := textProtocolPackage(t)
	for _, direction := range []Direction{DecodeRequest, EncodeRequest} {
		entry := document.Mapping.Directions[direction]
		entry.Effects = append(entry.Effects, ProtocolSemanticEffect{
			ID: "runtime-guard", Kind: "reject", Path: "/model", Target: "/model", Reason: "synthetic runtime guard",
			When: expressionPointer(readExpression("runtime", "/interleavedThinking")),
		})
		document.Mapping.Directions[direction] = entry
	}
	compiled := compilePackageForTest(t, document)
	input := Value{raw: `{"model":"m","prompt":"hello"}`}
	request, err := compiled.DecodeRequestValue(t.Context(), input, EvaluationContext{})
	if err != nil {
		t.Fatal(err)
	}
	core, issues := compiled.Execute(t.Context(), DecodeRequest, input, EvaluationContext{})
	if err := IssuesError(issues); err != nil {
		t.Fatal(err)
	}
	for _, isEnabled := range []bool{false, true} {
		options := EvaluationContext{InterleavedThinking: isEnabled, Values: Object{"interleavedThinking": coreBoolean(!isEnabled)}}
		for name, execute := range map[string]func() error{
			"raw-decode": func() error {
				_, issues := compiled.Execute(t.Context(), DecodeRequest, input, options)
				return IssuesError(issues)
			},
			"raw-encode": func() error {
				_, issues := compiled.Execute(t.Context(), EncodeRequest, core, options)
				return IssuesError(issues)
			},
			"typed-decode": func() error { _, err := compiled.DecodeRequestValue(t.Context(), input, options); return err },
			"typed-encode": func() error { _, err := compiled.EncodeRequestValue(t.Context(), request, options); return err },
		} {
			err := execute()
			if !isEnabled {
				if err != nil {
					t.Fatal(name, err)
				}
				continue
			}
			var conversion *ConversionError
			if !errors.As(err, &conversion) || len(conversion.Issues) != 1 || conversion.Issues[0].RuleID != "runtime-guard" {
				t.Fatal(name, "runtime condition did not reject", err)
			}
		}
	}
}

func TestRuntimeMetadataRejectsInvalidScopesAndFields(t *testing.T) {
	read := readExpression("runtime", "/interleavedThinking")
	for _, scope := range []string{"v2", "spec", "projection"} {
		t.Run(scope, func(t *testing.T) {
			compiler, _ := NewCompiler(DefaultLimits(), nil, nil)
			var compiled *Compiled
			var issues []ConversionIssue
			if scope == "v2" {
				definition := textVerificationDefinition(t, "runtime-unavailable")
				definition.Directions[DecodeRequest] = Mapping{Transform: &read}
				value := fixtureValue(t, definition)
				compiled, issues = compiler.Compile(value.Bytes())
			} else {
				document := textProtocolPackage(t)
				if scope == "spec" {
					contract := document.Spec.Documents[document.Mapping.Directions[DecodeRequest].Document]
					contract.Assertions = []WireAssertion{{Path: "/prompt", Schema: ValueSchema{Type: StringType}, Check: &read, Code: InvalidInput, Reason: "synthetic assertion"}}
					document.Spec.Documents[document.Mapping.Directions[DecodeRequest].Document] = contract
				} else {
					document = noCachePackage(t)
					document.Mapping.Directions[EncodeRequest].Effects[2].When = &read
				}
				value := fixtureValue(t, document)
				compiled, issues = compiler.CompilePackage(value.Bytes())
			}
			if err := IssuesError(issues); compiled != nil || err == nil || !strings.Contains(err.Error(), "runtime metadata requires") {
				t.Fatal("runtime metadata escaped its compilation scope", err)
			}
		})
	}
	for _, path := range []string{"", "/headers", "/scope", "/scope/account", "/scope/model/value", "/identity", "/interleavedThinking/value"} {
		compiler := expressionCompiler{limits: DefaultLimits()}
		if _, err := compiler.compilePlan(readExpression("runtime", path), "/program", expressionScope{canReadRuntime: true}, 1); err == nil || !strings.Contains(err.Error(), "unknown runtime metadata field") {
			t.Fatal("undeclared runtime field compiled", path, err)
		}
	}
}

func TestRuntimeMetadataRequiresExecutionBinding(t *testing.T) {
	for _, path := range []string{"/interleavedThinking", "/scope/model"} {
		compiler := expressionCompiler{limits: DefaultLimits()}
		plan, err := compiler.compilePlan(readExpression("runtime", path), "/program", expressionScope{canReadRuntime: true}, 1)
		if err != nil {
			t.Fatal(err)
		}
		state := evaluation{ctx: t.Context(), budget: &evaluationBudget{limits: DefaultLimits()}}
		if _, err := plan.evaluate(state); err == nil || !strings.Contains(err.Error(), "runtime metadata is not bound") {
			t.Fatal("an unbound evaluation silently returned a zero value", path, err)
		}
	}
}
