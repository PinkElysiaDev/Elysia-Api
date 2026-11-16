package protocol

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/bytedance/sonic"
)

func TestExpressionEncodingFieldContract(t *testing.T) {
	kind := reflect.TypeFor[Expression]()
	var fields []string
	for index := range kind.NumField() {
		field := kind.Field(index)
		if field.IsExported() {
			fields = append(fields, field.Name+"|"+strings.ReplaceAll(field.Type.String(), "protocol.", "")+"|"+field.Tag.Get("json"))
		}
	}
	if strings.Join(fields, "\n") != expressionEncodingContract {
		t.Fatal("Expression field contract changed; regenerate its append encoder")
	}
}

func expressionEncodingCause(err error) error {
	for {
		var marshaler *json.MarshalerError
		if !errors.As(err, &marshaler) {
			return err
		}
		err = marshaler.Err
	}
}

func checkExpressionEncoding(t *testing.T, expression Expression) {
	t.Helper()
	oracle := buildRecursiveExpressionOracle(expression)
	for name, encode := range map[string]func(any) ([]byte, error){"sonic": sonic.ConfigStd.Marshal, "standard": json.Marshal} {
		want, wantErr := encode(oracle)
		got, gotErr := encode(expression)
		if !bytes.Equal(got, want) || fmt.Sprint(expressionEncodingCause(gotErr)) != fmt.Sprint(expressionEncodingCause(wantErr)) {
			t.Fatalf("%s changed recursive codec output or refusal: got=%s / %v want=%s / %v", name, got, gotErr, want, wantErr)
		}
	}
	want, wantErr := oracle.MarshalJSON()
	got, gotErr := expression.MarshalJSON()
	if !bytes.Equal(got, want) || fmt.Sprint(gotErr) != fmt.Sprint(wantErr) {
		t.Fatalf("direct marshal changed: got=%s / %v want=%s / %v", got, gotErr, want, wantErr)
	}
}

func TestExpressionEncodingOmissionAndEscaping(t *testing.T) {
	child := literalExpression("\"\\\n<&>\u2028猫😀\xff")
	for _, expression := range []Expression{
		{},
		{Op: "object", Fields: map[string]Expression{}, Items: []Expression{}, Cases: map[string]Expression{}, Values: map[string]Value{}},
		{Op: "project", Keys: []string{}, Prefixes: []string{}},
		{Op: "project", Keys: []string{"", "a\n<&>猫", "\xff"}, Prefixes: []string{"\u2029"}},
		{Op: "object", Fields: map[string]Expression{"z": child, "a": child, "\xff": child, "�": child, "<\n": child}},
		{Op: "array", Items: []Expression{child, {}, {Op: "literal", Value: Value{raw: "null"}}}},
		{Op: "if", When: &child, Then: &child, Otherwise: &child},
		{Op: "all-fields", Operation: "mode", Name: "name", Namespace: "scope", KeyMode: "raw_json", From: "input", Path: "/<value>",
			Value:  Value{raw: `  {"decimal":9007199254740993.25,"exponent":1e+0003,"zero":-0,"huge":1e9999,"html":"<>&","line":"\u2028","opaque":"$binding/"}  `},
			Fields: map[string]Expression{"field": child}, Items: []Expression{child}, Source: &child, Body: &child, When: &child, Then: &child, Otherwise: &child,
			Cases: map[string]Expression{"b": child, "a": child}, Values: map[string]Value{"b": StringValue("$input"), "a": Value{raw: "9007199254740993"}},
			To: StringType, Key: "/key", Keys: []string{}, Prefixes: []string{}, Policy: "reject", OnConflict: &MergeConflictDiagnostic{},
			OnUnknown: &ObjectFieldDiagnostic{}, Diagnostic: &RejectDiagnostic{}, Reason: "<reason>", Ref: "reference", Required: true},
	} {
		checkExpressionEncoding(t, expression)
	}
}

func TestExpressionEncodingCompactMutationsAndErrors(t *testing.T) {
	for _, raw := range []string{`"$input"`, `"$binding/a~1b~0c/<x>"`, `{"op":"object","fields":{"a":"$input/value","b":{"op":"literal","value":"$input/~invalid"}}}`} {
		checkExpressionEncoding(t, decodeCompactFixture(t, raw))
	}
	for _, mutate := range []func(*Expression){
		func(expression *Expression) { expression.Required = true },
		func(expression *Expression) { expression.Path = "/changed" },
		func(expression *Expression) { expression.Path = "/~invalid" },
		func(expression *Expression) { expression.From = "invalid" },
		func(expression *Expression) { expression.Keys = []string{} },
		func(expression *Expression) { expression.Source = expressionPointer(literalExpression(1)) },
		func(expression *Expression) { expression.unknownKeys = []string{"surprise"} },
		func(expression *Expression) { expression.Op = "literal"; expression.Value = StringValue("new") },
	} {
		expression := decodeCompactFixture(t, `"$input/value"`)
		mutate(&expression)
		checkExpressionEncoding(t, expression)
		checkExpressionEncoding(t, objectExpression(map[string]Expression{"nested": expression}))
	}
	invalid := Expression{Op: "enum", Values: map[string]Value{"first": {}, "second": StringValue("valid")}}
	checkExpressionEncoding(t, invalid)
	checkExpressionEncoding(t, Expression{Op: "array", Items: []Expression{invalid, decodeCompactFixture(t, `"$input"`)}})
	binding := decodeCompactFixture(t, `"$binding/name"`)
	binding.From, binding.Name = "input", ""
	checkExpressionEncoding(t, binding)
}

func TestExpressionEncodingDeepOpaquePayload(t *testing.T) {
	const payloadBytes = 128 << 10
	for _, depth := range []int{4, 16, 24} {
		t.Run(fmt.Sprint(depth), func(t *testing.T) {
			expression := objectExpression(map[string]Expression{
				"payload": literalExpression(strings.Repeat("x", payloadBytes)),
				"read":    decodeCompactFixture(t, `"$input/model"`),
				"exact":   {Op: "literal", Value: Value{raw: `{"number":9007199254740993.25,"zero":-0,"text":"$input/x"}`}},
			})
			for index := range depth {
				switch index % 3 {
				case 0:
					expression = objectExpression(map[string]Expression{"nested": expression})
				case 1:
					expression = Expression{Op: "if", When: expressionPointer(literalExpression(true)), Then: expressionPointer(expression)}
				case 2:
					expression = Expression{Op: "let", Name: "bound", Source: expressionPointer(literalExpression(1)), Body: expressionPointer(expression)}
				}
			}
			checkExpressionEncoding(t, expression)
			encoded, err := EncodeValue(expression)
			if err != nil || !strings.Contains(encoded.JSONString(), "9007199254740993.25") || !strings.Contains(encoded.JSONString(), `"$input/model"`) {
				t.Fatal("nested encoding changed exact payload or compact syntax", err)
			}
		})
	}
}
