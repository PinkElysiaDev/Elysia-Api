package protocol

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestPlanIndexPoliciesAndRawKeys(t *testing.T) {
	for _, tc := range []struct{ policy, want string }{{"last", "second"}, {"first", "first"}} {
		body := indexTestSequence(indexTestRemember(literalExpression("k"), literalExpression("first")), indexTestSequence(indexTestRemember(literalExpression("k"), literalExpression("second")), indexTestRecall(literalExpression("k"), "reject")))
		value, err := numericTestPlan(t, indexTestScope(tc.policy, body)).evaluate(numericTestState(t, Value{}))
		if err != nil || !value.IsString(tc.want) {
			t.Fatal("duplicate policy changed", tc.policy, value, err)
		}
	}
	retained := Expression{Op: "array", Items: []Expression{indexTestRemember(literalExpression("k"), literalExpression("first")), indexTestRemember(literalExpression("k"), literalExpression("second")), indexTestRecall(literalExpression("k"), "reject")}}
	current, err := numericTestPlan(t, indexTestScope("first", retained)).evaluate(numericTestState(t, Value{}))
	if err != nil || current.raw != `["first","second","first"]` {
		t.Fatal("remember must return this selected value even when the index keeps its first entry", current, err)
	}
	key, value := readExpression("item", "/key"), readExpression("item", "/value")
	body := Expression{Op: "choose", Source: expressionPointer(readExpression("item", "/action")), Cases: map[string]Expression{
		"put": indexTestRemember(key, value), "get": indexTestRecall(key, "omit"), "require": indexTestRecall(key, "reject"),
	}}
	plan := numericTestPlan(t, indexTestScope("last", Expression{Op: "map", Source: expressionPointer(readExpression("input", "")), Body: &body}))
	for _, tc := range []struct{ raw, want string }{
		{`[{"action":"put","key":"<","value":"literal"},{"action":"get","key":"\u003c"},{"action":"get","key":"<"}]`, `["literal","literal"]`},
		{`[{"action":"put","key":1,"value":"integer"},{"action":"get","key":1.0},{"action":"get","key":1}]`, `["integer","integer"]`},
		{`[{"action":"put","value":"missing"},{"action":"put","key":null,"value":"null"},{"action":"get"},{"action":"get","key":null}]`, `["missing","null","missing","null"]`},
		{`[{"action":"put","key":"a","value":"old"},{"action":"put","key":"a"},{"action":"require","key":"a"},{"action":"put","key":"b","value":null},{"action":"require","key":"b"}]`, `["old",null,null]`},
	} {
		actual, err := plan.evaluate(numericTestState(t, numericTestInput(t, tc.raw)))
		if err != nil || actual.raw != tc.want {
			t.Fatal("raw index changed spelling, missing or null", actual, err, tc.want)
		}
	}
	selected := readExpression("input", "/value")
	resolve := Expression{Op: "if", When: expressionPointer(Expression{Op: "exists", Source: &selected}), Then: &selected, Otherwise: expressionPointer(indexTestRecall(literalExpression("k"), "reject"))}
	plan = numericTestPlan(t, indexTestScope("last", indexTestSequence(indexTestRemember(literalExpression("k"), literalExpression("remembered")), resolve)))
	actual, err := plan.evaluate(numericTestState(t, numericTestInput(t, `{"value":null}`)))
	if err != nil || !actual.IsNull() {
		t.Fatal("explicit presence condition replaced an existing null", actual, err)
	}
}

func TestPlanIndexPreservesPreorderAndTags(t *testing.T) {
	key := readExpression("item", "/key")
	resolve := Expression{Op: "choose", Source: expressionPointer(readExpression("item", "/action")), Cases: map[string]Expression{
		"put": indexTestRemember(key, readExpression("item", "/value")), "get": indexTestRecall(key, "omit"),
	}}
	body := Expression{Op: "let", Name: "resolved", Source: &resolve, Body: expressionPointer(objectExpression(map[string]Expression{
		"value":    bindingTestRead("resolved", ""),
		"children": {Op: "if", When: expressionPointer(Expression{Op: "exists", Source: expressionPointer(readExpression("item", "/children"))}), Then: expressionPointer(Expression{Op: "recur", Source: expressionPointer(readExpression("item", "/children"))})},
	}))}
	plan := numericTestPlan(t, indexTestScope("last", Expression{Op: "walk", Source: expressionPointer(readExpression("input", "")), Body: &body}))
	input := numericTestInput(t, `[{"action":"put","key":"k","value":"old","arguments":[{"action":"put","key":"k","value":"poison"}]},{"action":"get","key":"k","children":[{"action":"put","key":"k","value":"new"}]},{"action":"get","key":"k"}]`)
	actual, err := plan.evaluate(numericTestState(t, input))
	if err != nil || actual.raw != `[{"value":"old"},{"children":[{"value":"new"}],"value":"old"},{"value":"new"}]` {
		t.Fatal("lookup moved after children or visited undeclared arrays", actual, err)
	}
	output := JSONObject(map[string]JSONOutput{"value": JSONLeaf(StringValue("immutable"))})
	output.node.origin = capturedNativeOrigin
	plan = numericTestPlan(t, indexTestScope("last", indexTestSequence(indexTestRemember(literalExpression("k"), readExpression("input", "")), indexTestRecall(literalExpression("k"), "reject"))))
	state := numericTestState(t, Value{})
	state.inputReader = func([]string) (JSONOutput, error) { return output, nil }
	actualPlan, err := plan.build(state)
	if err != nil || actualPlan.output.node != output.node || actualPlan.output.node.origin != capturedNativeOrigin {
		t.Fatal("index encoded, copied or stripped the immutable tagged value", err)
	}
}

func TestPlanIndexBoundsAndCleanup(t *testing.T) {
	body := indexTestSequence(indexTestRemember(literalExpression("a"), literalExpression("abcd")), indexTestRemember(literalExpression("b"), literalExpression("abcd")))
	plan := numericTestPlan(t, indexTestScope("last", body))
	for _, tc := range []struct {
		metric          string
		limit, observed int
	}{{"nodes", 1, 2}, {"bytes", 12, 18}} {
		state := numericTestState(t, Value{})
		state.indexes = &indexEvaluationState{frames: make([]*outputIndex, plan.indexSlots)}
		state.budget.stepLimit = 100
		if tc.metric == "nodes" {
			state.budget.limits.Nodes = tc.limit
		} else {
			state.budget.limits.BufferBytes = tc.limit
		}
		_, err := plan.evaluate(state)
		assertWalkLimit(t, err, tc.metric, tc.limit, tc.observed)
		if state.indexes.entries != 0 || state.indexes.bytes != 0 || state.indexes.frames[0] != nil {
			t.Fatal("failed index scope retained state", state.indexes)
		}
	}
	state := numericTestState(t, Value{})
	state.budget.limits.StateItems = 1
	if _, err := plan.evaluate(state); err != nil {
		t.Fatal("temporary index introduced a StateItems admission limit", err)
	}
	body = indexTestSequence(indexTestRemember(literalExpression("a"), literalExpression("large-data")), indexTestSequence(indexTestRemember(literalExpression("a"), literalExpression("x")), indexTestRemember(literalExpression("b"), literalExpression("large-data"))))
	plan = numericTestPlan(t, indexTestScope("last", body))
	state = numericTestState(t, Value{})
	state.budget.limits.BufferBytes = 21
	if _, err := plan.evaluate(state); err != nil {
		t.Fatal("replacement retained the previous logical value budget", err)
	}
	plan = numericTestPlan(t, indexTestScope("last", indexTestSequence(indexTestRemember(literalExpression("a"), literalExpression("value")), indexTestRecall(literalExpression("a"), "reject"))))
	state = numericTestState(t, Value{})
	state.indexes = &indexEvaluationState{frames: make([]*outputIndex, plan.indexSlots)}
	state.ctx = &outputPlanCancelContext{Context: t.Context(), remaining: 6}
	_, err := plan.evaluate(state)
	if !errors.Is(err, context.Canceled) || state.indexes.entries != 0 || state.indexes.bytes != 0 || state.indexes.frames[0] != nil {
		t.Fatal("cancelled index retained entries", err, state.indexes)
	}
}

func TestPlanIndexScopeAndErrors(t *testing.T) {
	key := literalExpression("private-key")
	for _, tc := range []struct {
		expression Expression
		message    string
	}{
		{indexTestScope("reject", indexTestSequence(indexTestRemember(key, literalExpression(1)), indexTestRemember(key, literalExpression(2)))), "duplicate index key"},
		{indexTestScope("last", indexTestRecall(key, "reject")), "no recorded key"},
		{indexTestScope("last", indexTestRemember(Expression{Op: "object"}, literalExpression(1))), "constructed containers are not encoded"},
	} {
		_, err := numericTestPlan(t, tc.expression).evaluate(numericTestState(t, Value{}))
		if err == nil || !strings.Contains(err.Error(), tc.message) || strings.Contains(err.Error(), "private-key") {
			t.Fatal("index failure changed or exposed its key", err)
		}
	}
	missing := Expression{Op: "read", Path: "/missing", Required: true}
	valueFirst := indexTestScope("first", indexTestSequence(indexTestRemember(key, literalExpression(1)), indexTestRemember(key, missing)))
	_, err := numericTestPlan(t, valueFirst).evaluate(numericTestState(t, Value{}))
	if err == nil || !strings.Contains(err.Error(), "required field is absent") {
		t.Fatal("duplicate policy bypassed value evaluation", err)
	}
	inner := indexTestScope("last", indexTestSequence(indexTestRemember(key, literalExpression("inner")), indexTestRecall(key, "reject")))
	body := indexTestSequence(indexTestRemember(key, literalExpression("outer")), Expression{Op: "array", Items: []Expression{inner, indexTestRecall(key, "reject")}})
	plan := numericTestPlan(t, indexTestScope("last", body))
	value, err := plan.evaluate(numericTestState(t, Value{}))
	if err != nil || value.raw != `["inner","outer"]` || plan.indexSlots != 2 {
		t.Fatal("index shadowing did not restore its lexical parent", value, err)
	}
	for _, expression := range []Expression{
		indexTestRecall(key, "omit"), indexTestRemember(key, literalExpression(1)),
		{Op: "with_index", Name: "records", KeyMode: "decoded", Policy: "last", Body: expressionPointer(literalExpression(1))},
		indexTestScope("implicit", literalExpression(1)), indexTestScope("last", indexTestRecall(key, "implicit")),
	} {
		compiler := expressionCompiler{limits: DefaultLimits()}
		if _, err := compiler.compilePlan(expression, "/index", expressionScope{}, 1); err == nil {
			t.Fatal("invalid index scope or policy compiled", expression)
		}
	}
}

func indexTestScope(policy string, body Expression) Expression {
	return Expression{Op: "with_index", Name: "records", KeyMode: "raw_json", Policy: policy, Body: &body}
}

func indexTestRemember(key, value Expression) Expression {
	return Expression{Op: "remember", Name: "records", Source: &key, Body: &value}
}

func indexTestRecall(key Expression, policy string) Expression {
	return Expression{Op: "recall", Name: "records", Source: &key, Policy: policy}
}

func indexTestSequence(first, body Expression) Expression {
	return Expression{Op: "let", Name: "recorded", Source: &first, Body: &body}
}
