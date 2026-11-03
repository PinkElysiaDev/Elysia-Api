package protocol

import (
	"fmt"
	"reflect"
	"testing"
)

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
