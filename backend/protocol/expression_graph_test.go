package protocol

import (
	"fmt"
	"testing"
)

func TestReleasedExpressionGraphPreservesExecution(t *testing.T) {
	read := Expression{Op: "read", Path: "/value", Required: true}
	compiler := newExpressionCompiler(DefaultLimits(), map[string]Expression{"value": read})
	plan, err := compiler.compilePlan(objectExpression(map[string]Expression{
		"first": {Op: "ref", Ref: "value"}, "second": {Op: "ref", Ref: "value"},
	}), "/program", expressionScope{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	input := Value{raw: `{"value":9007199254740993}`}
	run := func(value Value) (Value, error) {
		return plan.evaluate(evaluation{ctx: t.Context(), input: value, budget: &evaluationBudget{limits: DefaultLimits()}})
	}
	want, wantErr := run(input)
	_, missing := run(Value{raw: `{}`})
	if wantErr != nil || missing == nil {
		t.Fatal("invalid execution fixture", wantErr, missing)
	}
	compiledExpressions := compiler.compiledExpressions
	compiler.releaseGraph()
	for _, expression := range compiledExpressions {
		if expression.expressionCompileFacts != nil {
			t.Fatal("execution retained its compilation graph")
		}
	}
	got, err := run(input)
	_, gotMissing := run(Value{raw: `{}`})
	if got != want || err != nil || fmt.Sprint(gotMissing) != fmt.Sprint(missing) {
		t.Fatal("graph release changed output or diagnostic location", got, err, gotMissing)
	}
}

func TestReleasedObjectPlanRetainsProjectKeys(t *testing.T) {
	for _, fixture := range []struct {
		policy string
		keys   []string
	}{
		{"include", []string{"z", "missing", "a"}},
		{"exclude", []string{"required", "drop"}},
	} {
		t.Run(fixture.policy, func(t *testing.T) {
			compiler := newExpressionCompiler(DefaultLimits(), nil)
			plan, err := compiler.compilePlan(objectExpression(map[string]Expression{
				"tail": {Op: "read", Path: "/required", Required: true},
				"selected": {Op: "project", Source: expressionPointer(readExpression("input", "")),
					Keys: fixture.keys, Policy: fixture.policy},
			}), "/program", expressionScope{}, 1)
			if err != nil {
				t.Fatal(err)
			}
			input := parseTestValue(t, `{"z":9007199254740993,"drop":false,"a":null,"required":"ok"}`)
			run := func(value Value) (Value, error) {
				return plan.evaluate(evaluation{ctx: t.Context(), input: value, budget: &evaluationBudget{limits: DefaultLimits()}})
			}
			want, err := run(input)
			_, missing := run(parseTestValue(t, `{"a":null,"z":9007199254740993}`))
			if err != nil || want.JSONString() != `{"selected":{"a":null,"z":9007199254740993},"tail":"ok"}` || missing == nil {
				t.Fatal("invalid object/project release fixture", want, err, missing)
			}
			compiledExpressions := compiler.compiledExpressions
			compiler.releaseGraph()
			for _, expression := range compiledExpressions {
				if expression.expressionCompileFacts != nil {
					t.Fatal("object/project plan retained compilation facts")
				}
			}
			got, err := run(input)
			_, gotMissing := run(parseTestValue(t, `{"a":null,"z":9007199254740993}`))
			if got != want || err != nil || fmt.Sprint(gotMissing) != fmt.Sprint(missing) || mappingErrorPath("", gotMissing) != "/program/fields/tail" {
				t.Fatal("release changed object order, project keys or error location", got, err, gotMissing)
			}
		})
	}
}

func TestPackageReleasesExpressionGraphsAfterProofs(t *testing.T) {
	compiled := compilePackageForTest(t, textProtocolPackage(t))
	if compiled.compileExpressions != nil {
		t.Fatal("published package retained its compiler workspace")
	}
	var visit func(*compiledOutputPlan)
	seen := make(map[*compiledOutputPlan]bool)
	visit = func(plan *compiledOutputPlan) {
		if plan == nil || seen[plan] {
			return
		}
		seen[plan] = true
		if plan.expression.expressionCompileFacts != nil {
			t.Fatal("published plan retained inferred types or graph edges")
		}
		for _, child := range []*compiledOutputPlan{plan.source, plan.body, plan.when, plan.then, plan.otherwise} {
			visit(child)
		}
		for _, child := range plan.items {
			visit(child)
		}
		fields, cases := plan.readCollections()
		for _, child := range cases {
			visit(child)
		}
		for _, field := range fields {
			visit(field.expression)
		}
	}
	for _, mapping := range compiled.mappings {
		visit(mapping.outputPlan)
	}
}
