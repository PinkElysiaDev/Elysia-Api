package protocol

import (
	"context"
	"errors"
	"testing"
)

func TestTextComparisonPreservesDecodedContent(t *testing.T) {
	plan, _ := compileOutputPlan(compileTestOutputExpression(t, Expression{Op: "equal_text", Items: []Expression{
		readExpression("input", "/left"), readExpression("input", "/right"),
	}}))
	for _, fixture := range []struct {
		input    string
		expected bool
	}{
		{`{"left":"","right":[]}`, true},
		{`{"left":["","ab","c"],"right":["a","","bc",""]}`, true},
		{`{"left":"A\u0026\u2028界","right":["A","&","\u2028","界"]}`, true},
		{`{"left":["ab"],"right":["a"]}`, false},
		{`{"left":"a","right":"b"}`, false},
	} {
		output, err := plan.evaluate(evaluation{ctx: t.Context(), input: parseTestValue(t, fixture.input), budget: &evaluationBudget{limits: DefaultLimits()}})
		if err != nil || output != coreBoolean(fixture.expected) {
			t.Fatal(output, err)
		}
	}
	for _, raw := range []string{`{"left":null,"right":""}`, `{"right":""}`, `{"left":{},"right":""}`, `{"left":"a","right":["b",null]}`, `{"left":[],"right":[1]}`} {
		_, err := plan.evaluate(evaluation{ctx: t.Context(), input: parseTestValue(t, raw), budget: &evaluationBudget{limits: DefaultLimits()}})
		if err == nil {
			t.Fatal("invalid text was coerced or hidden by an earlier mismatch", raw)
		}
	}
	input := parseTestValue(t, `{"left":["a","b"],"right":"ab"}`)
	_, err := plan.evaluate(evaluation{ctx: &outputPlanCancelContext{Context: t.Context(), remaining: 4}, input: input, budget: &evaluationBudget{limits: DefaultLimits()}})
	if !errors.Is(err, context.Canceled) {
		t.Fatal("comparison ignored cancellation", err)
	}
	limits := DefaultLimits()
	limits.BufferBytes = 5
	_, err = plan.evaluate(evaluation{ctx: t.Context(), input: input, budget: &evaluationBudget{limits: limits}})
	var limit *ResourceLimitError
	if !errors.As(err, &limit) {
		t.Fatal("comparison escaped its operand budget", err)
	}
	for _, raw := range []string{`"\ud800"`, `"\ud83d\ude00"`, `"\u0000"`, "\"\xff\"", " \"text\" "} {
		leaf := JSONLeaf(Value{raw: raw})
		want, wantErr := readPlannedString(leaf)
		got, gotErr := readComparisonText(leaf)
		if want != got || (wantErr == nil) != (gotErr == nil) {
			t.Fatal("borrowed text changed JSON decoding", raw, want, got, wantErr, gotErr)
		}
	}
}
