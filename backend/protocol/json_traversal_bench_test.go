package protocol

import (
	"fmt"
	"strings"
	"testing"
)

var traversalObjectSink Object
var traversalArraySink []Value
var traversalValueSink Value

func BenchmarkJSONTraversal(b *testing.B) {
	var fields []string
	for i := range 128 {
		fields = append(fields, fmt.Sprintf(`"k%d":{"value":%d}`, i, i))
	}
	wide := "{" + strings.Join(fields, ",") + "}"
	large := `{"metadata":{"id":"small"},"payload":"` + strings.Repeat("A", 1<<20) + `","tail":{"value":9007199254740993123}}`
	for _, tc := range []struct {
		name, raw string
		mode      string
		pointer   []string
	}{
		{"object-small", `{"id":1,"flag":false,"null":null,"nested":{"a":[1,2]}}`, "object", nil},
		{"object-wide", wide, "object", nil},
		{"object-large-owned", large, "object", nil},
		{"object-large-view", large, "view", nil},
		{"array", `[` + strings.Repeat(`{"value":9007199254740993123},`, 127) + `null]`, "array", nil},
		{"pointer-small", `{"id":1,"flag":false,"null":null,"nested":{"a":[1,2]}}`, "pointer", []string{"nested", "a", "1"}},
		{"pointer-wide", wide, "pointer", []string{"k127", "value"}},
		{"pointer-large-head", large, "pointer", []string{"metadata", "id"}},
		{"pointer-large-tail", large, "pointer", []string{"tail", "value"}},
		{"pointer-array", `{"items":[` + strings.Repeat(`{"value":false},`, 127) + `{"value":0}]}`, "pointer", []string{"items", "127", "value"}},
	} {
		b.Run(tc.name, func(b *testing.B) {
			value, err := ParseValue([]byte(tc.raw))
			if err != nil {
				b.Fatal(err)
			}
			reader := requestValueReader(value)
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				switch tc.mode {
				case "object":
					traversalObjectSink, err = value.ReadObject()
				case "view":
					traversalObjectSink, err = value.readObjectWithin(reader)
				case "array":
					traversalArraySink, err = value.readArray()
				case "pointer":
					traversalValueSink, err = readValuePointer(value, tc.pointer)
				}
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
