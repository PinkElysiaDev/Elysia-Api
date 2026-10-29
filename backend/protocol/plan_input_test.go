package protocol

import (
	"fmt"
	"strings"
	"testing"
)

func TestOutputPlanReadsConstructedInput(t *testing.T) {
	read := readExpression("input", "/items")
	item := readExpression("item", "")
	input := JSONObject(map[string]JSONOutput{
		"items": JSONArray([]JSONOutput{
			JSONObject(map[string]JSONOutput{"text": JSONLeaf(StringValue("first <text>")), "keep": JSONLeaf(controlValue(true))}),
			JSONObject(map[string]JSONOutput{"text": JSONLeaf(StringValue("second")), "keep": JSONLeaf(controlValue(false))}),
		}),
	})
	encoded, err := input.Encode(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, expression := range []Expression{
		readExpression("input", ""), readExpression("root", "/items/0/text"),
		readExpression("input", "/items/0/missing"), readExpression("input", "/items/00"),
		{Op: "read", Path: "/absent", Required: true},
		{Op: "map", Source: &read, Body: &item},
		{Op: "map", Source: &read, Body: expressionPointer(objectExpression(map[string]Expression{"content": readExpression("item", "/text")}))},
		{Op: "filter", Source: &read, When: expressionPointer(readExpression("item", "/keep"))},
		{Op: "map", Source: expressionPointer(Expression{Op: "map", Source: &read, Body: &item}), Body: &item},
	} {
		compiled := compileTestOutputExpression(t, expression)
		plan, ok := compileOutputPlan(compiled)
		if !ok {
			t.Fatal("constructed input plan was not compiled", expression.Op)
		}
		for buffer := 1; buffer <= encoded.Len()+32; buffer++ {
			limits := DefaultLimits()
			limits.BufferBytes = buffer
			original := evaluation{ctx: t.Context(), input: encoded, root: encoded, budget: &evaluationBudget{limits: limits}}
			view := evaluation{ctx: t.Context(), budget: &evaluationBudget{limits: limits}}
			view.inputReader = func(pointer []string) (JSONOutput, error) { return readOutputPointer(input, pointer, nil) }
			view.rootReader = view.inputReader
			want, wantErr := compiled.evaluate(original)
			got, gotErr := plan.evaluate(view)
			if got != want || fmt.Sprint(gotErr) != fmt.Sprint(wantErr) || view.budget.steps != original.budget.steps {
				t.Fatalf("%s, buffer=%d: got %s / %v (%d steps), want %s / %v (%d steps)", expression.Op, buffer, got.raw, gotErr, view.budget.steps, want.raw, wantErr, original.budget.steps)
			}
		}
	}
}

func TestOutputPlanRootObjectReadScope(t *testing.T) {
	root := Value{raw: `{"a":{"n":1},"b":2}`}
	for _, tc := range []struct {
		name       string
		expression Expression
		reads      uint8
	}{
		{"identity", readExpression("input", ""), 0},
		{"single", readExpression("input", "/a"), 1},
		{"context", objectExpression(map[string]Expression{"a": readExpression("context", "/a"), "b": readExpression("context", "/b")}), 0},
		{"root-and-input", objectExpression(map[string]Expression{"a": readExpression("input", "/a"), "b": readExpression("root", "/b"), "again": readExpression("input", "/a")}), rootObjectReuseReads},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan, _ := compileOutputPlan(compileTestOutputExpression(t, tc.expression))
			if plan.rootObjectReads != tc.reads {
				t.Fatalf("root reads=%d, want %d", plan.rootObjectReads, tc.reads)
			}
			objects := plan.newRootObjectReader(root)
			if (objects != nil) != (tc.reads == rootObjectReuseReads) || plan.newRootObjectReader(Value{raw: `[]`}) != nil {
				t.Fatal("root reader eligibility changed")
			}
			if objects == nil {
				return
			}
			copied := Value{raw: strings.Clone(root.raw)}
			value, err := readValuePointerWithObjectReader(copied, []string{"a", "n"}, nil, objects)
			if err != nil || value.raw != "1" || objects.isLoaded {
				t.Fatal("equal contents reused a different root's storage", value, err)
			}
			for _, input := range []struct {
				value Value
				want  string
			}{{root, "1"}, {Value{raw: `{"a":{"n":3}}`}, "3"}, {root, "1"}} {
				value, err := readValuePointerWithObjectReader(input.value, []string{"a", "n"}, nil, objects)
				if err != nil || value.raw != input.want || !objects.isLoaded {
					t.Fatal("root reader crossed storage boundaries", value, err)
				}
			}
		})
	}
}
