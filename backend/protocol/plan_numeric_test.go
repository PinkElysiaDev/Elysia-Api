package protocol

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestPlanArrayLength(t *testing.T) {
	plan := numericTestPlan(t, Expression{Op: "length", Source: expressionPointer(readExpression("input", ""))})
	for _, fixture := range []struct {
		raw  string
		want int64
	}{{`[]`, 0}, {`[null,{"n":9007199254740993},"x"]`, 3}, {``, -1}, {`null`, -1}, {`{}`, -1}} {
		output, err := plan.evaluate(numericTestState(t, numericTestInput(t, fixture.raw)))
		if fixture.want < 0 {
			if err == nil {
				t.Fatal("non-array was counted", fixture.raw)
			}
		} else if err != nil || output != coreInteger(fixture.want) {
			t.Fatal("array member count changed", output, err)
		}
	}
	constructed := numericTestPlan(t, Expression{Op: "length", Source: &Expression{Op: "array", Items: []Expression{literalExpression(1), literalExpression(false)}}})
	if output, err := constructed.evaluate(numericTestState(t, Value{})); err != nil || output != coreInteger(2) {
		t.Fatal("constructed array length changed", output, err)
	}
}

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

func integerCodecFixtures() []struct{ name, raw string } {
	return []struct{ name, raw string }{
		{"zero", "0"}, {"negative-zero", "-0"}, {"positive", "42"}, {"negative", "-42"},
		{"whitespace", " \t\r\n42\t\n "}, {"negative-zero-whitespace", " \n-0\r\t"},
		{"beyond-float-integer", "9007199254740993"}, {"negative-beyond-float-integer", "-9007199254740993"},
		{"maximum", "9223372036854775807"}, {"minimum", "-9223372036854775808"},
		{"positive-overflow", "9223372036854775808"}, {"negative-overflow", "-9223372036854775809"},
		{"wide-overflow", "999999999999999999999999999999999999999999999999"},
		{"decimal-zero", "0.0"}, {"decimal-negative-zero", "-0.0"}, {"decimal-integral", "1.0"},
		{"fractional", "1.5"}, {"negative-fractional", "-1.5"}, {"decimal-large", "9007199254740993.0"},
		{"exponent-zero", "1e0"}, {"exponent-positive", "1E+2"}, {"exponent-negative", "1e-1"},
		{"exponent-integral", "10e-1"}, {"exponent-negative-zero", "-0e100"}, {"exponent-large", "1e999999999"},
		{"positive-eighteen-bytes", "999999999999999999"},
		{"positive-nineteen-bytes", "1000000000000000000"},
		{"negative-eighteen-bytes", "-99999999999999999"},
		{"negative-nineteen-bytes", "-100000000000000000"},
		{"absent", ""}, {"null", "null"}, {"string", `"1"`}, {"boolean", "true"},
		{"object", "{}"}, {"array", "[]"},
	}
}

// This oracle preserves the frozen original codec boundary, including its wrapper.
func readIntegerWithCodec(output JSONOutput) (int64, error) {
	if plannedJSONType(output) != "number" {
		return 0, fmt.Errorf("expected an int64 number")
	}
	var number int64
	if err := output.value.Decode(&number); err != nil {
		return 0, fmt.Errorf("expected an int64 number: %w", err)
	}
	return number, nil
}

func compareIntegerErrors(t testing.TB, actual, expected error) {
	t.Helper()
	for _, sentinel := range []error{strconv.ErrRange, strconv.ErrSyntax, context.Canceled, context.DeadlineExceeded} {
		if errors.Is(actual, sentinel) != errors.Is(expected, sentinel) {
			t.Fatalf("integer error identity changed for %v: got %v, want %v", sentinel, actual, expected)
		}
	}
	var actualType, expectedType *json.UnmarshalTypeError
	if errors.As(actual, &actualType) != errors.As(expected, &expectedType) || !reflect.DeepEqual(actualType, expectedType) {
		t.Fatalf("integer codec type error changed: got %#v, want %#v", actualType, expectedType)
	}
	var actualNumber, expectedNumber *strconv.NumError
	if errors.As(actual, &actualNumber) != errors.As(expected, &expectedNumber) || !reflect.DeepEqual(actualNumber, expectedNumber) {
		t.Fatalf("integer numeric error changed: got %#v, want %#v", actualNumber, expectedNumber)
	}
	for actual != nil || expected != nil {
		if actual == nil || expected == nil || reflect.TypeOf(actual) != reflect.TypeOf(expected) || actual.Error() != expected.Error() || mappingErrorPath("", actual) != mappingErrorPath("", expected) {
			t.Fatalf("integer error chain changed: got %T %v, want %T %v", actual, actual, expected, expected)
		}
		actual, expected = errors.Unwrap(actual), errors.Unwrap(expected)
	}
}

func TestPlannedIntegerCodecParity(t *testing.T) {
	read := readExpression("input", "")
	query := numericTestPlan(t, Expression{Op: "is_integer", Source: &read})
	for _, fixture := range integerCodecFixtures() {
		t.Run(fixture.name, func(t *testing.T) {
			input := numericTestInput(t, fixture.raw)
			output := JSONLeaf(input)
			want, expectedErr := readIntegerWithCodec(output)
			actual, actualErr := readPlannedInteger(output)
			if actual != want {
				t.Fatal("exact integer changed", actual, want)
			}
			compareIntegerErrors(t, actualErr, expectedErr)
			state := numericTestState(t, input)
			result, err := query.evaluate(state)
			if err != nil || result != coreBoolean(expectedErr == nil) || state.budget.steps != 2 {
				t.Fatal("integer predicate changed codec acceptance or steps", result, err, state.budget.steps)
			}
		})
	}
}

func TestPlanIntegerCodecErrorPaths(t *testing.T) {
	expression := Expression{Op: "integer", Operation: "add", Items: []Expression{readExpression("input", ""), literalExpression(0)}}
	plan := numericTestPlan(t, expression)
	for _, fixture := range integerCodecFixtures() {
		t.Run(fixture.name, func(t *testing.T) {
			input := numericTestInput(t, fixture.raw)
			want, expectedErr := readIntegerWithCodec(JSONLeaf(input))
			state := numericTestState(t, input)
			result, actualErr := plan.evaluate(state)
			compareIntegerErrors(t, actualErr, locateMappingError("/mapping/items/0", expectedErr))
			if state.budget.steps != 3 || expectedErr == nil && result != coreInteger(want) {
				t.Fatal("integer operand result or eager steps changed", result, state.budget.steps)
			}
		})
	}
}

func TestPlannedIntegerConstructedContainers(t *testing.T) {
	for _, fixture := range []struct {
		operation string
		output    JSONOutput
	}{
		{"object", JSONObject(map[string]JSONOutput{})},
		{"array", JSONArray([]JSONOutput{})},
	} {
		t.Run(fixture.operation, func(t *testing.T) {
			want, expectedErr := readIntegerWithCodec(fixture.output)
			actual, actualErr := readPlannedInteger(fixture.output)
			if actual != want {
				t.Fatal("constructed container changed integer result", actual, want)
			}
			compareIntegerErrors(t, actualErr, expectedErr)
			source := Expression{Op: fixture.operation}
			plan := numericTestPlan(t, Expression{Op: "is_integer", Source: &source})
			state := numericTestState(t, Value{})
			result, err := plan.evaluate(state)
			if err != nil || result != coreBoolean(false) || state.budget.steps != 2 {
				t.Fatal("constructed container was treated as an integer", result, err, state.budget.steps)
			}
		})
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
		{"multiply", "[9007199254740993,3]", "27021597764222979", 2},
		{"multiply", "[-9223372036854775808,1]", "-9223372036854775808", 2},
		{"multiply", "[0,-9223372036854775808]", "0", 2},
		{"multiply", "[-3,-7]", "21", 2},
		{"divide", "[-8,3]", "-2", 2},
		{"divide", "[8,-3]", "-2", 2},
		{"remainder", "[-8,3]", "-2", 2},
		{"remainder", "[8,-3]", "2", 2},
		{"remainder", "[-9223372036854775808,-1]", "0", 2},
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
		{"multiply", "[9223372036854775807,2]", "multiplication overflow", 2},
		{"multiply", "[-9223372036854775808,-1]", "multiplication overflow", 2},
		{"multiply", "[-1,-9223372036854775808]", "multiplication overflow", 2},
		{"divide", "[-9223372036854775808,-1]", "division overflow", 2},
		{"divide", "[1,0]", "division by zero", 2},
		{"remainder", "[1,0]", "division by zero", 2},
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

func TestSmallOperandConsumersPreserveFailureOrder(t *testing.T) {
	for _, operation := range []string{"integer", "join", "merge_usage"} {
		for _, count := range []int{2, 3, 4, 5, 16} {
			expression := Expression{Op: operation}
			if operation == "integer" {
				expression.Operation = "add"
			}
			for index := range count {
				expression.Items = append(expression.Items, readExpression("input", fmt.Sprintf("/%d", index)))
			}
			expression.Items[count-1].Required = true
			plan := numericTestPlan(t, expression)
			input := `["invalid"]`
			if operation == "join" {
				input = `[123]`
			}
			state := numericTestState(t, numericTestInput(t, input))
			_, err := plan.evaluate(state)
			if err == nil || !strings.Contains(err.Error(), "required field is absent") || mappingErrorPath("", err) != fmt.Sprintf("/mapping/items/%d", count-1) || state.budget.steps != count+1 {
				t.Fatal("operand type checking preceded evaluation", operation, count, err, state.budget.steps)
			}
		}
	}
	plan := numericTestPlan(t, numericTestExpression("add", 3))
	_, err := plan.evaluate(numericTestState(t, numericTestInput(t, `[9223372036854775807,1,"invalid"]`)))
	if err == nil || mappingErrorPath("", err) != "/mapping/items/2" || strings.Contains(err.Error(), "overflow") {
		t.Fatal("arithmetic preceded complete type checking", err)
	}
}

func TestPlanNumericCompilation(t *testing.T) {
	read := readExpression("input", "")
	for _, expression := range []Expression{
		{Op: "type"}, {Op: "type", Source: &read, Operation: "add"},
		{Op: "integer", Operation: "power", Items: []Expression{read, read}},
		numericTestExpression("multiply", 3), numericTestExpression("divide", 1), numericTestExpression("remainder", 0),
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
	}{{"add", IntegerType}, {"subtract", IntegerType}, {"multiply", IntegerType}, {"divide", IntegerType}, {"remainder", IntegerType}, {"lt", BooleanType}, {"lte", BooleanType}, {"gt", BooleanType}, {"gte", BooleanType}} {
		plan := numericTestPlan(t, numericTestExpression(tc.operation, 2))
		if plan.expression.result != tc.result {
			t.Fatal("numeric operation has wrong static type", tc.operation, plan.expression.result)
		}
	}
}

func TestPlanStringSelectors(t *testing.T) {
	read := readExpression("input", "")
	for _, fixture := range []struct {
		operation, input, pattern, want string
	}{
		{"lower", `"MODELS/ÄBC-Σ"`, "", `"models/äbc-σ"`},
		{"lower", `""`, "", `""`},
		{"quote_string", `"<model>\n\u2028"`, "", `"\"\u003cmodel\u003e\\n\\u2028\""`},
		{"has_prefix", `"models/name"`, "models/", "true"},
		{"has_prefix", `"Models/name"`, "models/", "false"},
		{"contains", `"model-audio-preview"`, "audio", "true"},
		{"contains", `"model"`, "audio", "false"},
		{"contains", `"\u0070ro"`, "pro", "true"},
		{"has_prefix", `"value"`, "", "true"},
	} {
		expression := Expression{Op: fixture.operation, Source: &read}
		if fixture.operation != "lower" && fixture.operation != "quote_string" {
			expression.Value = StringValue(fixture.pattern)
		}
		plan := numericTestPlan(t, expression)
		value, err := plan.evaluate(numericTestState(t, numericTestInput(t, fixture.input)))
		if err != nil || value.JSONString() != fixture.want {
			t.Fatal(fixture, value.JSONString(), err)
		}
		for _, invalid := range []string{"", "null", "1", "false", "[]", "{}"} {
			if _, err := plan.evaluate(numericTestState(t, numericTestInput(t, invalid))); err == nil {
				t.Fatal("string source was coerced", fixture.operation, invalid)
			}
		}
	}
	for _, expression := range []Expression{
		{Op: "lower"}, {Op: "lower", Source: expressionPointer(literalExpression(1))},
		{Op: "contains", Source: &read, Value: coreBoolean(true)},
		{Op: "has_prefix", Source: expressionPointer(literalExpression(false)), Value: StringValue("prefix")},
	} {
		compiler := expressionCompiler{limits: DefaultLimits()}
		if _, err := compiler.compilePlan(expression, "/string", expressionScope{}, 1); err == nil {
			t.Fatal("invalid string expression compiled", expression)
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
