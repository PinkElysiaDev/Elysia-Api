package protocol

import (
	"errors"
	"strings"
	"testing"
)

func TestProjectObjectFields(t *testing.T) {
	for _, test := range []struct {
		name, policy, input, expected string
		keys                          []string
	}{
		{"exclude", "exclude", `{"ttl":"1h","type":"ephemeral","future":{"n":9007199254740993}}`, `{"future":{"n":9007199254740993},"type":"ephemeral"}`, []string{"ttl"}},
		{"include", "include", `{"z":0,"a":null,"b":false}`, `{"a":null,"z":0}`, []string{"z", "missing", "a"}},
		{"duplicates", "exclude", `{"b":1e+03,"a":1,"a":2}`, `{"a":2,"b":1e+03}`, []string{"missing"}},
		{"exclude-empty", "exclude", `{"b":null,"a":false}`, `{"a":false,"b":null}`, []string{}},
		{"include-empty", "include", `{"a":null}`, `{}`, []string{}},
		{"missing-member", "include", `{}`, `{}`, []string{"a"}},
		{"literal-key", "include", `{"a/b":true,"a":{"b":false}}`, `{"a/b":true}`, []string{"a/b"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			expression := Expression{Op: "project", Source: expressionPointer(readExpression("input", "")), Keys: test.keys, Policy: test.policy}
			definition := testDefinition("projection", "m", "h", "t", "r")
			definition.Directions[EncodeRequest] = Mapping{Transform: &expression}
			compiled := compileTestDefinition(t, definition)
			input := parseTestValue(t, test.input)
			output, issues := compiled.Execute(t.Context(), EncodeRequest, input, EvaluationContext{})
			if err := IssuesError(issues); err != nil || output.raw != test.expected {
				t.Fatalf("got %s / %v, want %s", output.raw, err, test.expected)
			}
			// after and event rules also use the direct expression entrypoint.
			state := evaluation{ctx: t.Context(), input: input, budget: &evaluationBudget{limits: DefaultLimits()}}
			output, err := compiled.mappings[EncodeRequest].outputPlan.expression.evaluate(state)
			if err != nil || output.raw != test.expected {
				t.Fatalf("expression: got %s / %v, want %s", output.raw, err, test.expected)
			}
		})
	}
}

func TestProjectCompilationAndSourceTypes(t *testing.T) {
	read := readExpression("input", "")
	for _, expression := range []Expression{
		{Op: "project", Source: &read, Keys: []string{"same", "same"}, Policy: "exclude"},
		{Op: "project", Source: &read, Keys: []string{"bad\xff", "bad\ufffd"}, Policy: "include"},
		{Op: "project", Source: &read, Keys: []string{}, Policy: "other"},
		{Op: "project", Source: &read, Policy: "include"},
		{Op: "project", Source: expressionPointer(literalExpression([]int{1})), Keys: []string{}, Policy: "include"},
	} {
		compiler := expressionCompiler{limits: DefaultLimits()}
		if _, err := compiler.compile(expression, "/project", expressionScope{}, 1); err == nil {
			t.Fatalf("invalid projection compiled: %+v", expression)
		}
	}
	for _, path := range []string{"", "/missing"} {
		expression := compileTestOutputExpression(t, Expression{Op: "project", Source: expressionPointer(readExpression("input", path)), Keys: []string{}, Policy: "exclude"})
		plan, ok := compileOutputPlan(expression)
		if !ok {
			t.Fatal("projection has no executable plan")
		}
		for _, raw := range []string{`null`, `[]`, `true`, `"text"`, `{}`} {
			if path == "" && raw == `{}` {
				continue
			}
			state := evaluation{ctx: t.Context(), input: parseTestValue(t, raw), budget: &evaluationBudget{limits: DefaultLimits()}}
			if _, err := plan.evaluate(state); err == nil {
				t.Fatalf("non-object source accepted: path=%s input=%s", path, raw)
			}
		}
	}
}

func TestProjectBudgetAndSharedValues(t *testing.T) {
	const payloadBytes = 1 << 20
	payload := StringValue(strings.Repeat("x", payloadBytes))
	source := JSONObject(map[string]JSONOutput{
		"keep": JSONLeaf(payload), "remove": JSONLeaf(Value{raw: "null"}),
		"bad\xff": JSONLeaf(StringValue("first")), "bad\ufffd": JSONLeaf(StringValue("last")),
	})
	expression := compileTestOutputExpression(t, Expression{Op: "project", Source: expressionPointer(readExpression("input", "")), Keys: []string{"remove"}, Policy: "exclude"})
	plan, ok := compileOutputPlan(expression)
	if !ok {
		t.Fatal("projection has no executable plan")
	}
	reads := 0
	state := evaluation{ctx: t.Context(), budget: &evaluationBudget{limits: DefaultLimits()}, inputReader: func([]string) (JSONOutput, error) {
		reads++
		return source, nil
	}}
	projected, err := plan.build(state)
	if err != nil || reads != 1 || state.budget.steps != 2 {
		t.Fatal("projection did not evaluate its source once", err, reads, state.budget.steps)
	}
	selected, err := readOutputPointer(projected.output, []string{"keep"}, nil)
	if err != nil || !sharesValueStorage(payload, selected.value) {
		t.Fatal("projection copied immutable payload", err)
	}
	selected, err = readOutputPointer(projected.output, []string{"bad\ufffd"}, nil)
	if err != nil || !selected.value.IsString("first") {
		// Go keys sort before JSON normalization: invalid 0xff sorts last.
		t.Fatal("projection changed normalized duplicate selection", selected, err)
	}
	for _, test := range []struct {
		raw    string
		limit  int
		policy string
		keys   []string
	}{
		{`{"large":"discard me"}`, 10, "exclude", []string{"large"}},
		{`{"x":"<"}`, len(`{"x":"<"}`), "include", []string{"x"}},
	} {
		expression := compileTestOutputExpression(t, Expression{Op: "project", Source: expressionPointer(readExpression("input", "")), Keys: test.keys, Policy: test.policy})
		plan, _ := compileOutputPlan(expression)
		limits := DefaultLimits()
		limits.BufferBytes = test.limit
		state := evaluation{ctx: t.Context(), input: parseTestValue(t, test.raw), budget: &evaluationBudget{limits: limits}}
		_, err := plan.evaluate(state)
		var failure *ResourceLimitError
		if !errors.As(err, &failure) || failure.Metric != "bytes" {
			t.Fatal("projection bypassed source or final output budget", err)
		}
	}
}
