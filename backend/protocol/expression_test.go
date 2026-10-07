package protocol

import (
	"encoding/json"
	"testing"
)

func TestExpressionOperationsAndFailureBoundaries(t *testing.T) {
	read := readExpression("input", "")
	cases := []struct {
		name            string
		expression      Expression
		input, expected string
		shouldFail      bool
	}{
		{"enum", Expression{Op: "enum", Source: &read, Values: map[string]Value{"human": StringValue("user")}}, `"human"`, `"user"`, false},
		{"enum unmapped", Expression{Op: "enum", Source: &read, Values: map[string]Value{"human": StringValue("user")}}, `"tool"`, "", true},
		{"choose", Expression{Op: "choose", Source: &read, Cases: map[string]Expression{"text": literalExpression(1)}, Otherwise: expressionPointer(literalExpression(2))}, `"media"`, `2`, false},
		{"choose required", Expression{Op: "choose", Source: &read, Cases: map[string]Expression{"text": literalExpression(1)}}, `"media"`, "", true},
		{"cast exact", Expression{Op: "cast", Source: &read, To: IntegerType}, `"9007199254740993"`, `9007199254740993`, false},
		{"cast fraction", Expression{Op: "cast", Source: &read, To: IntegerType}, `"1.5"`, "", true},
		{"parse JSON", Expression{Op: "parse_json", Source: &read}, `"{\"a\":1}"`, `{"a":1}`, false},
		{"invalid JSON", Expression{Op: "parse_json", Source: &read}, `"broken"`, "", true},
		{"stringify JSON", Expression{Op: "stringify_json", Source: &read}, `{"a":1}`, `"{\"a\":1}"`, false},
		{"concat", Expression{Op: "concat", Items: []Expression{literalExpression([]int{1}), literalExpression([]int{2, 3})}}, `null`, `[1,2,3]`, false},
		{"join", Expression{Op: "join", Items: []Expression{literalExpression("prefix "), read}}, `"suffix"`, `"prefix suffix"`, false},
		{"join dynamic", Expression{Op: "join", Source: &read}, `["hello",", ","world"]`, `"hello, world"`, false},
		{"join empty", Expression{Op: "join", Source: &read}, `[]`, `""`, false},
		{"join wrong member", Expression{Op: "join", Source: &read}, `["hello",0]`, "", true},
		{"join wrong source", Expression{Op: "join", Source: &read}, `null`, "", true},
		{"merge collision", Expression{Op: "merge", Policy: "reject", Items: []Expression{literalExpression(map[string]int{"a": 1}), read}}, `{"a":2}`, "", true},
		{"merge last", Expression{Op: "merge", Policy: "last", Items: []Expression{literalExpression(map[string]int{"a": 1}), read}}, `{"a":0}`, `{"a":0}`, false},
		{"stable sort", Expression{Op: "sort", Source: &read, Key: "/rank"}, `[{"rank":2,"id":"a"},{"rank":1,"id":"b"},{"rank":2,"id":"c"}]`, `[{"rank":1,"id":"b"},{"rank":2,"id":"a"},{"rank":2,"id":"c"}]`, false},
		{"association", Expression{Op: "associate", Source: &read, Key: "/call"}, `[{"call":"x","v":0},{"call":"y","v":false}]`, `{"x":{"call":"x","v":0},"y":{"call":"y","v":false}}`, false},
		{"duplicate association", Expression{Op: "associate", Source: &read, Key: "/call"}, `[{"call":"x"},{"call":"x"}]`, "", true},
		{"exists null", Expression{Op: "exists", Source: expressionPointer(readExpression("input", "/n"))}, `{"n":null}`, `true`, false},
		{"exists missing", Expression{Op: "exists", Source: expressionPointer(readExpression("input", "/n"))}, `{}`, `false`, false},
		{"present null", Expression{Op: "present", Source: expressionPointer(readExpression("input", "/n"))}, `{"n":null}`, `false`, false},
		{"present missing", Expression{Op: "present", Source: expressionPointer(readExpression("input", "/n"))}, `{}`, `false`, false},
		{"present false value", Expression{Op: "present", Source: expressionPointer(readExpression("input", "/n"))}, `{"n":false}`, `true`, false},
		{"equal exact number", Expression{Op: "equal", Items: []Expression{read, literalExpression(json.Number("9007199254740993"))}}, `9007199254740993`, `true`, false},
		{"not", Expression{Op: "not", Source: &read}, `false`, `true`, false},
		{"strip prefix", Expression{Op: "strip_prefix", Source: &read, Value: StringValue("models/")}, `"models/model"`, `"model"`, false},
		{"wrong prefix", Expression{Op: "strip_prefix", Source: &read, Value: StringValue("models/")}, `"other/model"`, "", true},
		{"prefix type", Expression{Op: "strip_prefix", Source: &read, Value: StringValue("models/")}, `null`, "", true},
		{"trim prefix present", Expression{Op: "trim_prefix", Source: &read, Value: StringValue("models/")}, `"models/model"`, `"model"`, false},
		{"trim prefix absent passes through", Expression{Op: "trim_prefix", Source: &read, Value: StringValue("models/")}, `"bare-model"`, `"bare-model"`, false},
		{"trim prefix type", Expression{Op: "trim_prefix", Source: &read, Value: StringValue("models/")}, `null`, "", true},
		{"truthiness rejected", Expression{Op: "not", Source: &read}, `0`, "", true},
		{"all", Expression{Op: "all", Items: []Expression{literalExpression(true), read}}, `false`, `false`, false},
		{"any", Expression{Op: "any", Items: []Expression{literalExpression(false), read}}, `true`, `true`, false},
		{"if", Expression{Op: "if", When: expressionPointer(Expression{Op: "exists", Source: expressionPointer(readExpression("input", "/a"))}), Then: expressionPointer(readExpression("input", "/a")), Otherwise: expressionPointer(literalExpression(3))}, `{"a":false}`, `false`, false},
		{"flatmap", Expression{Op: "flatmap", Source: &read, Body: expressionPointer(readExpression("item", ""))}, `[[1,2],[3]]`, `[1,2,3]`, false},
		{"filter", Expression{Op: "filter", Source: &read, When: expressionPointer(readExpression("item", "/keep"))}, `[{"keep":true},{"keep":false}]`, `[{"keep":true}]`, false},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			definition := testDefinition("operators", "m", "h", "t", "r")
			definition.Directions[EncodeRequest] = Mapping{Transform: &test.expression}
			compiled := compileTestDefinition(t, definition)
			input, err := ParseValue([]byte(test.input))
			if err != nil {
				t.Fatal(err)
			}
			output, issues := compiled.Execute(t.Context(), EncodeRequest, input, EvaluationContext{})
			if test.shouldFail {
				if IssuesError(issues) == nil {
					t.Fatal("invalid operation succeeded")
				}
				return
			}
			if err := IssuesError(issues); err != nil {
				t.Fatal(err)
			}
			expected, _ := ParseValue([]byte(test.expected))
			if !equalValues(output, expected) {
				t.Fatalf("got %s want %s", output.Bytes(), test.expected)
			}
		})
	}
}

func TestCompileChecksNestedOutputSchema(t *testing.T) {
	definition := testDefinition("typed", "m", "h", "t", "r")
	definition.Directions[EncodeRequest] = Mapping{Transform: expressionPointer(objectExpression(map[string]Expression{"name": literalExpression(false)})), Output: &ValueSchema{Type: ObjectType, Required: []string{"name"}, Properties: map[string]ValueSchema{"name": {Type: StringType}}}}
	compiler, _ := NewCompiler(DefaultLimits(), nil, nil)
	raw, _ := json.Marshal(definition)
	if _, issues := compiler.Compile(raw); IssuesError(issues) == nil {
		t.Fatal("incompatible nested output compiled")
	}
}

func TestExpressionLimitsAndNullSource(t *testing.T) {
	definition := testDefinition("bounded", "m", "h", "t", "r")
	definition.Directions[EncodeRequest] = Mapping{Transform: &Expression{Op: "map", Source: expressionPointer(readExpression("input", "")), Body: expressionPointer(literalExpression("size"))}}
	limits := DefaultLimits()
	limits.Nodes = 20
	definition.Limits = &limits
	// Keep the definition small enough for its intentionally small compile budget.
	definition.Directions = map[Direction]Mapping{EncodeRequest: definition.Directions[EncodeRequest]}
	op := definition.Operations["generate"]
	op.Response = ""
	definition.Operations["generate"] = op
	compiled := compileTestDefinition(t, definition)
	input, _ := EncodeValue(make([]int, 21))
	if _, issues := compiled.Execute(t.Context(), EncodeRequest, input, EvaluationContext{}); IssuesError(issues) == nil {
		t.Fatal("unbounded evaluation accepted")
	}
	compiler, _ := NewCompiler(DefaultLimits(), nil, nil)
	raw, _ := json.Marshal(definition)
	var document map[string]any
	_ = json.Unmarshal(raw, &document)
	direction := document["directions"].(map[string]any)["encode_request"].(map[string]any)
	direction["transform"] = map[string]any{"op": "map", "source": nil, "body": map[string]any{"op": "literal", "value": 1}}
	raw, _ = json.Marshal(document)
	if _, issues := compiler.Compile(raw); IssuesError(issues) == nil {
		t.Fatal("null source accepted")
	}
}
