package protocol

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func TestScalarArrayConsumersMatchEagerEvaluator(t *testing.T) {
	for _, operation := range []string{"length", "join"} {
		for _, raw := range []string{
			`[]`, `["one","two","😀"]`, ` [ "<>&\u2028", "a\\b\"c" ] `,
			`[9007199254740993,1e100,null]`, `[{"a":1,"a":2},[]]`, `null`, `{}`, `true`,
			"[\"bad\xff\"]",
		} {
			expression := compileTestOutputExpression(t, Expression{Op: operation, Source: expressionPointer(readExpression("input", ""))})
			plan, _ := compileOutputPlan(expression)
			input := parseTestValue(t, raw)
			for _, limit := range []int{DefaultLimits().BufferBytes, input.Len(), max(1, input.Len()-1)} {
				state := func() evaluation {
					limits := DefaultLimits()
					limits.BufferBytes = limit
					return evaluation{ctx: t.Context(), input: input, root: input, budget: &evaluationBudget{limits: limits}}
				}
				original, candidate := state(), state()
				want, wantErr := scalarArrayOracle(plan, original)
				got, gotErr := plan.evaluate(candidate)
				if want != got || fmt.Sprint(wantErr) != fmt.Sprint(gotErr) || original.budget.steps != candidate.budget.steps {
					t.Fatal("scalar array semantics changed", operation, raw, limit, want, got, wantErr, gotErr)
				}
			}
		}
	}
}

func scalarArrayOracle(plan *compiledOutputPlan, state evaluation) (Value, error) {
	if err := state.advance(); err != nil {
		return Value{}, locateMappingError(plan.expression.path, err)
	}
	source, err := plan.source.buildNode(state)
	if err != nil {
		return Value{}, locateMappingError(plan.expression.path, err)
	}
	items, err := plannedArrayItems(source.output)
	if err != nil {
		return Value{}, locateMappingError(plan.expression.path, err)
	}
	result := coreInteger(int64(len(items)))
	if plan.expression.op == "join" {
		var joined strings.Builder
		for _, item := range items {
			text, err := readComparisonText(item)
			if err != nil {
				return Value{}, locateMappingError(plan.expression.path, err)
			}
			if joined.Len()+len(text) > state.budget.limits.BufferBytes {
				return Value{}, locateMappingError(plan.expression.path, resourceFailure("evaluation", "bytes", state.budget.limits.BufferBytes, joined.Len()+len(text)))
			}
			joined.WriteString(text)
		}
		result = StringValue(joined.String())
	}
	if result.Len() > state.budget.limits.BufferBytes {
		return Value{}, locateMappingError(plan.expression.path, resourceFailure("evaluation", "bytes", state.budget.limits.BufferBytes, result.Len()))
	}
	return result, nil
}

func TestScalarArrayJoinOwnsResultAndPreservesCancellation(t *testing.T) {
	input := parseTestValue(t, `["`+strings.Repeat("x", 128<<10)+`","end"]`)
	expression := compileTestOutputExpression(t, Expression{Op: "join", Source: expressionPointer(readExpression("input", ""))})
	plan, _ := compileOutputPlan(expression)
	result, err := plan.evaluate(numericTestState(t, input))
	if err != nil || result.Len() != (128<<10)+5 || sharesValueStorage(input, result) {
		t.Fatal("join retained input or changed output", result.Len(), err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	state := numericTestState(t, input)
	state.ctx = ctx
	_, wantErr := scalarArrayOracle(plan, state)
	state = numericTestState(t, input)
	state.ctx = ctx
	_, gotErr := plan.evaluate(state)
	if fmt.Sprint(gotErr) != fmt.Sprint(wantErr) {
		t.Fatal("cancellation changed", gotErr, wantErr)
	}
}
