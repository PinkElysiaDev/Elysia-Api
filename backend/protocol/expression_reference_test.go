package protocol

import (
	"strings"
	"testing"
)

func TestNamedProgramDepthIncludesCachedReferenceEdges(t *testing.T) {
	limits := DefaultLimits()
	limits.Depth = 4
	references := map[string]Expression{
		"outer": {Op: "ref", Ref: "inner"},
		"inner": literalExpression(1),
	}
	for _, isWarm := range []bool{false, true} {
		compiler := newExpressionCompiler(limits, references)
		call := Expression{Op: "ref", Ref: "outer"}
		if isWarm {
			if _, err := compiler.compile(call, "/mapping/program/a", expressionScope{}, 2); err != nil {
				t.Fatal("reference at the exact depth limit was rejected", err)
			}
		}
		if _, err := compiler.compile(call, "/mapping/program/b/x", expressionScope{}, 3); err == nil || !strings.Contains(err.Error(), "depth") {
			t.Fatalf("cached=%v: nested reference exceeded the depth limit without rejection: %v", isWarm, err)
		}
	}
}

func TestNamedProgramsShareInstructionsAndIsolateBindings(t *testing.T) {
	compiler := newExpressionCompiler(DefaultLimits(), map[string]Expression{
		"echo": {Op: "read", From: "binding", Name: "value"},
	})
	bind := func(value int) Expression {
		return Expression{Op: "let", Name: "value", Source: expressionPointer(literalExpression(value)),
			Body: &Expression{Op: "ref", Ref: "echo"}}
	}
	expression, err := compiler.compile(objectExpression(map[string]Expression{"a": bind(1), "b": bind(2),
		"nested": {Op: "let", Name: "outer", Source: expressionPointer(literalExpression(99)), Body: expressionPointer(bind(3))},
	}), "/mapping/program", expressionScope{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if expression.fields["a"].body != expression.fields["b"].body || expression.fields["a"].body == expression.fields["nested"].body.body {
		t.Fatal("named instructions were expanded or crossed incompatible lexical slots")
	}
	plan, ok := compileOutputPlan(expression)
	if !ok {
		t.Fatal("named program has no execution plan")
	}
	output, err := plan.evaluate(evaluation{ctx: t.Context(), budget: &evaluationBudget{limits: DefaultLimits()}})
	if err != nil || output.JSONString() != `{"a":1,"b":2,"nested":3}` {
		t.Fatal("shared instructions retained another call's value", output.JSONString(), err)
	}
}

func TestNamedProgramReusePreservesPermissionsAndDependencies(t *testing.T) {
	compiler := newExpressionCompiler(DefaultLimits(), map[string]Expression{
		"outer": {Op: "ref", Ref: "inner"},
		"inner": {Op: "read", From: "runtime", Path: "/scope/model"},
	})
	for range 2 {
		compiler.mappingReferences = make(map[string]Expression)
		if _, err := compiler.compilePlan(Expression{Op: "ref", Ref: "outer"}, "/mapping/program", expressionScope{canReadRuntime: true}, 1); err != nil {
			t.Fatal(err)
		}
		if len(compiler.mappingReferences) != 2 {
			t.Fatal("cached reference omitted a transitive revision dependency")
		}
	}
	if _, err := compiler.compilePlan(Expression{Op: "ref", Ref: "outer"}, "/spec/check", expressionScope{}, 1); err == nil {
		t.Fatal("a previous mapping compilation granted runtime access to a wire check")
	}
}

func TestNamedProgramNativePermissionIsIsolated(t *testing.T) {
	for _, fixture := range []struct {
		path  string
		scope expressionScope
	}{
		{"/clientOutput/includeUsage", expressionScope{isWireContract: true, canReadClientOutput: true}},
		{"/encoding/hasRendered", expressionScope{isWireContract: true, canReadEncodingState: true}},
	} {
		compiler := newExpressionCompiler(DefaultLimits(), map[string]Expression{
			"outer": {Op: "ref", Ref: "inner"},
			"inner": readExpression("runtime", fixture.path),
		})
		call := Expression{Op: "ref", Ref: "outer"}
		if _, err := compiler.compilePlan(call, "/mapping/nativeWhen", fixture.scope, 1); err != nil {
			t.Fatal(err)
		}
		if _, err := compiler.compilePlan(call, "/spec/check", expressionScope{isWireContract: true}, 1); err == nil {
			t.Fatal("cached native predicate granted runtime access to a wire check", fixture.path)
		}
	}
}
