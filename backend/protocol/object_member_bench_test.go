package protocol

import (
	"fmt"
	"strings"
	"testing"
)

func BenchmarkObjectMember(b *testing.B) {
	for _, size := range []int{64, 512, 2048, 65536} {
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			input, err := ParseValue([]byte(`{"unused":"` + strings.Repeat("x", size) + `","selected":1,"\u0073elected":9007199254740993}`))
			if err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			for b.Loop() {
				value, err := readValuePointer(input, []string{"selected"})
				if err != nil || value.JSONString() != "9007199254740993" {
					b.Fatal("object lookup changed duplicate or exact-number semantics", value, err)
				}
			}
		})
	}
}
