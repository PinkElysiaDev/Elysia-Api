package protocol

import (
	"context"
	"errors"
	"testing"
)

func TestGroupRunsPreservesOrderAndViews(t *testing.T) {
	source, predicate := readExpression("input", ""), readExpression("item", "/group")
	plan, _ := compileOutputPlan(compileTestOutputExpression(t, Expression{Op: "group_runs", Source: &source, When: &predicate}))
	for _, fixture := range []struct{ input, expected string }{
		{`[]`, `[]`},
		{`[{"group":true,"n":9007199254740993},{"group":true,"n":1e3},{"group":false},{"group":true}]`, `[[{"group":true,"n":9007199254740993},{"group":true,"n":1e3}],[{"group":false}],[{"group":true}]]`},
	} {
		input := parseTestValue(t, fixture.input)
		output, err := plan.evaluate(evaluation{ctx: t.Context(), input: input, budget: &evaluationBudget{limits: DefaultLimits()}})
		if err != nil || output.raw != fixture.expected || input.raw != fixture.input {
			t.Fatal(output.raw, err)
		}
	}
	for _, raw := range []string{`null`, `{}`, `[{}]`, `[{"group":null}]`, `[{"group":1}]`} {
		_, err := plan.evaluate(evaluation{ctx: t.Context(), input: parseTestValue(t, raw), budget: &evaluationBudget{limits: DefaultLimits()}})
		if err == nil {
			t.Fatal("invalid source or predicate became a group", raw)
		}
	}
	input := parseTestValue(t, `[{"group":true},{"group":false}]`)
	limits := DefaultLimits()
	limits.BufferBytes = input.Len()
	_, err := plan.evaluate(evaluation{ctx: t.Context(), input: input, budget: &evaluationBudget{limits: limits}})
	var limit *ResourceLimitError
	if !errors.As(err, &limit) || limit.Metric != "bytes" {
		t.Fatal("group containers escaped the buffer limit", err)
	}
	_, err = plan.evaluate(evaluation{ctx: &outputPlanCancelContext{Context: t.Context(), remaining: 3}, input: input, budget: &evaluationBudget{limits: DefaultLimits()}})
	if !errors.Is(err, context.Canceled) {
		t.Fatal("group traversal ignored cancellation", err)
	}
}
