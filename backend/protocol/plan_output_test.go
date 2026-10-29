package protocol

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func compileTestOutputExpression(t testing.TB, expression Expression) *compiledExpression {
	t.Helper()
	compiler := expressionCompiler{limits: DefaultLimits(), references: map[string]Expression{"identity": readExpression("input", "")}, resolving: map[string]bool{}, used: map[string]bool{}}
	compiled, err := compiler.compile(expression, "/mapping", expressionScope{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	return compiled
}

func TestOutputPlanParity(t *testing.T) {
	read := readExpression("input", "")
	item := readExpression("item", "")
	text := readExpression("item", "/text")
	object := objectExpression(map[string]Expression{"text": text, "missing": readExpression("item", "/missing")})
	mapObjects := Expression{Op: "map", Source: &read, Body: &object}
	mapItems := Expression{Op: "map", Source: &item, Body: &object}
	for _, tc := range []struct {
		name       string
		expression Expression
		input      string
	}{
		{"identity", read, ` { "n" : 9007199254740993, "n":1e+3 } `},
		{"reference", Expression{Op: "ref", Ref: "identity"}, ` [ 1, null ] `},
		{"literal", literalExpression("<>&\u2028"), `null`},
		{"omit", Expression{Op: "omit"}, `null`},
		{"empty-object", Expression{Op: "object"}, `null`},
		{"empty-array", Expression{Op: "array"}, `null`},
		{"nested", objectExpression(map[string]Expression{"z": {Op: "array", Items: []Expression{read, {Op: "omit"}, literalExpression(nil)}}, "a<&": {Op: "object", Fields: map[string]Expression{"n": readExpression("input", "/n")}}}), `{"n":9007199254740993,"text":"<>& ","dupe":1,"dupe":2}`},
		{"invalid-key-encoding", objectExpression(map[string]Expression{"bad\xff": read}), `"text"`},
		{"map", mapObjects, `[{"text":"<>& "},{"text":null},{}]`},
		{"map-raw", Expression{Op: "map", Source: &read, Body: &item}, `[ {"k":"<"}, [ 1, 2 ], null ]`},
		{"map-absent", mapObjects, `[{},{}]`},
		{"map-null", mapObjects, `null`},
		{"map-scalar", mapObjects, `false`},
		{"flatmap-raw", Expression{Op: "flatmap", Source: &read, Body: &item}, `[[ {"x":"<"} ],[],[1e+3,null]]`},
		{"flatmap-planned", Expression{Op: "flatmap", Source: &read, Body: &mapItems}, `[[{"text":"<"},{"text":"two"}],[],[{"text":"three"}]]`},
		{"flatmap-invalid", Expression{Op: "flatmap", Source: &read, Body: &item}, `[[1],true]`},
		{"if-object", Expression{Op: "if", When: expressionPointer(Expression{Op: "exists", Source: expressionPointer(readExpression("input", "/present"))}), Then: expressionPointer(objectExpression(map[string]Expression{"value": readExpression("input", "/present")}))}, `{"present":false}`},
		{"if-omit", Expression{Op: "if", When: expressionPointer(readExpression("input", "/flag")), Then: expressionPointer(Expression{Op: "array", Items: []Expression{read}})}, `{"flag":false}`},
		{"if-type", Expression{Op: "if", When: &read, Then: expressionPointer(Expression{Op: "array"})}, `0`},
		{"choose", Expression{Op: "choose", Source: expressionPointer(readExpression("input", "/type")), Cases: map[string]Expression{"text": {Op: "object", Fields: map[string]Expression{"content": readExpression("input", "/text")}}}}, `{"type":"text","text":"hello"}`},
		{"choose-missing", Expression{Op: "choose", Source: &read, Cases: map[string]Expression{"text": {Op: "object"}}}, `"unknown"`},
		{"choose-type", Expression{Op: "choose", Source: &read, Cases: map[string]Expression{"text": {Op: "object"}}}, `true`},
		{"required-order", objectExpression(map[string]Expression{"z": {Op: "read", Path: "/z"}, "a": {Op: "read", Path: "/a", Required: true}}), `{}`},
		{"merge-last", Expression{Op: "merge", Policy: "last", Items: []Expression{read, objectExpression(map[string]Expression{"a": literalExpression("<&"), "c": {Op: "array", Items: []Expression{read}}})}}, `{"a":1,"b": [ 1e+3 ],"a":2}`},
		{"merge-first", Expression{Op: "merge", Policy: "first", Items: []Expression{read, objectExpression(map[string]Expression{"a": literalExpression("new")})}}, `{"a":1}`},
		{"merge-collision", Expression{Op: "merge", Policy: "reject", Items: []Expression{read, read}}, `{"z":1,"a":2}`},
		{"merge-type", Expression{Op: "merge", Policy: "last", Items: []Expression{read}}, `null`},
		{"merge-priority", Expression{Op: "merge", Policy: "reject", Items: []Expression{read, {Op: "read", Path: "/missing", Required: true}}}, `{"a":1}`},
		{"concat", Expression{Op: "concat", Items: []Expression{read, {Op: "array", Items: []Expression{read}}, read}}, `[ {"k":"<"}, [ 1, 2 ], null ]`},
		{"concat-null", Expression{Op: "concat", Items: []Expression{read}}, `null`},
		{"filter", Expression{Op: "filter", Source: &read, When: expressionPointer(readExpression("item", "/keep"))}, `[{"keep":false},{"keep":true,"value":"<"}]`},
		{"filter-empty", Expression{Op: "filter", Source: &read, When: expressionPointer(literalExpression(false))}, `[null,1]`},
		{"filter-type", Expression{Op: "filter", Source: &read, When: expressionPointer(readExpression("item", "/keep"))}, `[{"keep":null}]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			compiled := compileTestOutputExpression(t, tc.expression)
			plan, ok := compileOutputPlan(compiled)
			if !ok {
				t.Fatal("eligible expression rejected")
			}
			input, err := ParseValue([]byte(tc.input))
			if err != nil {
				t.Fatal(err)
			}
			compareOutputPlan(t, compiled, plan, input, DefaultLimits(), 0, 0)
			for limit := 1; limit <= 96; limit++ {
				limits := DefaultLimits()
				limits.BufferBytes = limit
				compareOutputPlan(t, compiled, plan, input, limits, 0, 0)
			}
			for limit := 1; limit <= 12; limit++ {
				compareOutputPlan(t, compiled, plan, input, DefaultLimits(), limit, 0)
				compareOutputPlan(t, compiled, plan, input, DefaultLimits(), 0, limit)
			}
		})
	}
}

func compareOutputPlan(t *testing.T, expression *compiledExpression, plan *compiledOutputPlan, input Value, limits Limits, stepLimit, cancelAt int) {
	t.Helper()
	state := func() evaluation {
		var ctx context.Context = t.Context()
		if cancelAt != 0 {
			ctx = &outputPlanCancelContext{Context: ctx, remaining: cancelAt}
		}
		return evaluation{ctx: ctx, input: input, root: input, budget: &evaluationBudget{limits: limits, stepLimit: stepLimit}}
	}
	originalState, plannedState := state(), state()
	want, wantErr := expression.evaluate(originalState)
	got, gotErr := plan.evaluate(plannedState)
	if got != want || fmt.Sprint(gotErr) != fmt.Sprint(wantErr) || mappingErrorPath("", gotErr) != mappingErrorPath("", wantErr) || originalState.budget.steps != plannedState.budget.steps {
		t.Fatalf("buffer=%d steps=%d cancel=%d: got %s / %v (%s, %d steps), want %s / %v (%s, %d steps)", limits.BufferBytes, stepLimit, cancelAt, got.raw, gotErr, mappingErrorPath("", gotErr), plannedState.budget.steps, want.raw, wantErr, mappingErrorPath("", wantErr), originalState.budget.steps)
	}
	var wantLimit, gotLimit *ResourceLimitError
	if errors.As(wantErr, &wantLimit) != errors.As(gotErr, &gotLimit) || !reflect.DeepEqual(wantLimit, gotLimit) || errors.Is(wantErr, context.Canceled) != errors.Is(gotErr, context.Canceled) {
		t.Fatal("error chain changed", gotErr, wantErr)
	}
}

type outputPlanCancelContext struct {
	context.Context
	remaining int
}

func (ctx *outputPlanCancelContext) Err() error {
	ctx.remaining--
	if ctx.remaining <= 0 {
		return context.Canceled
	}
	return ctx.Context.Err()
}

func TestOutputPlanSelectsBeforeExecution(t *testing.T) {
	read := readExpression("input", "")
	for _, expression := range []Expression{
		{Op: "sort", Source: &read, Key: "/id"},
		{Op: "map", Source: expressionPointer(Expression{Op: "array"}), Body: &read},
		{Op: "if", When: expressionPointer(literalExpression(false)), Then: expressionPointer(Expression{Op: "sort", Source: &read, Key: "/id"}), Otherwise: expressionPointer(Expression{Op: "object"})},
	} {
		if _, ok := compileOutputPlan(compileTestOutputExpression(t, expression)); ok {
			t.Fatal("unsupported construction selected", expression.Op)
		}
	}
}

func TestOutputPlanLogicalNodeLimit(t *testing.T) {
	read := readExpression("input", "")
	expression := compileTestOutputExpression(t, Expression{Op: "flatmap", Source: &read, Body: expressionPointer(readExpression("item", ""))})
	plan, _ := compileOutputPlan(expression)
	input, _ := ParseValue([]byte(`[[1,2],[3,4]]`))
	limits := DefaultLimits()
	limits.Nodes = 3
	compareOutputPlan(t, expression, plan, input, limits, 100, 0)
}

func BenchmarkOutputPlanNestedPayload(b *testing.B) {
	expression := readExpression("item", "/text")
	for range 4 {
		expression = objectExpression(map[string]Expression{"nested": expression})
	}
	body := expression
	expression = Expression{Op: "map", Source: expressionPointer(readExpression("input", "")), Body: &body}
	compiled := compileTestOutputExpression(b, expression)
	plan, ok := compileOutputPlan(compiled)
	if !ok {
		b.Fatal("output plan not selected")
	}
	input, err := ParseValue([]byte(`[{"text":"` + strings.Repeat("p", 1<<20) + `"}]`))
	if err != nil {
		b.Fatal(err)
	}
	for _, isPlanned := range []bool{false, true} {
		name := "expression"
		if isPlanned {
			name = "output-plan"
		}
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				state := evaluation{ctx: b.Context(), input: input, root: input, budget: &evaluationBudget{limits: DefaultLimits()}}
				var value Value
				var err error
				if isPlanned {
					value, err = plan.evaluate(state)
				} else {
					value, err = compiled.evaluate(state)
				}
				if err != nil || value.Len() < 1<<20 {
					b.Fatal("incomplete nested output", err)
				}
			}
		})
	}
}
