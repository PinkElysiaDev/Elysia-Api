package protocol

import (
	"fmt"
	"strings"
	"testing"
)

// Run the identical object-only benchmark on the baseline and candidate to
// expose nested MarshalJSON copying independently of compact-source savings.
func BenchmarkExpressionAuthoringEncoding(b *testing.B) {
	const payloadBytes = 64 << 10
	for _, depth := range []int{1, 8, 32, 60} {
		b.Run(fmt.Sprintf("depth-%d", depth), func(b *testing.B) {
			expression := Expression{Op: "literal", Value: StringValue(strings.Repeat("p", payloadBytes))}
			for range depth {
				expression = Expression{Op: "object", Fields: map[string]Expression{"nested": expression}}
			}
			b.ReportAllocs()
			b.SetBytes(payloadBytes)
			b.ResetTimer()
			for b.Loop() {
				encoded, err := EncodeValue(expression)
				if err != nil || encoded.Len() < payloadBytes {
					b.Fatal("authoring encoding lost the literal", err)
				}
			}
		})
	}
}
