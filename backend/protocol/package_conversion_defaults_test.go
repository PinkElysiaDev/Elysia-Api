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

func TestPackageConversionProgramsRequireExplicitDefaults(t *testing.T) {
	document := noCachePackage(t)
	document.Mapping.ModelPolicy = &ProtocolModelPolicy{
		Reasoning: literalExpression(ReasoningCapabilities{Kind: "none"}),
		Steps:     []ProtocolControlStep{{Set: map[string]Expression{"/parameters/model-rule": literalExpression(true)}}},
	}
	document.Mapping.ConversionDefaults = nil
	compiled := compilePackageForTest(t, document)
	if rules := DefaultConversionPolicy(compiled, compiled).Rules; len(rules) != 0 {
		t.Fatal("undeclared programs became default rules", rules)
	}
	document.Mapping.ConversionDefaults = []ProtocolConversionDefault{
		{ID: "cache-selection", Order: 120, Enabled: true, Phase: ConversionRequest, Use: "request_projection"},
		{ID: "model-selection", Order: 180, Enabled: false, Phase: ConversionRequest, Use: "model_policy"},
	}
	compiled = compilePackageForTest(t, document)
	policy := DefaultConversionPolicy(compiled, compiled)
	if len(policy.Rules) != 2 || policy.Rules[0].ID != "cache-selection" || policy.Rules[0].Order != 120 || policy.Rules[1].Enabled {
		t.Fatal("configuration did not own program identity, order and enablement", policy.Rules)
	}
	conversion, err := ResolveConversion(policy)
	if err != nil {
		t.Fatal(err)
	}
	request := &Request{SchemaVersion: ProtocolCoreVersion, Cache: []CacheIntent{{Kind: "breakpoint", Value: fixtureValue(t, map[string]any{"type": "ephemeral"})}}}
	result, err := conversion.Request(t.Context(), request, ConversionContext{Source: compiled.Identity(), Target: compiled.Identity()}, nil)
	if err != nil || len(result.Cache) != 0 || len(result.Parameters) != 0 || len(request.Cache) != 1 {
		t.Fatal("disabled model program ran or cache selection mutated its caller", result, err)
	}
	for _, declaration := range []ProtocolConversionDefault{
		{Use: "unknown", Phase: ConversionRequest},
		{Use: "model_policy", Phase: ConversionResponse},
		{Use: "model_policy", Phase: ConversionRequest, Steps: []ProtocolControlStep{{Stop: true}}},
	} {
		declaration.ID, declaration.Order, declaration.Enabled = "invalid-program", 10, true
		document.Mapping.ConversionDefaults = []ProtocolConversionDefault{declaration}
		compiler, err := NewCompiler(DefaultLimits(), nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		if result, issues := compiler.CompilePackage(fixtureValue(t, document).Bytes()); result != nil || IssuesError(issues) == nil {
			t.Fatal("invalid program declaration compiled", declaration, issues)
		}
	}
}
