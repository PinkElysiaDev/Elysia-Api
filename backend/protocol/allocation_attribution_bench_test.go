package protocol

import (
	"fmt"
	"strings"
	"testing"
)

// Diagnostic shapes isolate existing allocation boundaries. The borrowed case
// consumes all fields immediately; it is not a replacement for retained reads.
func BenchmarkAllocationFieldViews(b *testing.B) {
	for _, count := range []int{4, 16} {
		fields := Object{}
		for index := range count {
			fields[fmt.Sprintf("f%d", index)] = StringValue("small😀")
		}
		input, err := EncodeValue(fields)
		if err != nil {
			b.Fatal(err)
		}
		for _, borrowed := range []bool{false, true} {
			b.Run(fmt.Sprintf("fields-%d/borrowed-%t", count, borrowed), func(b *testing.B) {
				var reader *valueReader
				if borrowed {
					reader = &valueReader{isBorrowed: true}
				}
				b.ReportAllocs()
				for b.Loop() {
					got, err := input.readObjectFieldsWithin(reader, true)
					if err != nil || len(got) != count || got["f0"].raw != fields["f0"].raw {
						b.Fatal("field view changed content", err)
					}
				}
			})
		}
	}
}

func BenchmarkAllocationCoreProjection(b *testing.B) {
	text := StringValue("small😀")
	node := Node{Kind: TextNode, Payload: text}
	for _, field := range []string{"", "payload"} {
		name := field
		var pointer []string
		if field == "" {
			name = "whole-node"
		} else {
			pointer = []string{field}
		}
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				output, err := readCoreNode(node, pointer, nil)
				if err != nil || output.IsZero() {
					b.Fatal("core projection failed", err)
				}
				if field == "" && len(output.node.fields) != 2 {
					b.Fatal("core projection lost fields")
				}
			}
		})
	}
}

func BenchmarkAllocationEncodingParts(b *testing.B) {
	for _, count := range []int{8, 32, 128} {
		items := make([]JSONOutput, count)
		for index := range items {
			items[index] = JSONLeaf(StringValue(strings.Repeat("x", 16)))
		}
		output := JSONArray(items)
		b.Run(fmt.Sprintf("items-%d", count), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				value, err := output.Encode(b.Context())
				if err != nil || value.Len() != count*19+1 {
					b.Fatal("encoded content length changed", err)
				}
			}
		})
	}
}
