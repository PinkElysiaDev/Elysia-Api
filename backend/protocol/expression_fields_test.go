package protocol

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/bytedance/sonic"
)

func TestExpressionFieldPresence(t *testing.T) {
	for _, fixture := range []struct {
		name, raw string
		isValid   bool
	}{
		{"literal-null", `{"op":"literal","value":null}`, true},
		{"literal-absent", `{"op":"literal"}`, false},
		{"unrelated-false", `{"op":"omit","required":false}`, false},
		{"unrelated-empty-string", `{"op":"omit","path":""}`, false},
		{"unrelated-empty-map", `{"op":"omit","fields":{}}`, false},
		{"unrelated-null", `{"op":"omit","source":null}`, false},
		{"null-source", `{"op":"map","source":null,"body":{"op":"omit"}}`, false},
		{"null-body", `{"op":"map","source":{"op":"read"},"body":null}`, false},
		{"empty-object", `{"op":"object","fields":{}}`, true},
		{"null-fields", `{"op":"object","fields":null}`, true},
		{"empty-cases", `{"op":"choose","source":{"op":"read"},"cases":{}}`, false},
		{"empty-values", `{"op":"enum","source":{"op":"read"},"values":{}}`, false},
		{"null-enum-member", `{"op":"enum","source":{"op":"read"},"values":{"none":null}}`, true},
		{"empty-keys", `{"op":"project","source":{"op":"read"},"keys":[],"policy":"include"}`, true},
		{"null-keys", `{"op":"project","source":{"op":"read"},"keys":null,"policy":"include"}`, false},
		{"mixed-case-fields", `{"op":"project","sourCe":{},"keYs":[],"poLiCY":"0"}`, false},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			var expression Expression
			if err := decodeContract([]byte(fixture.raw), &expression); err != nil {
				t.Fatal(err)
			}
			compiler := newExpressionCompiler(DefaultLimits(), nil)
			_, err := compiler.compilePlan(expression, "/program", expressionScope{}, 1)
			if (err == nil) != fixture.isValid {
				t.Fatalf("valid=%t: %v", fixture.isValid, err)
			}
		})
	}
}

func TestExpressionFieldsCheckGoAuthoredChanges(t *testing.T) {
	var expression Expression
	if err := decodeContract([]byte(`{"op":"literal","value":1}`), &expression); err != nil {
		t.Fatal(err)
	}
	for _, changed := range []Expression{
		{Op: "literal"},
		{Op: "enum", Source: expressionPointer(literalExpression("none")), Values: map[string]Value{"none": {}}},
		{Op: "map", Source: expressionPointer(literalExpression([]int{1})), Body: &Expression{Op: "literal", Required: true, Value: StringValue("bad")}},
	} {
		compiler := newExpressionCompiler(DefaultLimits(), nil)
		if _, err := compiler.compilePlan(changed, "/program", expressionScope{}, 1); err == nil {
			t.Fatal("invalid Go-authored expression compiled", changed.Op)
		}
	}
	// Presence retained from decoding cannot hide a later Go-side mutation.
	expression.Required = true
	compiler := newExpressionCompiler(DefaultLimits(), nil)
	if _, err := compiler.compilePlan(expression, "/program", expressionScope{}, 1); err == nil || !strings.Contains(err.Error(), "/required") {
		t.Fatal("new unrelated field escaped validation", err)
	}
	expression.Required, expression.Value = false, Value{}
	if _, err := compiler.compilePlan(expression, "/program", expressionScope{}, 1); err == nil || !strings.Contains(err.Error(), "/value") {
		t.Fatal("old presence allowed an absent required value", err)
	}
}

// Keep the old codec round trip only as a test oracle for field omission rules.
func encodedExpressionFieldsValid(expression Expression, operation OperationInfo) bool {
	raw, err := sonic.ConfigStd.Marshal(expression)
	if err != nil {
		return false
	}
	var fields map[string]json.RawMessage
	if err := sonic.ConfigStd.Unmarshal(raw, &fields); err != nil {
		return false
	}
	for key := range expression.present {
		if _, exists := fields[key]; !exists {
			fields[key] = nil
		}
	}
	for key := range fields {
		if key != "op" && !slices.Contains(operation.Fields, key) {
			return false
		}
	}
	for _, key := range operation.Required {
		if value := fields[key]; len(value) == 0 || string(value) == "null" && key != "value" {
			return false
		}
	}
	return true
}

func FuzzExpressionFieldPresence(f *testing.F) {
	for _, raw := range []string{`{"op":"literal","value":null}`, `{"op":"read","required":false}`, `{"op":"project","source":{"op":"read"},"keys":[],"policy":"include"}`, `{"op":"enum","source":{"op":"read"},"values":{"none":null}}`} {
		f.Add(raw)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		var expression Expression
		if err := decodeContract([]byte(raw), &expression); err != nil {
			t.Skip()
		}
		for _, operation := range expressionOperations {
			if operation.Name != expression.Op {
				continue
			}
			got := checkExpressionFields(expression, operation, "/program") == nil
			if got != encodedExpressionFieldsValid(expression, operation) {
				t.Fatal("field presence differs from the codec contract", raw)
			}
		}
	})
}
