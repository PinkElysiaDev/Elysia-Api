package protocol

import (
	"reflect"
	"testing"
)

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
