package protocol

import (
	"fmt"
	"testing"
)

// Exercise the unchanged consumer boundary, so this fixture also compiles
// against a parent with map-backed field tables.
func BenchmarkScopedFieldReads(b *testing.B) {
	for _, count := range []int{1, 4, 8, 9, 32, 64} {
		fields := Object{}
		paths := make([][]string, count)
		values := make([]Value, count)
		for index := range count {
			key := fmt.Sprintf("f%d", index)
			paths[index] = []string{key}
			values[index] = StringValue(fmt.Sprintf("value%d😀", index))
			fields[key] = values[index]
		}
		input, err := EncodeValue(fields)
		if err != nil {
			b.Fatal(err)
		}
		for _, borrowed := range []bool{false, true} {
			b.Run(fmt.Sprintf("fields-%d/borrowed-%t", count, borrowed), func(b *testing.B) {
				valuesReader := requestValueReader(input)
				if borrowed {
					valuesReader = &valueReader{isBorrowed: true}
				}
				b.ReportAllocs()
				for b.Loop() {
					objects := newPlanObjectReader(input, objectReuseReads)
					for range 2 {
						for index, path := range paths {
							value, err := readValuePointerWithObjectReader(input, path, valuesReader, objects)
							if err != nil || value != values[index] {
								b.Fatal("scoped read changed value", err)
							}
						}
					}
					objects.releaseItem(nil)
				}
			})
		}
	}
}
