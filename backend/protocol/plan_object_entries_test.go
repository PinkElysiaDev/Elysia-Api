package protocol

import (
	"context"
	"errors"
	"testing"
)

func TestObjectEntriesRoundTrip(t *testing.T) {
	input := readExpression("input", "")
	entries := Expression{Op: "entries", Source: &input}
	output := Expression{Op: "from_entries", Source: &entries, Policy: "reject"}
	plan, _ := compileOutputPlan(compileTestOutputExpression(t, output))
	for _, fixture := range []struct{ input, expected string }{
		{`{}`, `{}`},
		{`{"b":1e+3,"":null,"a":false}`, `{"":null,"a":false,"b":1e+3}`},
		{`{"\u0061":0,"a":{"x":9007199254740993},"html":"<&"}`, `{"a":{"x":9007199254740993},"html":"\u003c\u0026"}`},
	} {
		value := parseTestValue(t, fixture.input)
		actual, err := plan.evaluate(evaluation{ctx: t.Context(), input: value, budget: &evaluationBudget{limits: DefaultLimits()}})
		if err != nil || actual.raw != fixture.expected || value.raw != fixture.input {
			t.Fatal("entry conversion changed members or caller input", actual.raw, err)
		}
	}
	for _, raw := range []string{`null`, `[]`, `"text"`} {
		_, err := plan.evaluate(evaluation{ctx: t.Context(), input: parseTestValue(t, raw), budget: &evaluationBudget{limits: DefaultLimits()}})
		if err == nil {
			t.Fatal("non-object silently became empty entries", raw)
		}
	}
}

func TestObjectEntriesDuplicatesAndDiagnostics(t *testing.T) {
	for _, policy := range []string{"reject", "identical"} {
		plan, _ := compileOutputPlan(compileTestOutputExpression(t, Expression{Op: "from_entries", Source: expressionPointer(readExpression("input", "")), Policy: policy,
			OnConflict: &MergeConflictDiagnostic{Code: UnsupportedCapability, Stage: "wire", PathPrefix: "/metadata", Reason: "duplicate metadata member"}}))
		for _, fixture := range []struct {
			input  string
			isSame bool
		}{
			{`[{"key":"a/b","value":1},{"key":"a/b","value":1}]`, true},
			{`[{"key":"a/b","value":1},{"key":"a/b","value":1.0}]`, false},
			{`[{"key":"a/b","value":null},{"key":"a/b","value":false}]`, false},
		} {
			_, err := plan.evaluate(evaluation{ctx: t.Context(), input: parseTestValue(t, fixture.input), budget: &evaluationBudget{limits: DefaultLimits()}})
			if policy == "identical" && fixture.isSame {
				if err != nil {
					t.Fatal(err)
				}
				continue
			}
			err = (&Compiled{}).mappingError(EncodeResponse, err)
			var failure *ConversionError
			var conflict *ObjectMergeConflictError
			if !errors.As(err, &failure) || failure.Issues[0].Path != "/metadata/a~1b" || failure.Issues[0].Code != UnsupportedCapability || !errors.As(err, &conflict) {
				t.Fatal("duplicate member lost its rejection or structured cause", err)
			}
		}
		for _, raw := range []string{`null`, `[{}]`, `[{"key":null,"value":1}]`, `[{"key":"a","value":1,"extra":false}]`} {
			_, err := plan.evaluate(evaluation{ctx: t.Context(), input: parseTestValue(t, raw), budget: &evaluationBudget{limits: DefaultLimits()}})
			if err == nil {
				t.Fatal("malformed entry accepted", raw)
			}
		}
	}
}

func TestObjectEntriesBudgetAndCancellation(t *testing.T) {
	plan, _ := compileOutputPlan(compileTestOutputExpression(t, Expression{Op: "entries", Source: expressionPointer(readExpression("input", ""))}))
	input := parseTestValue(t, `{"a":1,"b":2}`)
	limits := DefaultLimits()
	limits.BufferBytes = input.Len()
	_, err := plan.evaluate(evaluation{ctx: t.Context(), input: input, budget: &evaluationBudget{limits: limits}})
	var limit *ResourceLimitError
	if !errors.As(err, &limit) || limit.Metric != "bytes" {
		t.Fatal("entry expansion bypassed its budget", err)
	}
	_, err = plan.evaluate(evaluation{ctx: &outputPlanCancelContext{Context: t.Context(), remaining: 3}, input: input, budget: &evaluationBudget{limits: DefaultLimits()}})
	if !errors.Is(err, context.Canceled) {
		t.Fatal("entry traversal ignored cancellation", err)
	}
}
