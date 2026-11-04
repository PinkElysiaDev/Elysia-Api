package protocol

import (
	"fmt"
	"testing"
)

func BenchmarkScopedConsumers(b *testing.B) {
	input := Value{raw: `{"a":"one","b":"two","items":[{"a":"one","b":"two"},{"a":"one","b":"two"}],"text":["one","two","😀"]}`}
	read := readExpression("input", "")
	object := objectExpression(map[string]Expression{"z": literalExpression("last")})
	project := Expression{Op: "project", Source: &read, Keys: []string{"items", "text"}, Policy: "exclude"}
	for name, source := range map[string]Expression{"read": read, "project": project, "object": objectExpression(map[string]Expression{"a": literalExpression("one"), "b": literalExpression("two")})} {
		for _, policy := range []string{"first", "last", "reject"} {
			b.Run("merge-"+name+"-"+policy, func(b *testing.B) {
				benchmarkScopedConsumer(b, Expression{Op: "merge", Items: []Expression{source, object}, Policy: policy}, input)
			})
		}
	}
	b.Run("project-fields", func(b *testing.B) {
		compiled := compileTestOutputExpression(b, project)
		plan, _ := compileOutputPlan(compiled)
		b.ReportAllocs()
		for b.Loop() {
			output, err := plan.build(evaluation{ctx: b.Context(), input: input, root: input, budget: &evaluationBudget{limits: DefaultLimits()}})
			if err != nil {
				b.Fatal(err)
			}
			fields, err := plannedObjectFields(output.output)
			if err != nil || len(fields) != 2 {
				b.Fatal("projection changed", err)
			}
		}
	})
	for _, count := range []int{0, 1, 8, 32, 256} {
		items := make([]Value, count)
		expressions := make([]Expression, count)
		for index := range items {
			items[index] = StringValue("hello😀")
			expressions[index] = literalExpression("hello😀")
		}
		raw, err := EncodeValue(items)
		if err != nil {
			b.Fatal(err)
		}
		for _, operation := range []string{"length", "join"} {
			for _, constructed := range []bool{false, true} {
				source := read
				if constructed {
					source = Expression{Op: "array", Items: expressions}
				}
				b.Run(fmt.Sprintf("%s-%d/constructed-%t", operation, count, constructed), func(b *testing.B) {
					benchmarkScopedConsumer(b, Expression{Op: operation, Source: &source}, raw)
				})
			}
		}
	}
	body := objectExpression(map[string]Expression{"a": readExpression("input", "/a"), "b": readExpression("input", "/b")})
	b.Run("same-input-scope", func(b *testing.B) {
		benchmarkScopedConsumer(b, Expression{Op: "with_input", Source: &read, Body: &body}, input)
	})
	when := Expression{Op: "all", Items: []Expression{
		{Op: "equal", Items: []Expression{readExpression("item", "/a"), literalExpression("one")}},
		{Op: "equal", Items: []Expression{readExpression("item", "/b"), literalExpression("two")}},
	}}
	b.Run("filter-item", func(b *testing.B) {
		benchmarkScopedConsumer(b, Expression{Op: "filter", Source: expressionPointer(readExpression("input", "/items")), When: &when}, input)
	})
}

func benchmarkScopedConsumer(b *testing.B, expression Expression, input Value) {
	b.Helper()
	plan, ok := compileOutputPlan(compileTestOutputExpression(b, expression))
	if !ok {
		b.Fatal("missing output plan")
	}
	b.ReportAllocs()
	for b.Loop() {
		view := newInputView(input)
		output, err := plan.evaluate(evaluation{ctx: b.Context(), input: input, root: input, inputView: view, objectReader: view.objectReader(), valueReader: &view.owned, budget: &evaluationBudget{limits: DefaultLimits()}})
		if err != nil || output.IsZero() {
			b.Fatal("consumer failed", err)
		}
	}
}
