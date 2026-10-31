package protocol

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestPackageModelCapabilityBindingAndIsolation(t *testing.T) {
	document := textProtocolPackage(t)
	document.Mapping.ModelPolicy = &ProtocolModelPolicy{Reasoning: literalExpression(ReasoningCapabilities{Kind: "level", Disable: true, Levels: []string{"low", "high"}, DefaultEffort: "high"})}
	compiled := compilePackageForTest(t, document)
	first, err := compiled.ResolveModelReasoning(t.Context(), "model", nil)
	if err != nil {
		t.Fatal(err)
	}
	first.Levels[0] = "edited"
	bound := ReasoningCapabilities{Kind: "level", Levels: []string{"medium"}, DefaultMode: "enabled", Source: "explicit-binding"}
	got, err := compiled.ResolveModelReasoning(t.Context(), "model", &bound)
	if err != nil || !reflect.DeepEqual(got, bound) {
		t.Fatal("binding was merged with inferred capability", got, err)
	}
	got.Levels[0] = "edited"
	if bound.Levels[0] != "medium" {
		t.Fatal("result mutated the binding")
	}
	fresh, err := compiled.ResolveModelReasoning(t.Context(), "model", nil)
	if err != nil || fresh.Levels[0] != "low" {
		t.Fatal("result mutated the compiled profile", fresh, err)
	}
	bound.Kind = "invalid"
	if _, err := compiled.ResolveModelReasoning(t.Context(), "model", &bound); err == nil {
		t.Fatal("invalid dynamic binding was accepted")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := compiled.ResolveModelReasoning(ctx, "model", nil); !errors.Is(err, context.Canceled) {
		t.Fatal("capability lookup ignored cancellation", err)
	}
}

func TestPackageModelCapabilitiesValidateProfilesAtCompileTime(t *testing.T) {
	valid := literalExpression(ReasoningCapabilities{Kind: "unknown"})
	for _, expression := range []Expression{
		literalExpression(ReasoningCapabilities{Kind: "budget", MinBudget: 100, MaxBudget: 50}),
		literalExpression(ReasoningCapabilities{Kind: "level"}),
		literalExpression(ReasoningCapabilities{Kind: "unknown", Levels: []string{"unrecognized"}}),
		{Op: "if", When: expressionPointer(literalExpression(true)), Then: &valid},
		readExpression("input", ""),
	} {
		document := textProtocolPackage(t)
		document.Mapping.ModelPolicy = &ProtocolModelPolicy{Reasoning: expression}
		compiler, err := NewCompiler(DefaultLimits(), nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		compiled, issues := compiler.CompilePackage(fixtureValue(t, document).Bytes())
		if compiled != nil || IssuesError(issues) == nil {
			t.Fatal("invalid static model policy compiled", expression, issues)
		}
	}
	document := textProtocolPackage(t)
	document.Spec.Family = "openai-chat"
	compiled := compilePackageForTest(t, document)
	capability, err := compiled.ResolveModelReasoning(t.Context(), "gpt-6.1-sol", nil)
	if err != nil || capability.Kind != "unknown" || len(capability.Levels) != 0 {
		t.Fatal("wire family selected an undeclared model policy", capability, err)
	}
}
