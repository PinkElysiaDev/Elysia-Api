package protocol

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func buildRawFieldFixture(t testing.TB, count int, hasAliases bool) Value {
	t.Helper()
	values := []string{`9007199254740993`, `1e+30`, `null`, `false`, `{ "text": "<&>", "items": [ 1, 2 ] }`}
	var raw strings.Builder
	raw.WriteByte('{')
	for index := count - 1; index >= 0; index-- {
		if index != count-1 {
			raw.WriteByte(',')
		}
		fmt.Fprintf(&raw, "%q:%s", fmt.Sprintf("field%02d", index), values[index%len(values)])
	}
	if hasAliases {
		if count != 0 {
			raw.WriteString(`,"\u0066ield00":9007199254740993,`)
		}
		raw.WriteString("\"bad\xff\":0,\"bad\\ufffd\":1e+30")
	}
	raw.WriteByte('}')
	value, err := ParseValue([]byte(raw.String()))
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func TestRawObjectFieldMaterialization(t *testing.T) {
	inputs := []Value{parseTestValue(t, `{"":null,"a/b~":false,"a":1,"\u0061":9007199254740993}`)}
	for _, count := range []int{0, 1, 4, 5, 8, 9, 16} {
		for _, hasAliases := range []bool{false, true} {
			inputs = append(inputs, buildRawFieldFixture(t, count, hasAliases))
		}
	}
	for index, input := range inputs {
		t.Run(fmt.Sprint(index), func(t *testing.T) {
			public, err := input.ReadObject()
			if err != nil {
				t.Fatal(err)
			}
			want, err := EncodeValue(public)
			if err != nil {
				t.Fatal(err)
			}
			fields, err := plannedObjectFields(JSONLeaf(input))
			if err != nil || len(fields) != len(public) {
				t.Fatal("raw materialization changed field count", fields, err)
			}
			keys := sortedKeys(public)
			for index, field := range fields {
				if field.key != keys[index] || sharesValueStorage(input, Value{raw: field.key}) {
					t.Fatal("materialized key order or independent ownership changed", field.key)
				}
			}
			output := JSONOutput{node: &jsonOutputNode{object: true, hasCanonicalKeys: true, hasSortedFields: true, fields: fields}}
			got, err := output.Encode(t.Context())
			if err != nil || got != want || output.node.origin != 0 {
				t.Fatal("raw fields changed canonical bytes or forged an origin", got, want, err)
			}
		})
	}
	for _, input := range []Value{{}, Value{raw: "null"}, Value{raw: "[]"}, Value{raw: "true"}} {
		if _, err := plannedObjectFields(JSONLeaf(input)); err == nil || err.Error() != "expected JSON object" {
			t.Fatal("raw non-object diagnostic changed", input, err)
		}
	}
}

func TestRawFieldMergePreservesOperandErrors(t *testing.T) {
	for _, count := range []int{0, 1, 4, 5, 8, 9, 16} {
		input := buildRawFieldFixture(t, count, false)
		missing := readExpression("input", "/missing")
		missing.Required = true
		plan := numericTestPlan(t, Expression{Op: "merge", Policy: "reject", Items: []Expression{
			readExpression("input", "/left"), readExpression("input", "/right"), missing,
		}})
		for _, boundary := range []string{"source", "bytes", "cancel"} {
			state := numericTestState(t, input)
			state.inputReader = func(pointer []string) (JSONOutput, error) {
				if pointer[0] == "missing" {
					return JSONOutput{}, nil
				}
				return JSONLeaf(input), nil
			}
			if boundary == "bytes" {
				state.budget.limits.BufferBytes = input.Len() - 1
			} else if boundary == "cancel" {
				state.ctx = &outputPlanCancelContext{Context: t.Context(), remaining: 4}
			}
			_, err := plan.evaluate(state)
			if err == nil {
				t.Fatal("merge lost its earlier operand failure", count, boundary)
			}
			switch boundary {
			case "source":
				if mappingErrorPath("", err) != "/mapping/items/2" || !strings.Contains(err.Error(), "required field is absent") || state.budget.steps != 4 {
					t.Fatal("field collision preceded source evaluation", count, err, state.budget.steps)
				}
			case "bytes":
				var limit *ResourceLimitError
				if !errors.As(err, &limit) || limit.Metric != "bytes" || state.budget.steps != 2 {
					t.Fatal("field materialization bypassed the source byte limit", count, err, state.budget.steps)
				}
			case "cancel":
				if !errors.Is(err, context.Canceled) || mappingErrorPath("", err) != "/mapping/items/2" || state.budget.steps != 4 {
					t.Fatal("field materialization changed cancellation precedence", count, err, state.budget.steps)
				}
			}
		}
	}
}

func TestCanonicalContainerFieldSemantics(t *testing.T) {
	for _, fields := range []map[string]JSONOutput{
		{"z": JSONLeaf(Value{raw: "9007199254740993"}), "a": JSONLeaf(StringValue("<&>😀"))},
		{"bad\xff": JSONLeaf(StringValue("first")), "bad�": JSONLeaf(StringValue("last"))},
	} {
		output := JSONObject(fields)
		wire, err := output.Encode(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		want, err := plannedObjectFields(JSONLeaf(wire))
		if err != nil {
			t.Fatal(err)
		}
		got, err := plannedObjectFields(output)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatal("constructed field view changed decoded keys or ordering", got, want, err)
		}
		for _, field := range want {
			selected, err := readOutputPointer(output, []string{field.key}, nil)
			if err != nil || selected != field.value {
				t.Fatal("constructed pointer changed last-wins semantics", field.key, err)
			}
		}
	}
}

func BenchmarkConstructedObjectFields(b *testing.B) {
	fields := make(map[string]JSONOutput, 32)
	for index := range 32 {
		fields[fmt.Sprintf("field%d", index)] = JSONLeaf(coreInteger(int64(index)))
	}
	for _, canonical := range []bool{false, true} {
		b.Run(fmt.Sprintf("canonical=%t", canonical), func(b *testing.B) {
			output := JSONObject(fields)
			output.node.hasCanonicalKeys = canonical
			b.ReportAllocs()
			for b.Loop() {
				selected, err := plannedObjectFields(output)
				if err != nil || len(selected) != len(fields) {
					b.Fatal("field count changed", err)
				}
			}
		})
	}
}
