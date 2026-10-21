package protocol

import (
	"fmt"
	"strings"
	"testing"
)

// Identical fixtures compile against the previous source via a test-only overlay.
func BenchmarkAlgorithmDocumentIndex(b *testing.B) {
	for _, size := range []int{64 << 10, 1 << 20, 4 << 20} {
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			root, _ := ParseValue([]byte(strings.Repeat(`{"child":`, 8) + `"` + strings.Repeat("A", size) + `"` + strings.Repeat("}", 8)))
			b.ReportAllocs()
			b.SetBytes(int64(root.Len()))
			for b.Loop() {
				reader := requestValueReader(root)
				for range 4 {
					value := root
					for range 8 {
						fields, err := value.readObjectWithin(reader)
						if err != nil {
							b.Fatal(err)
						}
						value = fields["child"]
					}
					if value.Len() != size+2 {
						b.Fatal("wrong leaf")
					}
				}
			}
		})
	}
}

func BenchmarkAlgorithmMetricIdentity(b *testing.B) {
	for _, size := range []int{64 << 10, 1 << 20, 4 << 20} {
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			cache := &semanticCache{limits: Limits{BufferBytes: 64 << 20, StateItems: 16}}
			var value Value
			for i := range 12 {
				value = StringValue(strings.Repeat(string(rune('a'+i)), size))
				cache.store(value, semanticValueEntry{hasMetrics: true})
			}
			b.ReportAllocs()
			for b.Loop() {
				if entry, ok := cache.lookup(value); !ok || !entry.hasMetrics {
					b.Fatal("miss")
				}
			}
		})
	}
}
