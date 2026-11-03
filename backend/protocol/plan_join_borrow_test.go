package protocol

import (
	"fmt"
	"strings"
	"testing"
)

func TestBorrowedJoinPreservesBoundaries(t *testing.T) {
	expression := compileTestOutputExpression(t, Expression{Op: "join", Source: expressionPointer(readExpression("input", ""))})
	plan, _ := compileOutputPlan(expression)
	for _, raw := range []string{`["plain","<>&😀"]`, `["\ud800","\ud83d\ude00","\u0000"]`, "[\"\xff\",\"end\"]", `["a",null]`, `[1,"a"]`} {
		for _, limit := range []int{3, 10, DefaultLimits().BufferBytes} {
			input := parseTestValue(t, raw)
			state := numericTestState(t, input)
			state.budget.limits.BufferBytes = limit
			got, err := plan.evaluate(state)
			steps := state.budget.steps
			state.budget.steps = 0
			want, wantErr := expression.evaluate(state)
			if got != want || fmt.Sprint(err) != fmt.Sprint(wantErr) || steps != state.budget.steps {
				t.Fatal("borrowed join changed content, error or accounting", raw, got, want, err, wantErr)
			}
		}
	}
	input := Value{raw: strings.Repeat(" ", 1<<20) + `["small",""]`}
	output, err := plan.evaluate(numericTestState(t, input))
	if err != nil || !output.IsString("small") || sharesValueStorage(input, output) {
		t.Fatal("joined output retained the source body", err)
	}
}

func BenchmarkJoinTextRead(b *testing.B) {
	for _, fixture := range []struct {
		name string
		text Value
	}{
		{"short", StringValue("hello")},
		{"ascii", StringValue(strings.Repeat("abc", 4096))},
		{"unicode", StringValue(strings.Repeat("世界😀", 1024))},
		{"escaped", Value{raw: `"` + strings.Repeat(`\u0061\n`, 512) + `"`}},
	} {
		b.Run(fixture.name, func(b *testing.B) {
			literal := Expression{Op: "literal", Value: fixture.text}
			expression := compileTestOutputExpression(b, Expression{Op: "join", Items: []Expression{literal, literal}})
			plan, _ := compileOutputPlan(expression)
			b.ReportAllocs()
			for b.Loop() {
				state := evaluation{ctx: b.Context(), budget: &evaluationBudget{limits: DefaultLimits()}}
				output, err := plan.evaluate(state)
				if err != nil || output.IsZero() {
					b.Fatal("join failed", err)
				}
			}
		})
	}
}
