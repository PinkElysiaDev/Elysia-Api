package protocol

import (
	"errors"
	"strings"
	"testing"
)

func TestKeysTypeAndNormalization(t *testing.T) {
	read := readExpression("input", "")
	expression := compileTestOutputExpression(t, Expression{Op: "keys", Source: &read})
	plan, _ := compileOutputPlan(expression)
	if expression.coreType != nil {
		t.Fatal("object keys acquired core provenance")
	}
	for _, raw := range []string{"null", "[]", "true", "0", `"object"`, ""} {
		state := evaluation{ctx: t.Context(), input: Value{raw: raw}, budget: &evaluationBudget{limits: DefaultLimits()}}
		if _, err := plan.evaluate(state); err == nil || !strings.Contains(err.Error(), "expected JSON object") {
			t.Fatal("non-object keys source was accepted", raw, err)
		}
	}
	for _, source := range []*Expression{nil, expressionPointer(literalExpression(nil)), expressionPointer(literalExpression([]int{})), expressionPointer(literalExpression("object"))} {
		compiler := expressionCompiler{limits: DefaultLimits()}
		if _, err := compiler.compile(Expression{Op: "keys", Source: source}, "/keys", expressionScope{}, 1); err == nil {
			t.Fatal("missing or statically non-object source compiled")
		}
	}
	for _, kind := range []JSONType{StringType, IntegerType} {
		err := checkExpressionOutput(expression, &ValueSchema{Type: ArrayType, Items: &ValueSchema{Type: kind}}, DefaultLimits())
		if (err == nil) != (kind == StringType) {
			t.Fatal("keys output type was not checked", kind, err)
		}
	}
	for _, raw := range []string{`{"bad\ufffd":0,"bad\ud800":1,"z":2}`, "{\"bad\xff\":0,\"bad\ufffd\":1,\"z\":2}"} {
		input := parseTestValue(t, raw)
		state := evaluation{ctx: t.Context(), input: input, budget: &evaluationBudget{limits: DefaultLimits()}}
		output, err := plan.evaluate(state)
		if err != nil || output != fixtureValue(t, []string{"bad\ufffd", "z"}) {
			t.Fatal("decoded key collisions were not normalized and folded", output, err)
		}
	}
}

func TestKeysOwnsNamesWithoutRetainingPayloads(t *testing.T) {
	expression := compileTestOutputExpression(t, Expression{Op: "keys", Source: expressionPointer(readExpression("input", ""))})
	plan, _ := compileOutputPlan(expression)
	input := Value{raw: `{"small":"` + strings.Repeat("<", 1<<20) + `"}`}
	reads := 0
	state := evaluation{ctx: t.Context(), budget: &evaluationBudget{limits: DefaultLimits()}, inputReader: func([]string) (JSONOutput, error) {
		reads++
		return JSONLeaf(input), nil
	}}
	output, err := plan.build(state)
	if err != nil || reads != 1 || state.budget.steps != 2 {
		t.Fatal("keys repeated its source or changed expression steps", reads, err)
	}
	if output.output.node.origin != 0 || len(output.output.node.items) != 1 {
		t.Fatal("keys retained the source's shape or provenance")
	}
	name := output.output.node.items[0].value
	if !name.IsString("small") || sharesValueStorage(input, name) {
		t.Fatal("a retained key pinned the source payload")
	}
}

func TestKeysMeasuresEscapingBeforeOutput(t *testing.T) {
	key := strings.Repeat("<", 128)
	input := Value{raw: `{"` + key + `":0}`}
	expected := fixtureValue(t, []string{key})
	expression := compileTestOutputExpression(t, Expression{Op: "keys", Source: expressionPointer(readExpression("input", ""))})
	plan, _ := compileOutputPlan(expression)
	for _, limit := range []int{input.Len(), expected.Len() - 1, expected.Len(), expected.Len() + 1} {
		limits := DefaultLimits()
		limits.BufferBytes = limit
		state := evaluation{ctx: t.Context(), input: input, budget: &evaluationBudget{limits: limits}}
		output, err := plan.evaluate(state)
		if limit >= expected.Len() {
			if err != nil || output != expected {
				t.Fatal("exact key output budget rejected", limit, output, err)
			}
			continue
		}
		var failure *ResourceLimitError
		if !errors.As(err, &failure) || failure.Domain != "evaluation" || failure.Metric != "bytes" || failure.Observed != expected.Len() || !output.IsZero() {
			t.Fatal("escaped key output bypassed its budget or changed the failure", limit, output, err)
		}
	}
}
