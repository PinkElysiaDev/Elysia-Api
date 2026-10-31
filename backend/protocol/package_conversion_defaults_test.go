package protocol

import "testing"

func TestPackageConversionDefaultReplacement(t *testing.T) {
	document := textProtocolPackage(t)
	document.Mapping.ConversionDefaults = []ProtocolConversionDefault{{ID: "field-default", Order: 10, Enabled: true, Phase: ConversionRequest,
		Steps: []ProtocolControlStep{{Set: map[string]Expression{"/parameters/choice": literalExpression("original")}}}}}
	compiled := compilePackageForTest(t, document)
	defaults := DefaultConversionPolicy(compiled, compiled)
	if len(defaults.Rules) != 1 || defaults.Rules[0].Action != "core_controls" || defaults.Rules[0].preparedOption == nil {
		t.Fatal("package defaults did not retain a prepared typed rule", defaults.Rules)
	}
	request := &Request{SchemaVersion: ProtocolCoreVersion}
	route := ConversionContext{Source: compiled.Identity(), Target: compiled.Identity()}
	run := func(policy ConversionPolicy) *Request {
		t.Helper()
		conversion, err := ResolveConversion(defaults, policy)
		if err != nil {
			t.Fatal(err)
		}
		result, err := conversion.Request(t.Context(), request, route, nil)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	if result := run(ConversionPolicy{SchemaVersion: ConversionPolicySchemaVersion, ID: "empty"}); !result.Parameters["choice"].IsString("original") {
		t.Fatal("declared default did not run", result.Parameters)
	}
	rule := defaults.Rules[0]
	var replacement coreControlOptions
	if err := rule.Value.Decode(&replacement); err != nil {
		t.Fatal(err)
	}
	replacement.Steps[0].Set["/parameters/choice"] = literalExpression("replacement")
	rule.Value = fixtureValue(t, replacement)
	override := ConversionPolicy{SchemaVersion: ConversionPolicySchemaVersion, ID: "override", Rules: []ConversionRule{rule}}
	if result := run(override); !result.Parameters["choice"].IsString("replacement") {
		t.Fatal("same ID reused a stale prepared program", result.Parameters)
	}
	override.Rules[0].Enabled = false
	if result := run(override); !result.Parameters["choice"].IsZero() {
		t.Fatal("disabled replacement left the old default active")
	}
	override.Rules[0] = defaults.Rules[0]
	override.Rules[0].Phase = ConversionResponse
	if _, err := ResolveConversion(defaults, override); err == nil {
		t.Fatal("prepared request program escaped its declared phase")
	}
	if request.Parameters != nil {
		t.Fatal("default projection changed its caller's request")
	}
}

func TestPointerJoinEscapesOnlySegments(t *testing.T) {
	expression := Expression{Op: "pointer_join", Source: expressionPointer(literalExpression("/already~1escaped")), Items: []Expression{literalExpression("a/b~c"), literalExpression("")}}
	plan, _ := compileOutputPlan(compileTestOutputExpression(t, expression))
	output, err := plan.evaluate(evaluation{ctx: t.Context(), budget: &evaluationBudget{limits: DefaultLimits()}})
	if err != nil || !output.IsString("/already~1escaped/a~1b~0c/") {
		t.Fatal("pointer segments changed path semantics", output, err)
	}
	expression.Source = expressionPointer(readExpression("input", ""))
	plan, _ = compileOutputPlan(compileTestOutputExpression(t, expression))
	if _, err := plan.evaluate(evaluation{ctx: t.Context(), input: StringValue("not-a-pointer"), budget: &evaluationBudget{limits: DefaultLimits()}}); err == nil {
		t.Fatal("invalid pointer prefix accepted")
	}
}
