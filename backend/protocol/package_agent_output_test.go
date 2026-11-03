package protocol

import (
	"reflect"
	"strings"
	"testing"
)

func TestPackageAgentClientOutputOwnership(t *testing.T) {
	compiled, configuration := agentOutputFixture(t)
	configuration.ClientOutput = map[string]Expression{
		"includeUsage": {Op: "ref", Ref: "local-preference"},
		"responsesStorage": {Op: "let", Name: "choice", Source: expressionPointer(literalExpression(false)),
			Body: expressionPointer(objectExpression(map[string]Expression{
				"requested": {Op: "read", From: "binding", Name: "choice"},
				"effective": {Op: "read", From: "binding", Name: "choice"},
			}))},
	}
	references := map[string]Expression{"local-preference": {Op: "let", Name: "choice", Source: expressionPointer(literalExpression(false)), Body: &Expression{Op: "read", From: "binding", Name: "choice"}}}
	if err := compiled.compilePackageAgent(configuration, newExpressionCompiler(DefaultLimits(), references)); err != nil {
		t.Fatal(err)
	}
	includeUsage := true
	current := &ClientOutput{IncludeUsage: &includeUsage, RawResponsesInclude: fixtureValue(t, []string{"retained"}),
		ResponsesStorage: &ResponsesStorageIntent{SourceCodec: "original", Requested: true, Effective: true}}
	request := Request{SchemaVersion: ProtocolCoreVersion, Source: AgentIdentity(), ClientOutput: current}
	before := fixtureValue(t, request)
	prepared, err := compiled.BuildAgentRequest(t.Context(), request, AgentPreferences{MaxOutputTokens: 10, Stream: true})
	if err != nil {
		t.Fatal(err)
	}
	if !equalValues(before, fixtureValue(t, request)) {
		t.Fatal("Agent preparation mutated caller output intent")
	}
	output := prepared.ClientOutput
	if !output.CollectUsage || output.IncludeUsage == nil || *output.IncludeUsage || output.RawResponsesInclude != current.RawResponsesInclude || output.ResponsesStorage == nil || *output.ResponsesStorage != (ResponsesStorageIntent{}) {
		t.Fatalf("Agent policy lost an unrelated field or retained stale storage facts: %+v", output)
	}
	if prepared.Parameters["limit"].JSONString() != "10" {
		t.Fatal("parameter policy did not share the Agent preferences", prepared.Parameters)
	}
	// A parameter-only call does not evaluate the independent delivery policy.
	compiled.agentClientOutput[0].program, _ = compileOutputPlan(&compiledExpression{expressionCompileFacts: &expressionCompileFacts{}, op: "reject", path: "/clientOutput", rejection: &declaredRejectionError{reason: "must not run"}})
	if _, err := compiled.BuildAgentParameters(t.Context(), AgentPreferences{MaxOutputTokens: 10}); err != nil {
		t.Fatal("parameter preview executed client output fields", err)
	}
}

func TestPackageAgentClientOutputPermissions(t *testing.T) {
	for _, field := range []string{"collectUsage", "/responsesStorage/effective", "unknown"} {
		compiled, configuration := agentOutputFixture(t)
		configuration.ClientOutput = map[string]Expression{field: literalExpression(true)}
		if err := compiled.compilePackageAgent(configuration, newExpressionCompiler(DefaultLimits(), nil)); err == nil || !strings.Contains(err.Error(), "cannot write") {
			t.Fatalf("field %q escaped the declared Agent output permission: %v", field, err)
		}
	}
	compiled, configuration := agentOutputFixture(t)
	configuration.ClientOutput = map[string]Expression{"includeUsage": literalExpression(1)}
	if err := compiled.compilePackageAgent(configuration, newExpressionCompiler(DefaultLimits(), nil)); err == nil || !strings.Contains(err.Error(), "wrong client output type") {
		t.Fatal("statically wrong client output field type compiled", err)
	}
}

func TestStorageControlsPreserveSourceIntent(t *testing.T) {
	document := textProtocolPackage(t)
	document.Mapping.ConversionDefaults = []ProtocolConversionDefault{{ID: "storage", Enabled: true, Phase: ConversionRequest, Order: 1,
		Steps: []ProtocolControlStep{{Set: map[string]Expression{
			"/clientOutput/responsesStorage/effective":        literalExpression(true),
			"/clientOutput/responsesStorage/effectiveUnknown": literalExpression(false),
		}}}}}
	compiled := compilePackageForTest(t, document)
	conversion, err := ResolveConversion(DefaultConversionPolicy(compiled, compiled))
	if err != nil {
		t.Fatal(err)
	}
	route := ConversionContext{Source: compiled.Identity(), Target: compiled.Identity()}
	intent := &ResponsesStorageIntent{SourceCodec: "source", RequestedUnknown: true, EffectiveUnknown: true, Raw: fixtureValue(t, nil)}
	request := &Request{SchemaVersion: ProtocolCoreVersion, ClientOutput: &ClientOutput{ResponsesStorage: intent}}
	result, err := conversion.Request(t.Context(), request, route, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := *intent
	want.Effective, want.EffectiveUnknown = true, false
	if !reflect.DeepEqual(*result.ClientOutput.ResponsesStorage, want) || intent.Effective || !intent.EffectiveUnknown {
		t.Fatal("target disposition altered source facts or its caller", result.ClientOutput.ResponsesStorage, intent)
	}
	for _, output := range []*ClientOutput{nil, {}} {
		request.ClientOutput = output
		_, err := conversion.Request(t.Context(), request, route, nil)
		failure, ok := err.(*ConversionError)
		if !ok || len(failure.Issues) != 1 || failure.Issues[0].Code != InvalidInput || failure.Issues[0].Path != "/clientOutput/responsesStorage" || failure.Issues[0].Stage != "conversion.request" {
			t.Fatal("missing source owner was not rejected at its real boundary", err)
		}
	}
	for _, field := range []string{"raw", "requested", "sourceCodec"} {
		if _, _, err := compileControlSetter("/clientOutput/responsesStorage/" + field); err == nil {
			t.Fatal("target control can overwrite source intent", field)
		}
	}
}

func agentOutputFixture(t *testing.T) (*Compiled, *ProtocolAgentMapping) {
	t.Helper()
	compiled := &Compiled{limits: DefaultLimits(), packagePlan: &compiledPackagePlan{}, capabilities: CapabilitySet{FunctionToolsCapability: true},
		mappings:   map[Direction]compiledMapping{EncodeRequest: {}, DecodeRequest: {}},
		operations: map[string]Operation{"generate": {Kind: "generate", Transport: HTTPJSON}, "stream": {Kind: "generate", Transport: SSE}}}
	configuration := &ProtocolAgentMapping{ToolResults: JSONInput, Parameters: objectExpression(map[string]Expression{"limit": readExpression("input", "/maxOutputTokens")}),
		Samples: []AgentSample{{ID: "plain", Expected: fixtureValue(t, map[string]int{"limit": 10}), ExpectedClientOutput: fixtureValue(t, map[string]any{})}}}
	return compiled, configuration
}
