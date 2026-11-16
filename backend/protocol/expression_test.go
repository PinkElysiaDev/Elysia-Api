package protocol

import (
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	"github.com/bytedance/sonic"
)

func TestReadBooleanKeepsDecoderBoundaries(t *testing.T) {
	readOriginal := func(value Value) (bool, error) {
		if valueType(value) != BooleanType {
			return false, fmt.Errorf("expected a boolean")
		}
		var result bool
		err := value.Decode(&result)
		return result, err
	}
	for _, raw := range []string{"true", "false", " \ttrue\r\n", "false ", "truefalse", "false0", "t", "f", "true\x00", "\u00a0true", "", "null", "0", `"true"`, "[]", "{}"} {
		value := Value{raw: raw}
		want, wantErr := readOriginal(value)
		got, gotErr := readBoolean(value)
		if got != want || !reflect.DeepEqual(gotErr, wantErr) {
			t.Fatalf("%q: got %t, %v; want %t, %v", raw, got, gotErr, want, wantErr)
		}
	}
}

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
		{"keys decoded and sorted", Expression{Op: "keys", Source: &read}, `{"z":null,"a":false,"\u0061":{}}`, `["a","z"]`, false},
		{"keys empty", Expression{Op: "keys", Source: &read}, `{}`, `[]`, false},
		{"keys null", Expression{Op: "keys", Source: &read}, `null`, "", true},
		{"keys missing", Expression{Op: "keys", Source: expressionPointer(readExpression("input", "/missing"))}, `{}`, "", true},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			definition := testDefinition("operators", "m", "h", "t", "r")
			definition.Mapping.Directions[EncodeRequest] = ProtocolDirectionMapping{Effects: generationFixtureEffects(EncodeRequest), Document: testDocument(EncodeRequest), Program: test.expression}
			compiled := compileTestDefinition(t, definition)
			input, err := ParseValue([]byte(test.input))
			if err != nil {
				t.Fatal(err)
			}
			output, err := evaluateFixturePlan(t.Context(), compiled, EncodeRequest, input)
			if test.shouldFail {
				if err == nil {
					t.Fatal("invalid operation succeeded")
				}
				return
			}
			if err != nil {
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
	definition.Spec.Documents["test"] = WireContractSpec{Schema: &ValueSchema{Type: ObjectType, Required: []string{"name"}, Properties: map[string]ValueSchema{"name": {Type: StringType}}}}
	definition.Mapping.Directions[EncodeRequest] = ProtocolDirectionMapping{Effects: generationFixtureEffects(EncodeRequest), Document: testDocument(EncodeRequest), Program: objectExpression(map[string]Expression{"name": literalExpression(false)})}
	compiler, _ := NewCompiler(DefaultLimits(), nil)
	raw, _ := sonic.ConfigStd.Marshal(definition)
	if _, issues := compiler.Compile(raw); IssuesError(issues) == nil {
		t.Fatal("incompatible nested output compiled")
	}
}

func TestExpressionDiscriminatorSchemaCoverage(t *testing.T) {
	const programPath = "/mapping/discriminator"
	schema := &ValueSchema{Type: StringType, Enum: []Value{StringValue("first"), StringValue("second")}}
	exact := parseTestValue(t, "9007199254740993")
	null := parseTestValue(t, "null")
	for _, operation := range []string{"choose", "enum"} {
		for _, fixture := range []struct {
			name                     string
			isComplete, hasOtherwise bool
		}{
			{"complete", true, false},
			{"missing", false, false},
			{"otherwise", false, true},
		} {
			t.Run(operation+"/"+fixture.name, func(t *testing.T) {
				expression := Expression{Op: operation, Source: expressionPointer(readExpression("input", ""))}
				if operation == "choose" {
					expression.Cases = map[string]Expression{"first": {Op: "literal", Value: exact}}
					if fixture.isComplete {
						expression.Cases["second"] = Expression{Op: "literal", Value: null}
					}
				} else {
					expression.Values = map[string]Value{"first": exact}
					if fixture.isComplete {
						expression.Values["second"] = null
					}
				}
				if fixture.hasOtherwise {
					expression.Otherwise = expressionPointer(literalExpression("fallback"))
				}
				compiler := newExpressionCompiler(DefaultLimits(), nil)
				plan, err := compiler.compilePlan(expression, programPath, expressionScope{input: schema}, 1)
				if !fixture.isComplete && !fixture.hasOtherwise {
					if err == nil || err.Error() != programPath+": declared discriminator value has no branch: second" || mappingErrorPath("", err) != programPath {
						t.Fatal("missing discriminator branch changed its diagnostic", err)
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				compiler.releaseGraph()
				for _, key := range []string{"first", "second"} {
					want := exact
					if key == "second" {
						want = null
						if fixture.hasOtherwise {
							want = StringValue("fallback")
						}
					}
					output, err := plan.evaluate(evaluation{ctx: t.Context(), input: StringValue(key), budget: &evaluationBudget{limits: DefaultLimits()}})
					if err != nil || output != want {
						t.Fatal("released discriminator changed its selected value", key, output, err)
					}
				}
			})
		}
	}
}

func TestExpressionLimitsAndNullSource(t *testing.T) {
	definition := testDefinition("bounded", "m", "h", "t", "r")
	definition.Mapping.Directions[EncodeRequest] = ProtocolDirectionMapping{Effects: generationFixtureEffects(EncodeRequest), Document: testDocument(EncodeRequest), Program: Expression{Op: "map", Source: expressionPointer(readExpression("input", "")), Body: expressionPointer(literalExpression("size"))}}
	limits := DefaultLimits()
	limits.Nodes = 20
	definition.Mapping.Limits = &limits
	// Keep the definition small enough for its intentionally small compile budget.
	definition.Mapping.Directions = map[Direction]ProtocolDirectionMapping{EncodeRequest: definition.Mapping.Directions[EncodeRequest]}
	op := definition.Spec.Operations["generate"]
	op.Response = ""
	definition.Mapping.Operations["generate"] = ProtocolOperationMapping{Request: EncodeRequest}
	definition.Spec.Operations["generate"] = op
	compiled := compileTestDefinition(t, definition)
	input, _ := EncodeValue(make([]int, 21))
	if _, issues := compiled.Execute(t.Context(), EncodeRequest, input, EvaluationContext{}); IssuesError(issues) == nil {
		t.Fatal("unbounded evaluation accepted")
	}
	compiler, _ := NewCompiler(DefaultLimits(), nil)
	raw, _ := sonic.ConfigStd.Marshal(definition)
	var document map[string]any
	_ = sonic.ConfigStd.Unmarshal(raw, &document)
	direction := document["mapping"].(map[string]any)["directions"].(map[string]any)["encode_request"].(map[string]any)
	direction["program"] = map[string]any{"op": "map", "source": nil, "body": map[string]any{"op": "literal", "value": 1}}
	raw, _ = sonic.ConfigStd.Marshal(document)
	if _, issues := compiler.Compile(raw); IssuesError(issues) == nil {
		t.Fatal("null source accepted")
	}
}
