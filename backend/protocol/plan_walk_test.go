package protocol

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestPlanWalkContentTree(t *testing.T) {
	const levels = 12
	input := `{"type":"text","text":"leaf"}`
	want := `{"index":0,"kind":"text","payload":"leaf","root":"stable"}`
	for level := range levels {
		id := fmt.Sprintf("call-%d", level)
		input = `{"type":"tool_result","tool_use_id":"` + id + `","content":[` + input + `],"arguments":{"content":[{"type":"must-stay-opaque"}]}}`
		want = `{"arguments":{"content":[{"type":"must-stay-opaque"}]},"children":[` + want + `],"id":"` + id + `","index":0,"kind":"tool_result","root":"stable"}`
	}
	recur := Expression{Op: "recur", Source: expressionPointer(bindingTestRead("node", "/content"))}
	body := Expression{Op: "choose", Source: expressionPointer(bindingTestRead("node", "/type")), Cases: map[string]Expression{
		"text": objectExpression(map[string]Expression{
			"kind": literalExpression("text"), "payload": bindingTestRead("node", "/text"),
			"index": {Op: "index"}, "root": readExpression("root", "/label"),
		}),
		"tool_result": objectExpression(map[string]Expression{
			"kind": literalExpression("tool_result"), "id": bindingTestRead("node", "/tool_use_id"),
			"children": recur, "arguments": bindingTestRead("node", "/arguments"),
			"index": {Op: "index"}, "root": readExpression("input", "/label"),
		}),
	}}
	plan := numericTestPlan(t, Expression{Op: "walk", Source: expressionPointer(readExpression("input", "/content")), Body: expressionPointer(Expression{Op: "let", Name: "node", Source: expressionPointer(readExpression("item", "")), Body: &body})})
	state := numericTestState(t, numericTestInput(t, `{"label":"stable","content":[`+input+`]}`))
	value, err := plan.evaluate(state)
	if err != nil || value.raw != "["+want+"]" || state.budget.walkNodes != levels+1 {
		t.Fatal("explicit content walk changed order, bindings or opaque payloads", value, err, state.budget.walkNodes)
	}
	if plan.bindingSlots != 1 {
		t.Fatal("runtime recursion must reuse the compiled lexical slots", plan.bindingSlots)
	}
}

func TestPlanWalkUsesNearestBody(t *testing.T) {
	item := readExpression("item", "")
	innerBody := Expression{Op: "if", When: expressionPointer(Expression{Op: "equal", Items: []Expression{{Op: "type", Source: &item}, literalExpression("array")}}),
		Then: expressionPointer(Expression{Op: "recur", Source: &item}), Otherwise: expressionPointer(objectExpression(map[string]Expression{"value": item, "index": {Op: "index"}}))}
	outerBody := Expression{Op: "walk", Source: &item, Body: &innerBody}
	plan := numericTestPlan(t, Expression{Op: "walk", Source: expressionPointer(readExpression("input", "")), Body: &outerBody})
	value, err := plan.evaluate(numericTestState(t, numericTestInput(t, `[[[1,2],[3]]]`)))
	if err != nil || value.raw != `[[[{"index":0,"value":1},{"index":1,"value":2}],[{"index":0,"value":3}]]]` {
		t.Fatal("recur selected the wrong walk body or inherited an outer index", value, err)
	}
}

func TestPlanWalkResourceBoundaries(t *testing.T) {
	read := readExpression("root", "")
	plan := numericTestPlan(t, Expression{Op: "walk", Source: &read, Body: expressionPointer(Expression{Op: "recur", Source: &read})})
	state := numericTestState(t, numericTestInput(t, `[1]`))
	state.budget.limits.Depth = 3
	_, err := plan.evaluate(state)
	assertWalkLimit(t, err, "depth", 3, 4)
	state = numericTestState(t, numericTestInput(t, `[1]`))
	state.budget.stepLimit = 3
	_, err = plan.evaluate(state)
	assertWalkLimit(t, err, "steps", 3, 4)
	state = numericTestState(t, numericTestInput(t, `[1]`))
	state.ctx = &outputPlanCancelContext{Context: t.Context(), remaining: 5}
	_, err = plan.evaluate(state)
	if !errors.Is(err, context.Canceled) {
		t.Fatal("recursive body ignored cancellation", err)
	}
	plan = numericTestPlan(t, Expression{Op: "walk", Source: &read, Body: expressionPointer(Expression{Op: "omit"})})
	state = numericTestState(t, numericTestInput(t, `[1,2,3]`))
	state.budget.limits.Nodes, state.budget.stepLimit = 2, 100
	_, err = plan.evaluate(state)
	assertWalkLimit(t, err, "nodes", 2, 3)
	plan = numericTestPlan(t, Expression{Op: "walk", Source: &read, Body: expressionPointer(Expression{Op: "array", Items: []Expression{literalExpression("xxxx"), literalExpression("xxxx")}})})
	state = numericTestState(t, numericTestInput(t, `[1]`))
	state.budget.limits.BufferBytes = 8
	_, err = plan.evaluate(state)
	var limit *ResourceLimitError
	if !errors.As(err, &limit) || limit.Metric != "bytes" {
		t.Fatal("recursive output bypassed intermediate bytes", err)
	}
}

func TestPlanWalkSourceAndScope(t *testing.T) {
	read := readExpression("input", "")
	for _, expression := range []Expression{
		{Op: "recur", Source: &read},
		{Op: "walk", Source: expressionPointer(Expression{Op: "recur", Source: &read}), Body: expressionPointer(Expression{Op: "omit"})},
		{Op: "walk", Source: &read, Body: expressionPointer(Expression{Op: "recur", Source: expressionPointer(literalExpression(1))})},
	} {
		compiler := expressionCompiler{limits: DefaultLimits()}
		if _, err := compiler.compilePlan(expression, "/walk", expressionScope{}, 1); err == nil {
			t.Fatal("invalid recur scope or source compiled", expression)
		}
	}
	compiler := expressionCompiler{limits: DefaultLimits(), references: map[string]Expression{"cycle": {Op: "ref", Ref: "cycle"}}, resolving: map[string]bool{}, used: map[string]bool{}}
	_, err := compiler.compilePlan(Expression{Op: "walk", Source: &read, Body: expressionPointer(Expression{Op: "ref", Ref: "cycle"})}, "/walk", expressionScope{}, 1)
	if err == nil || !strings.Contains(err.Error(), "recursive expression") {
		t.Fatal("walk permitted an infinite reference expansion", err)
	}
	plan := numericTestPlan(t, Expression{Op: "walk", Source: &read, Body: expressionPointer(readExpression("item", ""))})
	for _, raw := range []string{"", "null", "1", "{}"} {
		_, err := plan.evaluate(numericTestState(t, numericTestInput(t, raw)))
		if err == nil || !strings.Contains(err.Error(), "expected an array") {
			t.Fatal("walk accepted a non-array source", raw, err)
		}
	}
	value, err := plan.evaluate(numericTestState(t, numericTestInput(t, `[]`)))
	if err != nil || value.raw != `[]` {
		t.Fatal("empty walk changed shape", value, err)
	}
}

func assertWalkLimit(t *testing.T, err error, metric string, maximum, observed int) {
	t.Helper()
	var limit *ResourceLimitError
	if !errors.As(err, &limit) || limit.Domain != "evaluation" || limit.Metric != metric || limit.Limit != maximum || limit.Observed != observed {
		t.Fatal("unexpected walk resource failure", err)
	}
}
