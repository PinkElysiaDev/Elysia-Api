package protocol

import "testing"

func TestJSONDigestKeepsMissingAndExactValues(t *testing.T) {
	expression := compileTestOutputExpression(t, Expression{Op: "json_digest", Source: expressionPointer(readExpression("input", "/value"))})
	plan, ok := compileOutputPlan(expression)
	if !ok {
		t.Fatal("digest has no bound output plan")
	}
	digest := func(raw string) Value {
		t.Helper()
		input, err := ParseValue([]byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		value, err := plan.evaluate(evaluation{ctx: t.Context(), input: input, root: input, budget: &evaluationBudget{limits: DefaultLimits(), stepLimit: DefaultLimits().Nodes}})
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	if !digest(`{}`).IsZero() || digest(`{"value":null}`).IsZero() {
		t.Fatal("digest collapsed missing and null")
	}
	first := digest(`{"value":{"number":9007199254740993,"text":"u"}}`)
	if first != digest(`{"value":{"text":"\u0075","number":9007199254740993.0}}`) || first == digest(`{"value":{"number":9007199254740992,"text":"u"}}`) {
		t.Fatal("digest changed exact JSON value equivalence")
	}
}
