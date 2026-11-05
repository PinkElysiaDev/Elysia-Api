package protocol

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestPlanBindingEvaluatesSourceOnce(t *testing.T) {
	read := bindingTestRead("saved", "/value")
	plan := numericTestPlan(t, Expression{Op: "let", Name: "saved", Source: expressionPointer(readExpression("input", "")), Body: expressionPointer(objectExpression(map[string]Expression{"first": read, "second": read}))})
	calls := 0
	state := numericTestState(t, Value{})
	state.inputReader = func(pointer []string) (JSONOutput, error) {
		calls++
		if len(pointer) != 0 {
			t.Fatal("binding read returned to the source reader", pointer)
		}
		return JSONObject(map[string]JSONOutput{"value": JSONLeaf(StringValue("owned"))}), nil
	}
	value, err := plan.evaluate(state)
	if err != nil || value.raw != `{"first":"owned","second":"owned"}` || calls != 1 || state.budget.steps != 5 {
		t.Fatal("binding repeated its source or changed evaluation steps", value, err, calls, state.budget.steps)
	}
}

func TestPlanBindingShadowingAndSlotReuse(t *testing.T) {
	read := bindingTestRead("value", "")
	inner := Expression{Op: "let", Name: "value", Source: expressionPointer(Expression{Op: "join", Items: []Expression{read, literalExpression("-inner")}}), Body: &read}
	body := Expression{Op: "array", Items: []Expression{read, inner, inner, read}}
	plan := numericTestPlan(t, Expression{Op: "let", Name: "value", Source: expressionPointer(readExpression("input", "")), Body: &body})
	if plan.bindingSlots != 2 {
		t.Fatal("binding storage must follow lexical depth, not total binding count", plan.bindingSlots)
	}
	for _, text := range []string{"first", "second"} {
		value, err := plan.evaluate(numericTestState(t, StringValue(text)))
		want, _ := EncodeValue([]string{text, text + "-inner", text + "-inner", text})
		if err != nil || value != want {
			t.Fatal("shadowing changed the outer binding or retained a previous execution", value, err)
		}
	}
}

func TestPlanIterationBindingsAndIndex(t *testing.T) {
	item := readExpression("item", "")
	index := Expression{Op: "index"}
	inner := Expression{Op: "map", Source: &item, Body: expressionPointer(objectExpression(map[string]Expression{
		"group": bindingTestRead("group-index", ""), "index": index,
		"parent": bindingTestRead("group", "/0"), "value": item,
	}))}
	bound := Expression{Op: "let", Name: "group-index", Source: &index, Body: expressionPointer(Expression{Op: "let", Name: "group", Source: &item, Body: &inner})}
	plan := numericTestPlan(t, Expression{Op: "map", Source: expressionPointer(readExpression("input", "")), Body: &bound})
	value, err := plan.evaluate(numericTestState(t, numericTestInput(t, `[["a","b"],["c"]]`)))
	if err != nil || value.raw != `[[{"group":0,"index":0,"parent":"a","value":"a"},{"group":0,"index":1,"parent":"a","value":"b"}],[{"group":1,"index":0,"parent":"c","value":"c"}]]` {
		t.Fatal("nested iteration lost its parent binding or index", value, err)
	}
	filtered := Expression{Op: "filter", Source: expressionPointer(readExpression("input", "")), When: expressionPointer(Expression{Op: "integer", Operation: "gte", Items: []Expression{index, literalExpression(1)}})}
	plan = numericTestPlan(t, Expression{Op: "map", Source: &filtered, Body: &index})
	value, err = plan.evaluate(numericTestState(t, numericTestInput(t, `["discarded","a","b"]`)))
	if err != nil || value.raw != `[0,1]` {
		t.Fatal("index must address the immediate filtered source, not claim an original wire position", value, err)
	}
	plan = numericTestPlan(t, Expression{Op: "flatmap", Source: expressionPointer(readExpression("input", "")), Body: expressionPointer(Expression{Op: "array", Items: []Expression{index, index}})})
	value, err = plan.evaluate(numericTestState(t, numericTestInput(t, `["a","b"]`)))
	if err != nil || value.raw != `[0,0,1,1]` {
		t.Fatal("flatmap index must identify each source element", value, err)
	}
}

func TestPlanBindingPresenceAndCleanup(t *testing.T) {
	read := bindingTestRead("value", "")
	plan := numericTestPlan(t, Expression{Op: "let", Name: "value", Source: expressionPointer(readExpression("input", "")), Body: &read})
	for _, raw := range []string{"", "null", ` { "n":1e+3 } `} {
		input := numericTestInput(t, raw)
		value, err := plan.evaluate(numericTestState(t, input))
		if err != nil || value != input {
			t.Fatal("binding changed presence or raw identity", raw, value, err)
		}
	}
	state := numericTestState(t, StringValue("long"))
	state.budget.limits.BufferBytes = 2
	_, err := plan.evaluate(state)
	var limit *ResourceLimitError
	if !errors.As(err, &limit) || mappingErrorPath("", err) != "/mapping/source" {
		t.Fatal("binding source bypassed its byte budget", err)
	}
	state = numericTestState(t, StringValue("retained"))
	state.bindings = make([]JSONOutput, plan.bindingSlots)
	state.ctx = &outputPlanCancelContext{Context: t.Context(), remaining: 3}
	_, err = plan.evaluate(state)
	if !errors.Is(err, context.Canceled) || !state.bindings[0].IsZero() || mappingErrorPath("", err) != "/mapping/body" {
		t.Fatal("binding cancellation changed boundaries or retained a frame value", err)
	}
	read.Required = true
	plan = numericTestPlan(t, Expression{Op: "let", Name: "value", Source: expressionPointer(Expression{Op: "omit"}), Body: &read})
	_, err = plan.evaluate(numericTestState(t, Value{}))
	if err == nil || !strings.Contains(err.Error(), "required field is absent") || mappingErrorPath("", err) != "/mapping/body" {
		t.Fatal("required binding silently accepted an absent value", err)
	}
}

func TestPlanBindingCompilation(t *testing.T) {
	read := bindingTestRead("value", "")
	for _, expression := range []Expression{
		read,
		{Op: "read", From: "input", Name: "value"},
		{Op: "let", Name: "bad name", Source: expressionPointer(literalExpression(1)), Body: &read},
		{Op: "let", Name: "value", Source: &read, Body: &read},
		{Op: "index"},
		{Op: "map", Source: expressionPointer(Expression{Op: "index"}), Body: expressionPointer(literalExpression(1))},
		{Op: "array", Items: []Expression{{Op: "let", Name: "value", Source: expressionPointer(literalExpression(1)), Body: &read}, read}},
	} {
		compiler := expressionCompiler{limits: DefaultLimits()}
		if _, err := compiler.compilePlan(expression, "/binding", expressionScope{}, 1); err == nil {
			t.Fatal("invalid binding or index scope compiled", expression)
		}
	}
}

func TestExpressionRootSchemaRemainsIndependent(t *testing.T) {
	root := &ValueSchema{Type: ObjectType, Properties: map[string]ValueSchema{
		"enabled": {Type: BooleanType},
		"rows":    {Type: ArrayType, Items: &ValueSchema{Type: ObjectType, Properties: map[string]ValueSchema{"name": {Type: StringType}}}},
	}}
	input := &ValueSchema{Type: ObjectType, Properties: map[string]ValueSchema{"current": {Type: StringType}}}
	compiler := expressionCompiler{limits: DefaultLimits()}
	plan, err := compiler.compilePlan(readExpression("root", "/enabled"), "/root", expressionScope{input: input, root: root}, 1)
	if err != nil || plan.expression.result != BooleanType {
		t.Fatal("root read used the selected input schema", err)
	}
	expression := Expression{Op: "map", Source: expressionPointer(readExpression("root", "/rows")), Body: expressionPointer(readExpression("item", "/name"))}
	plan, err = compiler.compilePlan(expression, "/root-array", expressionScope{input: input, root: root}, 1)
	if err != nil || plan.body.expression.result != StringType {
		t.Fatal("root iteration lost its source item schema", err)
	}
	read := bindingTestRead("rows", "")
	expression = Expression{Op: "let", Name: "rows", Source: expressionPointer(readExpression("root", "/rows")), Body: expressionPointer(Expression{Op: "map", Source: &read, Body: expressionPointer(readExpression("item", "/name"))})}
	plan, err = compiler.compilePlan(expression, "/bound-array", expressionScope{input: input, root: root}, 1)
	if err != nil || plan.body.body.expression.result != StringType {
		t.Fatal("binding lost its source schema", err)
	}
}

func bindingTestRead(name, path string) Expression {
	return Expression{Op: "read", From: "binding", Name: name, Path: path}
}
