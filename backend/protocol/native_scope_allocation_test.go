package protocol

import (
	"strings"
	"testing"
)

func TestNativeCaptureThroughIndirectRecursion(t *testing.T) {
	item := readExpression("item", "")
	children := readExpression("item", "/content")
	recurse := Expression{Op: "recur", Source: expressionPointer(readExpression("item", "/descendants"))}
	body := objectExpression(map[string]Expression{
		"source": {Op: "capture_source", Source: &item},
		"children": {Op: "if", When: expressionPointer(Expression{Op: "exists", Source: &children}),
			Then: expressionPointer(Expression{Op: "flatmap", Source: &children, Body: &recurse})},
	})
	compiler := expressionCompiler{limits: DefaultLimits()}
	plan, err := compiler.compilePlan(Expression{Op: "walk", Source: expressionPointer(readExpression("input", "/content")), Body: &body}, "/scope", expressionScope{wireCapture: true}, 1)
	if err != nil {
		t.Fatal(err)
	}
	input := parseTestValue(t, `{"content":[{"content":[{"descendants":[{"label":"nested"}]}]},{"label":"last"}]}`)
	state := numericTestState(t, input)
	state.capture = &nativeCaptureState{isNativeDisabled: true}
	output, err := plan.evaluate(state)
	if err != nil {
		t.Fatal(err)
	}
	var nodes []Node
	if err := output.Decode(&nodes); err != nil {
		t.Fatal(err)
	}
	if nodes[0].Source.Path != "/content/0" || nodes[0].Children[0].Source.Path != "/content/0/content/0/descendants/0" || nodes[1].Source.Path != "/content/1" {
		t.Fatal("indirect recursion lost source position", output.JSONString())
	}
}

func BenchmarkNativeIterationScopes(b *testing.B) {
	input := Value{raw: `{"content":[` + strings.Repeat(`{"label":"plain"},`, 31) + `{"label":"plain"}]}`}
	for _, operation := range []string{"read", "capture_source", "capture_native", "disabled_native"} {
		b.Run(operation, func(b *testing.B) {
			body := readExpression("item", "")
			if operation != "read" {
				op := operation
				if op == "disabled_native" {
					op = "capture_native"
				}
				body = Expression{Op: op, Source: expressionPointer(body)}
			}
			compiler := expressionCompiler{limits: DefaultLimits()}
			plan, err := compiler.compilePlan(Expression{Op: "map", Source: expressionPointer(readExpression("input", "/content")), Body: &body}, "/scope", expressionScope{wireCapture: true}, 1)
			if err != nil {
				b.Fatal(err)
			}
			capture := &nativeCaptureState{isNativeDisabled: operation == "disabled_native"}
			b.ReportAllocs()
			for b.Loop() {
				state := evaluation{ctx: b.Context(), input: input, root: input, capture: capture, budget: &evaluationBudget{limits: DefaultLimits()}}
				output, err := plan.build(state)
				if err != nil || output.output.IsZero() {
					b.Fatal("iteration failed", err)
				}
			}
		})
	}
}
