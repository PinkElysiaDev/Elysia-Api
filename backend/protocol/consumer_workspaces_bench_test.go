package protocol

import (
	"fmt"
	"slices"
	"testing"
)

func BenchmarkConsumerOperands(b *testing.B) {
	for _, count := range []int{2, 3, 8} {
		for _, operation := range []string{"integer", "join", "merge_usage"} {
			items := make([]Expression, count)
			input := make([]Value, count)
			for index := range items {
				items[index] = readExpression("input", fmt.Sprintf("/%d", index))
				switch operation {
				case "integer":
					input[index] = coreInteger(int64(index))
				case "join":
					input[index] = StringValue("hello😀")
				case "merge_usage":
					input[index] = Value{raw: `{"input":{"count":1,"origin":"observed"}}`}
				}
			}
			value, err := EncodeValue(input)
			if err != nil {
				b.Fatal(err)
			}
			expression := Expression{Op: operation, Items: items}
			if operation == "integer" {
				expression.Operation = "add"
			}
			b.Run(fmt.Sprintf("%s/%d", operation, count), func(b *testing.B) {
				benchmarkScopedConsumer(b, expression, value)
			})
		}
	}
}

func BenchmarkConsumerWireArrays(b *testing.B) {
	for _, count := range []int{0, 1, 8, 32, 256} {
		for _, kind := range []JSONType{NumberType, StringType, ObjectType} {
			schema := ValueSchema{Type: kind, AllowUnknown: kind == ObjectType}
			member := Value{raw: `9007199254740993`}
			switch kind {
			case StringType:
				member = Value{raw: `"hello\uD83D\uDE00"`}
			case ObjectType:
				member = Value{raw: `{"nested":[1,"😀",null],"a":0,"\u0061":1}`}
			}
			members := make([]Value, count)
			for index := range members {
				members[index] = member
			}
			value, err := EncodeValue(members)
			if err != nil {
				b.Fatal(err)
			}
			plan, err := compileWireSchemaPlan(&ValueSchema{Type: ArrayType, Items: &schema}, DefaultLimits())
			if err != nil {
				b.Fatal(err)
			}
			b.Run(fmt.Sprintf("%s/%d", kind, count), func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					if err := plan.validateWithin(b.Context(), value, "/wire", DefaultLimits(), newInputView(value)); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}

func BenchmarkConsumerConstructedFields(b *testing.B) {
	for _, count := range []int{1, 4, 8, 32, 64} {
		fields := make(map[string]JSONOutput, count)
		for index := range count {
			fields[fmt.Sprintf("field%03d", index)] = JSONLeaf(coreInteger(int64(index)))
		}
		for _, isSorted := range []bool{true, false} {
			output := JSONObject(fields)
			if !isSorted {
				slices.Reverse(output.node.fields)
				output.node.hasSortedFields = false
			}
			for _, position := range []string{"first", "middle", "last", "absent"} {
				index := map[string]int{"first": 0, "middle": count / 2, "last": count - 1, "absent": count}[position]
				pointer := []string{fmt.Sprintf("field%03d", index)}
				b.Run(fmt.Sprintf("%d/sorted-%t/%s", count, isSorted, position), func(b *testing.B) {
					b.ReportAllocs()
					for b.Loop() {
						value, err := readOutputPointer(output, pointer, nil)
						if err != nil || value.IsZero() != (position == "absent") {
							b.Fatal("field selection changed", err)
						}
					}
				})
			}
		}
	}
}
