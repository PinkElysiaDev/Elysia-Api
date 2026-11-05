package protocol

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func mergeDiagnosticExpression() Expression {
	return Expression{Op: "merge_deep", Items: []Expression{readExpression("input", "/left"), readExpression("input", "/right")},
		OnConflict: &MergeConflictDiagnostic{Code: UnsupportedCapability, Stage: "wire", PathPrefix: "/generationConfig", Reason: "extension collides with a mapped field"}}
}

func TestDeepMergeDeclaredDiagnostic(t *testing.T) {
	expression := mergeDiagnosticExpression()
	var decoded Expression
	if err := decodeContract(fixtureValue(t, expression).Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	plan, _ := compileOutputPlan(compileTestOutputExpression(t, decoded))
	decoded.OnConflict.Reason = "caller edit"
	identity := Identity{DefinitionID: "merge-diagnostic", Family: "synthetic", Revision: "1"}
	compiled := &Compiled{identity: identity}
	input := parseTestValue(t, `{"left":{"a/b":{"~key":"private-left"}},"right":{"a/b":{"~key":"private-right"}}}`)
	_, err := plan.evaluate(evaluation{ctx: t.Context(), input: input, budget: &evaluationBudget{limits: DefaultLimits()}})
	err = compiled.mappingError(EncodeRequest, err)
	var conversion *ConversionError
	var conflict *ObjectMergeConflictError
	if !errors.As(err, &conversion) || len(conversion.Issues) != 1 || !errors.As(err, &conflict) || conflict.Path != "/a~1b/~0key" {
		t.Fatal("declared conflict lost its original cause", err)
	}
	issue := conversion.Issues[0]
	if issue.Code != UnsupportedCapability || issue.Stage != "wire" || issue.Path != "/generationConfig/a~1b/~0key" || issue.Reason != expression.OnConflict.Reason || issue.Protocol != identity || issue.Direction != EncodeRequest || strings.Contains(err.Error(), "private-") {
		t.Fatal("declared conflict lost its business location or runtime attribution", issue)
	}
	if errors.Unwrap(err) == nil {
		t.Fatal("declared conflict lost its unwrap chain")
	}
	input = parseTestValue(t, `{"left":{"a":1},"right":{"b":2}}`)
	output, err := plan.evaluate(evaluation{ctx: t.Context(), input: input, budget: &evaluationBudget{limits: DefaultLimits()}})
	if err != nil || output.raw != `{"a":1,"b":2}` {
		t.Fatal("diagnostic changed successful merging", output, err)
	}
}

func TestDeepMergeDiagnosticKeepsOperandFailure(t *testing.T) {
	for _, hasInnerDiagnostic := range []bool{false, true} {
		inner := mergeDiagnosticExpression()
		inner.OnConflict = nil
		if hasInnerDiagnostic {
			inner.OnConflict = &MergeConflictDiagnostic{Code: ConversionRejected, Stage: "evaluate", PathPrefix: "/inner", Reason: "inner collision"}
		}
		outer := mergeDiagnosticExpression()
		outer.Items = []Expression{inner, literalExpression(Object{})}
		plan, _ := compileOutputPlan(compileTestOutputExpression(t, outer))
		input := parseTestValue(t, `{"left":{"x":1},"right":{"x":1}}`)
		_, err := plan.evaluate(evaluation{ctx: t.Context(), input: input, budget: &evaluationBudget{limits: DefaultLimits()}})
		err = (&Compiled{}).mappingError(EncodeRequest, err)
		var conversion *ConversionError
		var conflict *ObjectMergeConflictError
		if !errors.As(err, &conversion) || len(conversion.Issues) != 1 || !errors.As(err, &conflict) || conflict.Path != "/x" {
			t.Fatal("inner merge lost its failure", err)
		}
		issue := conversion.Issues[0]
		if hasInnerDiagnostic {
			if issue.Code != ConversionRejected || issue.Path != "/inner/x" || issue.Reason != "inner collision" {
				t.Fatal("outer diagnostic replaced an inner diagnostic", issue)
			}
		} else if issue.Code != InvalidInput || issue.Stage != "evaluate" || issue.Path != "/mapping/items/0" {
			t.Fatal("outer diagnostic classified an operand's conflict", issue)
		}
	}
	plan, _ := compileOutputPlan(compileTestOutputExpression(t, mergeDiagnosticExpression()))
	original := &ConversionError{CandidateOnly: true, Issues: []ConversionIssue{{Code: UnsupportedNative, Severity: SeverityError, Stage: "preserve", Path: "/native", Reason: "synthetic source rejection"}}}
	_, err := plan.evaluate(evaluation{ctx: t.Context(), budget: &evaluationBudget{limits: DefaultLimits()}, inputReader: func([]string) (JSONOutput, error) {
		return JSONOutput{}, original
	}})
	err = (&Compiled{}).mappingError(EncodeRequest, err)
	var conversion *ConversionError
	if !errors.Is(err, original) || !errors.As(err, &conversion) || conversion != original {
		t.Fatal("outer diagnostic changed an existing conversion error", err)
	}
}

func TestDeepMergeDiagnosticKeepsLimitsCancellationAndTypes(t *testing.T) {
	plan, _ := compileOutputPlan(compileTestOutputExpression(t, mergeDiagnosticExpression()))
	input := parseTestValue(t, `{"left":{"n":{"a":1}},"right":{"n":{"b":2}}}`)
	limits := DefaultLimits()
	limits.Depth = 1
	_, err := plan.evaluate(evaluation{ctx: t.Context(), input: input, budget: &evaluationBudget{limits: limits}})
	err = (&Compiled{}).mappingError(EncodeRequest, err)
	var limit *ResourceLimitError
	var conversion *ConversionError
	if !errors.As(err, &limit) || limit.Metric != "depth" || !errors.As(err, &conversion) || conversion.Issues[0].Code != LimitExceeded {
		t.Fatal("merge diagnostic hid a resource failure", err)
	}
	ctx := &outputPlanCancelContext{Context: t.Context(), remaining: 6}
	_, err = plan.evaluate(evaluation{ctx: ctx, input: input, budget: &evaluationBudget{limits: DefaultLimits()}})
	err = (&Compiled{}).mappingError(EncodeRequest, err)
	if !errors.Is(err, context.Canceled) || errors.As(err, &conversion) {
		t.Fatal("merge diagnostic hid cancellation", err)
	}
	input = parseTestValue(t, `{"left":{},"right":null}`)
	_, err = plan.evaluate(evaluation{ctx: t.Context(), input: input, budget: &evaluationBudget{limits: DefaultLimits()}})
	err = (&Compiled{}).mappingError(EncodeRequest, err)
	if !errors.As(err, &conversion) || conversion.Issues[0].Code != InvalidInput || conversion.Issues[0].Stage != "evaluate" || !strings.Contains(err.Error(), "expected JSON object") {
		t.Fatal("merge diagnostic reclassified a non-object operand", err)
	}
}

func TestCompileMergeConflictDiagnostic(t *testing.T) {
	for _, fixture := range []struct {
		name string
		edit func(*Expression)
	}{
		{"unrelated-operation", func(e *Expression) { e.Op, e.Policy = "merge", "reject" }},
		{"resource-code", func(e *Expression) { e.OnConflict.Code = LimitExceeded }},
		{"unrelated-stage", func(e *Expression) { e.OnConflict.Stage = "routing" }},
		{"invalid-path", func(e *Expression) { e.OnConflict.PathPrefix = "not-a-pointer" }},
		{"missing-reason", func(e *Expression) { e.OnConflict.Reason = "" }},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			expression := mergeDiagnosticExpression()
			fixture.edit(&expression)
			compiler := expressionCompiler{limits: DefaultLimits()}
			if _, err := compiler.compilePlan(expression, "/mapping", expressionScope{}, 1); err == nil {
				t.Fatal("invalid conflict diagnostic compiled")
			}
		})
	}
	var expression Expression
	if err := decodeContract([]byte(`{"op":"merge_deep","items":[{"op":"literal","value":{}}],"onConflict":null}`), &expression); err != nil {
		t.Fatal(err)
	}
	compiler := expressionCompiler{limits: DefaultLimits()}
	if _, err := compiler.compilePlan(expression, "/mapping", expressionScope{}, 1); err == nil || !strings.Contains(err.Error(), "requires a diagnostic object") {
		t.Fatal("null diagnostic was silently ignored", err)
	}
}
