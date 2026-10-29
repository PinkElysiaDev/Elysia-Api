package protocol

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func deepMergeTestPlan(t testing.TB) *compiledOutputPlan {
	t.Helper()
	expression := compileTestOutputExpression(t, Expression{Op: "merge_deep", Items: []Expression{readExpression("input", "/left"), readExpression("input", "/right")}})
	plan, _ := compileOutputPlan(expression)
	return plan
}

func TestDeepMergeObjectContract(t *testing.T) {
	plan := deepMergeTestPlan(t)
	for _, test := range []struct{ input, want string }{
		{`{"left":{},"right":{}}`, `{}`},
		{`{"left":{"outer":{"x": 1e+3},"array":[1,2],"z":null},"right":{"outer":{"y":"<&"}}}`, `{"array":[1,2],"outer":{"x":1e+3,"y":"\u003c\u0026"},"z":null}`},
		{`{"left":{"a":0,"\u0061":{"x":1}},"right":{"a":{"y":2}}}`, `{"a":{"x":1,"y":2}}`},
	} {
		input := parseTestValue(t, test.input)
		state := evaluation{ctx: t.Context(), input: input, budget: &evaluationBudget{limits: DefaultLimits()}}
		output, err := plan.evaluate(state)
		if err != nil || output.raw != test.want || input.raw != test.input {
			t.Fatal("object union or normalization changed", output.raw, err)
		}
	}
	for _, leaf := range []string{`1`, `true`, `null`, `[]`, `[1]`, `"same"`} {
		input := parseTestValue(t, `{"left":{"a/b":{"~key":`+leaf+`}},"right":{"a/b":{"~key":`+leaf+`}}}`)
		state := evaluation{ctx: t.Context(), input: input, budget: &evaluationBudget{limits: DefaultLimits()}}
		_, err := plan.evaluate(state)
		var conflict *ObjectMergeConflictError
		if !errors.As(err, &conflict) || conflict.Path != "/a~1b/~0key" || mappingErrorPath("", err) != "/mapping" {
			t.Fatal("equal leaves did not retain a structured conflict", leaf, err)
		}
	}
	for _, input := range []string{`{"left":{},"right":null}`, `{"left":{},"right":[]}`, `{"left":{}}`} {
		state := evaluation{ctx: t.Context(), input: parseTestValue(t, input), budget: &evaluationBudget{limits: DefaultLimits()}}
		if _, err := plan.evaluate(state); err == nil {
			t.Fatal("non-object operand became an empty object", input)
		}
	}
	for _, expression := range []Expression{
		{Op: "merge_deep", Items: []Expression{literalExpression(nil)}},
		{Op: "merge_deep", Items: []Expression{objectExpression(nil)}, Policy: "last"},
	} {
		compiler := expressionCompiler{limits: DefaultLimits()}
		if _, err := compiler.compile(expression, "/merge", expressionScope{}, 1); err == nil {
			t.Fatal("invalid deep merge contract compiled")
		}
	}
}

func TestDeepMergeBudgetAndCancellation(t *testing.T) {
	plan := deepMergeTestPlan(t)
	input := parseTestValue(t, `{"left":{"n":{"a":1}},"right":{"n":{"b":2}}}`)
	for _, test := range []struct {
		metric string
		change func(*evaluationBudget)
	}{
		{"depth", func(budget *evaluationBudget) { budget.limits.Depth = 1 }},
		{"nodes", func(budget *evaluationBudget) { budget.limits.Nodes = 3; budget.stepLimit = 100 }},
		{"steps", func(budget *evaluationBudget) { budget.stepLimit = 6 }},
		{"bytes", func(budget *evaluationBudget) { budget.limits.BufferBytes = 20 }},
	} {
		t.Run(test.metric, func(t *testing.T) {
			budget := evaluationBudget{limits: DefaultLimits()}
			test.change(&budget)
			_, err := plan.evaluate(evaluation{ctx: t.Context(), input: input, budget: &budget})
			var limit *ResourceLimitError
			if !errors.As(err, &limit) || limit.Domain != "evaluation" || limit.Metric != test.metric {
				t.Fatal("merge bypassed its budget", err)
			}
		})
	}
	ctx := &outputPlanCancelContext{Context: t.Context(), remaining: 6}
	_, err := plan.evaluate(evaluation{ctx: ctx, input: input, budget: &evaluationBudget{limits: DefaultLimits()}})
	if !errors.Is(err, context.Canceled) {
		t.Fatal("merge lost mid-operation cancellation", err)
	}
	key := strings.Repeat("<", 8)
	input = parseTestValue(t, `{"left":{"`+key+`":0},"right":{}}`)
	want := fixtureValue(t, map[string]int{key: 0})
	for _, limit := range []int{want.Len() - 1, want.Len()} {
		budget := evaluationBudget{limits: DefaultLimits()}
		budget.limits.BufferBytes = limit
		output, err := plan.evaluate(evaluation{ctx: t.Context(), input: input, budget: &budget})
		if limit == want.Len() {
			if err != nil || output != want {
				t.Fatal("exact normalized output limit failed", output, err)
			}
		} else {
			var failure *ResourceLimitError
			if !errors.As(err, &failure) || failure.Metric != "bytes" || failure.Observed != want.Len() {
				t.Fatal("normalized keys escaped the byte budget", err)
			}
		}
	}
}

func TestDeepMergePreservesEvaluationOrderAndCause(t *testing.T) {
	plan := deepMergeTestPlan(t)
	sentinel := errors.New("synthetic read failure")
	reads := 0
	state := evaluation{ctx: t.Context(), budget: &evaluationBudget{limits: DefaultLimits()}, inputReader: func(path []string) (JSONOutput, error) {
		reads++
		if path[0] == "left" {
			return JSONLeaf(Value{raw: "null"}), nil
		}
		return JSONOutput{}, sentinel
	}}
	_, err := plan.build(state)
	if reads != 2 || !errors.Is(err, sentinel) {
		t.Fatal("operand type checking replaced a later evaluation failure", reads, err)
	}
	state.inputReader = func([]string) (JSONOutput, error) { return JSONLeaf(Value{raw: `{"x":1}`}), nil }
	_, err = plan.build(state)
	compiled := &Compiled{}
	err = compiled.mappingError(EncodeRequest, err)
	var conflict *ObjectMergeConflictError
	var conversion *ConversionError
	if !errors.As(err, &conflict) || conflict.Path != "/x" || !errors.As(err, &conversion) || conversion.Issues[0].Code != InvalidInput {
		t.Fatal("generic conflict classification or cause changed", err)
	}
}

func TestDeepMergeSharesUnchangedChildren(t *testing.T) {
	opaque := JSONObject(map[string]JSONOutput{"payload": JSONLeaf(StringValue(strings.Repeat("x", 1<<20)))})
	overlap := JSONObject(map[string]JSONOutput{"left": JSONLeaf(Value{raw: "1"})})
	left := JSONObject(map[string]JSONOutput{"opaque": opaque, "overlap": overlap})
	left.node.origin, overlap.node.origin = capturedNativeOrigin, capturedNativeOrigin
	right := JSONObject(map[string]JSONOutput{"overlap": JSONObject(map[string]JSONOutput{"right": JSONLeaf(Value{raw: "2"})})})
	state := evaluation{ctx: t.Context(), budget: &evaluationBudget{limits: DefaultLimits()}, inputReader: func(path []string) (JSONOutput, error) {
		if path[0] == "left" {
			return left, nil
		}
		return right, nil
	}}
	output, err := deepMergeTestPlan(t).build(state)
	if err != nil || output.output.node.origin != 0 || output.output.node.fields[0].value.node != opaque.node ||
		output.output.node.fields[1].value.node.origin != 0 || len(overlap.node.fields) != 1 || left.node.origin != capturedNativeOrigin {
		t.Fatal("merge copied an opaque subtree, mutated its input or inherited origin", err)
	}
}

// Raw overlapping ancestors are normalized at each visited level. Compare them
// with already constructed containers; neither variant encodes intermediate trees.
func BenchmarkDeepMergeNestedPayload(b *testing.B) {
	for _, depth := range []int{1, 4, 16} {
		left := JSONObject(map[string]JSONOutput{"payload": JSONLeaf(StringValue(strings.Repeat("x", 2<<20)))})
		right := JSONObject(map[string]JSONOutput{"extra": JSONLeaf(Value{raw: "true"})})
		for range depth {
			left = JSONObject(map[string]JSONOutput{"nested": left})
			right = JSONObject(map[string]JSONOutput{"nested": right})
		}
		leftRaw, _ := left.Encode(b.Context())
		rightRaw, _ := right.Encode(b.Context())
		for _, isRaw := range []bool{false, true} {
			b.Run(fmt.Sprintf("depth-%d/raw-%t", depth, isRaw), func(b *testing.B) {
				plan := deepMergeTestPlan(b)
				reader := func(path []string) (JSONOutput, error) {
					if path[0] == "left" {
						if isRaw {
							return JSONLeaf(leftRaw), nil
						}
						return left, nil
					}
					if isRaw {
						return JSONLeaf(rightRaw), nil
					}
					return right, nil
				}
				b.ReportAllocs()
				for b.Loop() {
					state := evaluation{ctx: b.Context(), budget: &evaluationBudget{limits: DefaultLimits()}, inputReader: reader}
					value, err := plan.evaluate(state)
					if err != nil || value.Len() < 2<<20 {
						b.Fatal("incomplete deep merge", value.Len(), err)
					}
				}
			})
		}
	}
}
