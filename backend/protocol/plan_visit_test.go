package protocol

import (
	"reflect"
	"testing"
)

func TestVisitUsesOriginalValueLocation(t *testing.T) {
	children := readExpression("item", "/children")
	body := objectExpression(map[string]Expression{
		"name":     readExpression("item", "/name"),
		"native":   {Op: "capture_native", Source: expressionPointer(readExpression("item", ""))},
		"children": {Op: "if", When: &Expression{Op: "exists", Source: &children}, Then: &Expression{Op: "recur", Source: &children}},
	})
	expression := Expression{Op: "visit", Source: expressionPointer(readExpression("input", "/node")), Body: &body}
	compiler := expressionCompiler{limits: DefaultLimits()}
	plan, err := compiler.compilePlan(expression, "/program", expressionScope{wireCapture: true}, 1)
	if err != nil {
		t.Fatal(err)
	}
	input := parseTestValue(t, `{"node":{"name":"parent","children":[{"name":"child"}]}}`)
	output, err := plan.evaluate(evaluation{ctx: t.Context(), input: input, root: input, capture: &nativeCaptureState{}, budget: &evaluationBudget{limits: DefaultLimits()}})
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range []struct {
		pointer  []string
		expected string
	}{
		{[]string{"native", "source", "path"}, "/node"},
		{[]string{"children", "0", "native", "source", "path"}, "/node/children/0"},
	} {
		value, err := readValuePointer(output, entry.pointer)
		if err != nil || !value.IsString(entry.expected) {
			t.Fatal("visit invented an array position", output.raw, entry.expected, err)
		}
	}
	expression.Body = &Expression{Op: "index"}
	if _, err := compiler.compilePlan(expression, "/program", expressionScope{}, 1); err == nil {
		t.Fatal("single-value visit exposed a nonexistent array index")
	}
}

func TestVisitPreservesOnlyProvenCoreTypes(t *testing.T) {
	match := Expression{Op: "native_matches", Operation: "array_item", Path: "/content", Source: expressionPointer(readExpression("item", "/native/source"))}
	visit := Expression{Op: "visit", Source: expressionPointer(readExpression("input", "/content/0")), Body: &match}
	scope := expressionScope{coreInput: reflect.TypeFor[Request](), canReadNative: true}
	compiler := expressionCompiler{limits: DefaultLimits()}
	if _, err := compiler.compilePlan(visit, "/program", scope, 1); err != nil {
		t.Fatal("visit discarded the original core node type", err)
	}
	visit.Source = expressionPointer(objectExpression(map[string]Expression{"native": readExpression("input", "/content/0/native")}))
	if _, err := compiler.compilePlan(visit, "/program", scope, 1); err == nil {
		t.Fatal("constructed visit source acquired original-core authority")
	}
	visit.Source = expressionPointer(readExpression("input", "/content/0"))
	visit.Body = expressionPointer(objectExpression(map[string]Expression{
		"match": match, "children": {Op: "recur", Source: expressionPointer(readExpression("item", "/payload"))},
	}))
	if _, err := compiler.compilePlan(visit, "/program", scope, 1); err == nil {
		t.Fatal("opaque child values inherited the root core node's authority")
	}
}
