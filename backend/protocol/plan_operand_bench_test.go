package protocol

import (
	"fmt"
	"strings"
	"testing"
)

func BenchmarkOperandWorkspaces(b *testing.B) {
	for _, operation := range []string{"all", "any", "merge", "concat", "integer", "join"} {
		for _, count := range []int{0, 2, 3, 4, 5, 16} {
			if count == 0 && operation != "join" {
				continue
			}
			b.Run(fmt.Sprintf("%s/%d", operation, count), func(b *testing.B) {
				expression := operandBenchmarkExpression(operation, count)
				plan, ok := compileOutputPlan(compileTestOutputExpression(b, expression))
				if !ok {
					b.Fatal("operand expression has no output plan")
				}
				ctx, limits := b.Context(), DefaultLimits()
				b.ReportAllocs()
				for b.Loop() {
					state := evaluation{ctx: ctx, budget: &evaluationBudget{limits: limits}}
					output, err := plan.build(state)
					if err != nil || output.output.IsZero() {
						b.Fatal("operand evaluation failed", err)
					}
				}
			})
		}
	}
}

func operandBenchmarkExpression(operation string, count int) Expression {
	expression := Expression{Op: operation, Items: make([]Expression, count)}
	if operation == "integer" {
		expression.Operation = "add"
	}
	if operation == "merge" {
		expression.Policy = "last"
	}
	for index := range expression.Items {
		switch operation {
		case "all", "any":
			expression.Items[index] = literalExpression(index%2 == 0)
		case "merge":
			expression.Items[index] = objectExpression(map[string]Expression{fmt.Sprintf("field%d", index): literalExpression(index)})
		case "concat":
			expression.Items[index] = literalExpression([]int{index})
		case "integer":
			expression.Items[index] = literalExpression(index)
		case "join":
			expression.Items[index] = literalExpression(strings.Repeat("x", index+1))
		}
	}
	return expression
}
