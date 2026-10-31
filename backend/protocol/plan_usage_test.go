package protocol

import (
	"errors"
	"reflect"
	"testing"
)

func TestPlanCheckUsageUsesJSONDirection(t *testing.T) {
	document := textProtocolPackage(t)
	document.Mapping.Capabilities[UsageCapability] = true
	document.Spec.Documents["provider-response"].Schema.Properties["usage"] = ValueSchema{Type: ObjectType, AllowUnknown: true}
	document.Mapping.Directions[DecodeResponse].Program.Fields["usage"] = Expression{Op: "check_usage", Source: expressionPointer(readExpression("input", "/usage"))}
	compiled := compilePackageForTest(t, document)
	_, err := compiled.DecodeResponse(t.Context(), []byte(`{"reply":"ok","usage":{"output":{"count":1,"origin":"observed"},"details":{"output.reasoning_tokens":{"count":2,"origin":"observed"}}}}`), EvaluationContext{})
	var failure *ConversionError
	if !errors.As(err, &failure) || len(failure.Issues) != 1 {
		t.Fatal("usage check lost its structured rejection", err)
	}
	issue := failure.Issues[0]
	if issue.Code != InvalidInput || issue.Protocol != compiled.Identity() || issue.Direction != DecodeResponse || issue.Stage != "core.usage" || issue.Path != "/usage/details/output.reasoning_tokens" {
		t.Fatal("JSON check_usage lost its direction or core location", issue)
	}
	var checked *declaredCheckError
	var neutral *ConversionError
	if !errors.As(err, &checked) || !errors.As(checked.err, &neutral) || neutral.Issues[0].Direction != "" || neutral.Unwrap() == nil || !errors.Is(err, neutral.Unwrap()) {
		t.Fatal("declared usage check lost its neutral error or original cause", err)
	}
}

func TestPlanMergeUsageRetainsObservedCounts(t *testing.T) {
	plan := numericTestPlan(t, Expression{Op: "merge_usage", Items: []Expression{
		readExpression("input", "/current"), readExpression("input", "/event"), readExpression("input", "/response"),
	}})
	input := numericTestInput(t, `{"current":{"input":{"count":9,"origin":"observed"},"total":{"count":20,"origin":"observed"},"details":{"images":{"count":0,"origin":"observed"}}},"event":{"input":{"count":0,"origin":"observed"},"output":{"count":2,"origin":"observed"}},"response":{"input":{"count":4,"origin":"placeholder"},"total":{"count":6,"origin":"inferred"},"details":{"images":{"count":5,"origin":"inferred"}}}}`)
	value, err := plan.evaluate(numericTestState(t, input))
	if err != nil {
		t.Fatal(err)
	}
	var got Usage
	if err := value.Decode(&got); err != nil {
		t.Fatal(err)
	}
	want := Usage{Input: &Counter{0, ObservedCount}, Output: &Counter{2, ObservedCount}, Total: &Counter{20, ObservedCount}, Details: map[string]Counter{"images": {0, ObservedCount}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatal("observed zero or provider total was overwritten", got)
	}
	for _, raw := range []string{`{}`, `{"current":null,"event":null}`} {
		output, err := plan.evaluate(numericTestState(t, numericTestInput(t, raw)))
		if err != nil || !output.IsZero() {
			t.Fatal("absent snapshots fabricated usage", output, err)
		}
	}
	if _, err := plan.evaluate(numericTestState(t, numericTestInput(t, `{"event":{"input":{"count":"2","origin":"observed"}}}`))); err == nil {
		t.Fatal("invalid counter shape was ignored")
	}
}
