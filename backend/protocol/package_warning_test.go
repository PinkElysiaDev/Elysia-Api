package protocol

import (
	"context"
	"errors"
	"testing"
)

func packageWarning() ProtocolSemanticEffect {
	return ProtocolSemanticEffect{
		ID: "model-advisory", Kind: "warn", Path: "/model", Reason: "selected model has a configuration advisory",
		When:       expressionPointer(Expression{Op: "equal", Items: []Expression{readExpression("input", "/model"), literalExpression("m")}}),
		Diagnostic: &RejectDiagnostic{Code: InvalidInput, Stage: "wire", Suggestion: "Review the selected model."},
	}
}

func TestPackageWarningPreservesInputAndOutput(t *testing.T) {
	document := textProtocolPackage(t)
	for _, direction := range []Direction{DecodeRequest, EncodeRequest} {
		mapping := document.Mapping.Directions[direction]
		mapping.Effects = append(mapping.Effects, packageWarning())
		document.Mapping.Directions[direction] = mapping
	}
	compiled := compilePackageForTest(t, document)
	for _, model := range []string{"m", "other"} {
		wire := fixtureValue(t, map[string]any{"model": model, "prompt": "hello"})
		for _, hasSink := range []bool{false, true} {
			options := EvaluationContext{}
			if hasSink {
				options.Diagnostics = &DiagnosticSink{}
			}
			request, err := compiled.DecodeRequestValue(t.Context(), wire, options)
			if err != nil {
				t.Fatal(err)
			}
			before := fixtureValue(t, request)
			output, err := compiled.EncodeRequestValue(t.Context(), request, options)
			if err != nil || !equalValues(output, wire) || fixtureValue(t, request) != before {
				t.Fatal("advisory changed the request or result", output, err)
			}
			want := 0
			if hasSink && model == "m" {
				want = 2
			}
			if len(options.Diagnostics.Issues()) != want {
				t.Fatal("warning condition or direction was lost", options.Diagnostics.Issues())
			}
			for _, issue := range options.Diagnostics.Issues() {
				if issue.RuleID != "model-advisory" || issue.Path != "/model" || issue.Code != InvalidInput || issue.Severity != SeverityWarning || issue.Fidelity != "preserved" {
					t.Fatal("advisory classification changed", issue)
				}
			}
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			if _, err := compiled.EncodeRequestValue(ctx, request, options); !errors.Is(err, context.Canceled) {
				t.Fatal("warning bypassed cancellation", err)
			}
		}
	}
}

func TestPackageWarningRequiresReadOnlyDeclaration(t *testing.T) {
	for _, change := range []func(*ProtocolSemanticEffect){
		func(effect *ProtocolSemanticEffect) { effect.When = nil },
		func(effect *ProtocolSemanticEffect) { effect.Diagnostic = nil },
		func(effect *ProtocolSemanticEffect) { effect.Reason = "" },
		func(effect *ProtocolSemanticEffect) { effect.Target = "/model" },
		func(effect *ProtocolSemanticEffect) { effect.Scope = nodeScope },
		func(effect *ProtocolSemanticEffect) { effect.Default = StringValue("m") },
		func(effect *ProtocolSemanticEffect) { effect.Severity = SeverityError },
	} {
		document := textProtocolPackage(t)
		effect := packageWarning()
		change(&effect)
		mapping := document.Mapping.Directions[EncodeRequest]
		mapping.Effects = append(mapping.Effects, effect)
		document.Mapping.Directions[EncodeRequest] = mapping
		compiler, _ := NewCompiler(DefaultLimits(), nil)
		compiled, issues := compiler.CompilePackage(fixtureValue(t, document).Bytes())
		if compiled != nil || IssuesError(issues) == nil {
			t.Fatal("incomplete or mutating warning declaration compiled", issues)
		}
	}
}
