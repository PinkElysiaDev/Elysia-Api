package protocol

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestPlanTypeQueries(t *testing.T) {
	read := readExpression("input", "")
	for _, tc := range []struct {
		raw, kind string
		isInteger bool
	}{
		{"", "missing", false}, {"null", "null", false}, {`"1"`, "string", false},
		{"true", "boolean", false}, {"false", "boolean", false}, {"[]", "array", false}, {"{}", "object", false},
		{"0", "number", true}, {"-0", "number", true}, {" 42 ", "number", true},
		{"9223372036854775807", "number", true}, {"-9223372036854775808", "number", true},
		{"9223372036854775808", "number", false}, {"-9223372036854775809", "number", false},
		{"1.5", "number", false}, {"1e999999999", "number", false},
	} {
		t.Run(tc.raw, func(t *testing.T) {
			input := numericTestInput(t, tc.raw)
			for _, operation := range []string{"type", "is_integer"} {
				plan := numericTestPlan(t, Expression{Op: operation, Source: &read})
				state := numericTestState(t, input)
				value, err := plan.evaluate(state)
				want := StringValue(tc.kind)
				if operation == "is_integer" {
					want = coreBoolean(tc.isInteger)
				}
				if err != nil || value != want || state.budget.steps != 2 {
					t.Fatalf("%s: got %s / %v (%d steps), want %s", operation, value.raw, err, state.budget.steps, want.raw)
				}
			}
		})
	}
	for _, raw := range []string{"1.0", "1e0", "1E+2", "1e-1"} {
		input := numericTestInput(t, raw)
		var decoded int64
		want := input.Decode(&decoded) == nil
		value, err := numericTestPlan(t, Expression{Op: "is_integer", Source: &read}).evaluate(numericTestState(t, input))
		if err != nil || value != coreBoolean(want) {
			t.Fatal("integer query changed the Value.Decode contract", raw, value, err)
		}
	}
	for _, operation := range []string{"object", "array"} {
		source := Expression{Op: operation}
		value, err := numericTestPlan(t, Expression{Op: "type", Source: &source}).evaluate(numericTestState(t, Value{}))
		if err != nil || !value.IsString(operation) {
			t.Fatal("constructed container type changed", value, err)
		}
	}
}

func TestPlanIntegerOperations(t *testing.T) {
	for _, tc := range []struct {
		operation, input, want string
		operands               int
	}{
		{"add", "[1,2,-3]", "0", 3},
		{"add", "[9223372036854775806,1]", "9223372036854775807", 2},
		{"add", "[-9223372036854775808,0]", "-9223372036854775808", 2},
		{"subtract", "[-9223372036854775808,-9223372036854775808]", "0", 2},
		{"subtract", "[9223372036854775807,9223372036854775807]", "0", 2},
		{"subtract", "[-1,9223372036854775807]", "-9223372036854775808", 2},
		{"lt", "[-9223372036854775808,9223372036854775807]", "true", 2},
		{"lte", "[2,2]", "true", 2},
		{"gt", "[9007199254740993,9007199254740992]", "true", 2},
		{"gte", "[2,3]", "false", 2},
	} {
		t.Run(tc.operation+tc.input, func(t *testing.T) {
			state := numericTestState(t, numericTestInput(t, tc.input))
			value, err := numericTestPlan(t, numericTestExpression(tc.operation, tc.operands)).evaluate(state)
			if err != nil || value.raw != tc.want || state.budget.steps != tc.operands+1 {
				t.Fatal("integer result or evaluation count changed", value, err, state.budget.steps)
			}
		})
	}
	for _, tc := range []struct {
		operation, input, message string
		operands                  int
	}{
		{"add", "[9223372036854775807,1]", "addition overflow", 2},
		{"add", "[-9223372036854775808,-1]", "addition overflow", 2},
		{"add", "[9223372036854775807,1,-1]", "addition overflow", 3},
		{"subtract", "[0,-9223372036854775808]", "subtraction overflow", 2},
		{"subtract", "[-9223372036854775808,1]", "subtraction overflow", 2},
		{"subtract", "[9223372036854775807,-1]", "subtraction overflow", 2},
	} {
		_, err := numericTestPlan(t, numericTestExpression(tc.operation, tc.operands)).evaluate(numericTestState(t, numericTestInput(t, tc.input)))
		if err == nil || !strings.Contains(err.Error(), tc.message) {
			t.Fatal("integer overflow was not rejected", tc.input, err)
		}
	}
}

func TestPlanIntegerRejectsNonIntegers(t *testing.T) {
	plan := numericTestPlan(t, numericTestExpression("add", 2))
	for _, input := range []string{`[null,1]`, `[true,1]`, `["1",1]`, `[{},1]`, `[[],1]`, `[1.5,1]`, `[9223372036854775808,1]`, `[1]`} {
		_, err := plan.evaluate(numericTestState(t, numericTestInput(t, input)))
		path := "/mapping/items/0"
		if input == "[1]" {
			path = "/mapping/items/1"
		}
		if err == nil || !strings.Contains(err.Error(), "expected an int64 number") || mappingErrorPath("", err) != path {
			t.Fatal("integer operand was coerced or misattributed", input, err)
		}
	}
}

func TestPlanIntegerEvaluationBoundaries(t *testing.T) {
	expression := numericTestExpression("add", 2)
	expression.Items[1].Required = true
	plan := numericTestPlan(t, expression)
	state := numericTestState(t, numericTestInput(t, `["invalid"]`))
	_, err := plan.evaluate(state)
	if err == nil || !strings.Contains(err.Error(), "required field is absent") || mappingErrorPath("", err) != "/mapping/items/1" || state.budget.steps != 3 {
		t.Fatal("operand validation ran before all operand expressions", err, state.budget.steps)
	}
	state = numericTestState(t, numericTestInput(t, `[100,1]`))
	state.budget.limits.BufferBytes = 3
	_, err = plan.evaluate(state)
	var limit *ResourceLimitError
	if !errors.As(err, &limit) || state.budget.steps != 2 {
		t.Fatal("intermediate operand byte budget changed", err, state.budget.steps)
	}
	state = numericTestState(t, numericTestInput(t, `[1,2]`))
	state.budget.stepLimit = 2
	_, err = plan.evaluate(state)
	if !errors.As(err, &limit) || state.budget.steps != 3 {
		t.Fatal("operand step budget changed", err, state.budget.steps)
	}
	state = numericTestState(t, numericTestInput(t, `[1,2]`))
	state.ctx = &outputPlanCancelContext{Context: t.Context(), remaining: 3}
	_, err = plan.evaluate(state)
	if !errors.Is(err, context.Canceled) || mappingErrorPath("", err) != "/mapping/items/1" {
		t.Fatal("operand cancellation boundary changed", err)
	}
	query := numericTestPlan(t, Expression{Op: "is_integer", Source: &expression.Items[1]})
	_, err = query.evaluate(numericTestState(t, Value{}))
	if err == nil || !strings.Contains(err.Error(), "required field is absent") {
		t.Fatal("type query swallowed a source evaluation error", err)
	}
}

func TestPlanNumericCompilation(t *testing.T) {
	read := readExpression("input", "")
	for _, expression := range []Expression{
		{Op: "type"}, {Op: "type", Source: &read, Operation: "add"},
		{Op: "integer", Operation: "multiply", Items: []Expression{read, read}},
		numericTestExpression("add", 1), numericTestExpression("subtract", 3), numericTestExpression("gte", 1),
		{Op: "integer", Operation: "add", Items: []Expression{literalExpression("1"), read}},
	} {
		compiler := expressionCompiler{limits: DefaultLimits()}
		if _, err := compiler.compilePlan(expression, "/numeric", expressionScope{}, 1); err == nil {
			t.Fatal("invalid numeric expression compiled", expression)
		}
	}
	for _, tc := range []struct {
		operation string
		result    JSONType
	}{{"add", IntegerType}, {"subtract", IntegerType}, {"lt", BooleanType}, {"lte", BooleanType}, {"gt", BooleanType}, {"gte", BooleanType}} {
		plan := numericTestPlan(t, numericTestExpression(tc.operation, 2))
		if plan.expression.result != tc.result {
			t.Fatal("numeric operation has wrong static type", tc.operation, plan.expression.result)
		}
	}
}

func numericTestPlan(t *testing.T, expression Expression) *compiledOutputPlan {
	t.Helper()
	compiled := compileTestOutputExpression(t, expression)
	plan, ok := compileOutputPlan(compiled)
	if !ok {
		t.Fatal("numeric operation has no compiled plan")
	}
	return plan
}

func numericTestInput(t *testing.T, raw string) Value {
	t.Helper()
	if raw == "" {
		return Value{}
	}
	value, err := ParseValue([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func numericTestState(t *testing.T, input Value) evaluation {
	return evaluation{ctx: t.Context(), input: input, root: input, budget: &evaluationBudget{limits: DefaultLimits()}}
}

func numericTestExpression(operation string, operands int) Expression {
	expression := Expression{Op: "integer", Operation: operation}
	for index := range operands {
		expression.Items = append(expression.Items, readExpression("input", fmt.Sprintf("/%d", index)))
	}
	return expression
}
