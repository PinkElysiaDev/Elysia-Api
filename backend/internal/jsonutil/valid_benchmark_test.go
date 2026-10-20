package jsonutil

import (
	"strings"
	"testing"
)

func BenchmarkStrictJSONValidation(b *testing.B) {
	data := []byte(`{"payload":"` + strings.Repeat("A", 2<<20) + `","number":1e9999}`)
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for b.Loop() {
		if !Valid(data) {
			b.Fatal("valid input rejected")
		}
	}
}
