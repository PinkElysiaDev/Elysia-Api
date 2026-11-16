package protocol

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestNamedConstructorsKeepNestedBindingSlots(t *testing.T) {
	compiler := newExpressionCompiler(DefaultLimits(), map[string]Expression{
		"nested": {Op: "choose", Source: expressionPointer(literalExpression("selected")), Cases: map[string]Expression{
			"selected": {Op: "array", Items: []Expression{objectExpression(map[string]Expression{
				"value": {Op: "read", From: "binding", Name: "value"},
			})}},
		}},
	})
	var previous *compiledExpression
	for slot := range 2 {
		scope := expressionScope{bindingDepth: 2, bindings: map[string]expressionBinding{
			"value": {slot: slot, result: IntegerType},
		}}
		program, err := compiler.compile(Expression{Op: "ref", Ref: "nested"}, "/mapping/program", scope, 1)
		if err != nil {
			t.Fatal(err)
		}
		if program == previous {
			t.Fatal("nested binding reused another scope's slot")
		}
		previous = program
		plan, ok := compileOutputPlan(program)
		if !ok || plan.bindingSlots != slot+1 {
			t.Fatal("nested binding lost its required slot count")
		}
		bindings := []JSONOutput{JSONLeaf(coreInteger(10)), JSONLeaf(coreInteger(20))}
		output, err := plan.evaluate(evaluation{ctx: t.Context(), bindings: bindings, budget: &evaluationBudget{limits: DefaultLimits()}})
		expected := []string{`[{"value":10}]`, `[{"value":20}]`}
		if err != nil || output.JSONString() != expected[slot] {
			t.Fatal("nested reference read the wrong binding", output.JSONString(), err)
		}
	}
	if _, err := compiler.compile(Expression{Op: "ref", Ref: "nested"}, "/mapping/unbound", expressionScope{}, 1); err == nil {
		t.Fatal("cached constructor granted access to an undeclared binding")
	}
}

func TestNamedPureProgramsKeepSchemaProofs(t *testing.T) {
	for _, from := range []string{"input", "root", "item"} {
		t.Run(from, func(t *testing.T) {
			compiler := newExpressionCompiler(DefaultLimits(), map[string]Expression{"field": readExpression(from, "/value")})
			valid := &ValueSchema{Type: ObjectType, Properties: map[string]ValueSchema{"value": {Type: IntegerType}}}
			invalid := &ValueSchema{Type: StringType}
			for _, schema := range []*ValueSchema{valid, invalid} {
				scope := expressionScope{isIteration: true, hasInputScope: true}
				switch from {
				case "input":
					scope.input = schema
				case "root":
					scope.root = schema
				case "item":
					scope.item = schema
				}
				_, err := compiler.compile(Expression{Op: "ref", Ref: "field"}, "/mapping/program", scope, 1)
				if schema == valid && err != nil {
					t.Fatal(err)
				}
				if schema == invalid && err == nil {
					t.Fatal("cached field read bypassed an incompatible schema")
				}
			}
		})
	}
}

func TestNamedPureProgramsChargeEveryExecution(t *testing.T) {
	compiler := newExpressionCompiler(DefaultLimits(), map[string]Expression{"value": literalExpression(1)})
	call := Expression{Op: "ref", Ref: "value"}
	program, err := compiler.compile(objectExpression(map[string]Expression{"a": call, "b": call, "c": call}), "/mapping/program", expressionScope{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if program.fields["a"] != program.fields["b"] || program.fields["b"] != program.fields["c"] {
		t.Fatal("fixture did not reuse the same instruction")
	}
	plan, ok := compileOutputPlan(program)
	if !ok {
		t.Fatal("shared program has no plan")
	}
	budget := &evaluationBudget{limits: DefaultLimits(), stepLimit: 3}
	_, err = plan.build(evaluation{ctx: t.Context(), budget: budget})
	var failure *ResourceLimitError
	if !errors.As(err, &failure) || failure.Metric != "steps" || failure.Observed != 4 || budget.steps != 4 {
		t.Fatal("instruction sharing discounted an executed reference", budget.steps, err)
	}
	budget = &evaluationBudget{limits: DefaultLimits(), stepLimit: 4}
	if _, err := plan.build(evaluation{ctx: t.Context(), budget: budget}); err != nil || budget.steps != 4 {
		t.Fatal("exact execution budget changed", budget.steps, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	budget = &evaluationBudget{limits: DefaultLimits()}
	if _, err := plan.build(evaluation{ctx: ctx, budget: budget}); !errors.Is(err, context.Canceled) || budget.steps != 1 {
		t.Fatal("shared plan bypassed cancellation", budget.steps, err)
	}
}

func TestNamedPureProgramsShareAcrossUnrelatedScopes(t *testing.T) {
	compiler := newExpressionCompiler(DefaultLimits(), map[string]Expression{
		"outer": {Op: "ref", Ref: "field"},
		"field": readExpression("input", "/value"),
	})
	bind := func(value int) Expression {
		return Expression{Op: "let", Name: "unrelated", Source: expressionPointer(literalExpression(value)),
			Body: &Expression{Op: "ref", Ref: "outer"}}
	}
	program, err := compiler.compile(objectExpression(map[string]Expression{
		"plain":  {Op: "ref", Ref: "outer"},
		"bound":  bind(10),
		"nested": {Op: "let", Name: "another", Source: expressionPointer(literalExpression(20)), Body: expressionPointer(bind(30))},
	}), "/mapping/program", expressionScope{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if program.fields["plain"] != program.fields["bound"].body || program.fields["plain"] != program.fields["nested"].body.body {
		t.Fatal("unrelated lexical bindings duplicated a pure named program")
	}
	plan, ok := compileOutputPlan(program)
	if !ok {
		t.Fatal("pure program has no execution plan")
	}
	for _, input := range []string{`{"value":1}`, `{"value":9007199254740993}`} {
		value, err := ParseValue([]byte(input))
		if err != nil {
			t.Fatal(err)
		}
		output, err := plan.evaluate(evaluation{ctx: t.Context(), input: value, budget: &evaluationBudget{limits: DefaultLimits()}})
		if err != nil {
			t.Fatal(err)
		}
		fields, err := output.ReadObject()
		if err != nil {
			t.Fatal(err)
		}
		original, _ := value.ReadObject()
		for _, name := range []string{"plain", "bound", "nested"} {
			if fields[name] != original["value"] {
				t.Fatal("shared program changed a per-call value", name, input, output)
			}
		}
	}
	compiler.mappingReferences = make(map[string]Expression)
	if _, err := compiler.compile(Expression{Op: "ref", Ref: "outer"}, "/mapping/second", expressionScope{bindingDepth: 7}, 1); err != nil {
		t.Fatal(err)
	}
	if len(compiler.mappingReferences) != 2 {
		t.Fatal("scope-independent reuse lost transitive dependencies")
	}
}

func TestNamedPureProgramsKeepTypedAndIterationContexts(t *testing.T) {
	compiler := newExpressionCompiler(DefaultLimits(), map[string]Expression{
		"field": readExpression("input", "/model"),
		"item":  readExpression("item", ""),
	})
	call := Expression{Op: "ref", Ref: "field"}
	raw, err := compiler.compile(call, "/mapping/raw", expressionScope{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	typed, err := compiler.compile(call, "/mapping/core", expressionScope{coreInput: reflect.TypeFor[Request]()}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if raw == typed {
		t.Fatal("raw and typed core reads shared incompatible access instructions")
	}
	item := Expression{Op: "ref", Ref: "item"}
	if _, err := compiler.compile(item, "/mapping/iteration", expressionScope{isIteration: true}, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := compiler.compile(item, "/mapping/unbound", expressionScope{}, 1); err == nil {
		t.Fatal("cached item read escaped its iteration context")
	}
}

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

func TestNamedProgramPathsKeepContextErrorsAfterRelease(t *testing.T) {
	const reference = "selected/value~raw"
	const expectedPath = "/mapping/expressions/selected~1value~0raw/source"
	compiler := newExpressionCompiler(DefaultLimits(), map[string]Expression{
		reference: {Op: "present", Source: &Expression{Op: "read", From: "binding", Name: "selected", Path: "/missing", Required: true}},
	})
	plans := make([]*compiledOutputPlan, 2)
	for slot := range plans {
		scope := expressionScope{bindingDepth: 2, bindings: map[string]expressionBinding{
			"selected": {slot: slot, result: ObjectType},
		}}
		plan, err := compiler.compilePlan(Expression{Op: "ref", Ref: reference}, "/mapping/program", scope, 1)
		if err != nil {
			t.Fatal(err)
		}
		plans[slot] = plan
	}
	for _, shouldRelease := range []bool{false, true} {
		if shouldRelease {
			compiler.releaseGraph()
		}
		for slot, plan := range plans {
			bindings := []JSONOutput{JSONLeaf(Value{raw: `{"missing":null}`}), JSONLeaf(Value{raw: `{"missing":null}`})}
			bindings[slot] = JSONLeaf(Value{raw: `{}`})
			state := evaluation{ctx: t.Context(), bindings: bindings, budget: &evaluationBudget{limits: DefaultLimits()}}
			_, err := plan.evaluate(state)
			if err == nil || mappingErrorPath("", err) != expectedPath || err.Error() != expectedPath+": required field is absent" {
				t.Fatal("named source lost its context or escaped diagnostic location", slot, shouldRelease, err)
			}
			bindings[slot] = JSONLeaf(Value{raw: `{"missing":1}`})
			state.budget = &evaluationBudget{limits: DefaultLimits()}
			value, err := plan.evaluate(state)
			if err != nil || value.JSONString() != "true" {
				t.Fatal("named source read another context after failure", slot, shouldRelease, value, err)
			}
		}
	}
}
