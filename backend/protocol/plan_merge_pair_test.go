package protocol

import (
	"fmt"
	"testing"
)

// Keep the preceding map implementation as a compatibility oracle only.
func mergePairOracle(plan *compiledOutputPlan, operands []plannedOutput) (plannedOutput, error) {
	fields := map[string]JSONOutput{}
	for _, operand := range operands {
		selected, err := plannedObjectFields(operand.output)
		if err != nil {
			return plannedOutput{}, err
		}
		for _, field := range selected {
			if _, exists := fields[field.key]; exists {
				switch plan.expression.policy {
				case "reject":
					return plannedOutput{}, fmt.Errorf("%s: conflicting merge field %q", plan.expression.path, field.key)
				case "first":
					continue
				}
			}
			fields[field.key] = field.value
		}
	}
	output := JSONObject(fields)
	return plannedOutput{output, normalizedOutputBytes(output)}, nil
}

func TestMergePairMatchesMapSemantics(t *testing.T) {
	inputs := []JSONOutput{
		JSONLeaf(parseTestValue(t, `{}`)),
		JSONLeaf(parseTestValue(t, `{"z":1,"a":2,"\u0061":9007199254740993,"text":"<&>😀"}`)),
		JSONObject(map[string]JSONOutput{"z": JSONLeaf(StringValue("last")), "a": JSONLeaf(Value{raw: "1e100"}), "bad\xff": JSONLeaf(StringValue("first")), "bad�": JSONLeaf(StringValue("second"))}),
		JSONLeaf(Value{raw: "null"}),
		JSONArray([]JSONOutput{}),
	}
	for _, policy := range []string{"first", "last", "reject"} {
		plan, _ := compileOutputPlan(compileTestOutputExpression(t, Expression{Op: "merge", Policy: policy, Items: []Expression{objectExpression(nil), objectExpression(nil)}}))
		for _, left := range inputs {
			for _, right := range inputs {
				operands := []plannedOutput{{output: left}, {output: right}}
				got, gotErr := plan.mergeObjects(operands)
				want, wantErr := mergePairOracle(plan, operands)
				if fmt.Sprint(gotErr) != fmt.Sprint(wantErr) {
					t.Fatal("merge error order changed", gotErr, wantErr)
				}
				if gotErr != nil {
					continue
				}
				actual, err := got.output.Encode(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				expected, err := want.output.Encode(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				if actual != expected || got.bytes != want.bytes {
					t.Fatal("merge changed output or logical bytes", actual, expected, got.bytes, want.bytes)
				}
				if got.output.node.origin != 0 {
					t.Fatal("merge forged root provenance")
				}
			}
		}
	}
}

func TestMergePairKeepsChildOriginWithoutForgingRoot(t *testing.T) {
	child := JSONObject(map[string]JSONOutput{"value": JSONLeaf(StringValue("captured"))})
	child.node.origin = capturedNativeOrigin
	left := JSONObject(map[string]JSONOutput{"native": child})
	right := JSONObject(map[string]JSONOutput{"extra": JSONLeaf(StringValue("added"))})
	plan, _ := compileOutputPlan(compileTestOutputExpression(t, Expression{Op: "merge", Policy: "last", Items: []Expression{objectExpression(nil), objectExpression(nil)}}))
	result, err := plan.mergeObjects([]plannedOutput{{output: left}, {output: right}})
	if err != nil {
		t.Fatal(err)
	}
	selected, err := readOutputPointer(result.output, []string{"native"}, nil)
	if err != nil || selected.node != child.node || result.output.node.origin != 0 {
		t.Fatal("merge changed origin ownership", err)
	}
}
