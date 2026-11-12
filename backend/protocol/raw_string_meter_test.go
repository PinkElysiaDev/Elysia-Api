package protocol

import (
	"strings"
	"testing"
)

func BenchmarkRawStringMeter(b *testing.B) {
	for _, fixture := range []struct{ name, raw string }{
		{"short", `"hello"`},
		{"image2", `"` + strings.Repeat("A", 2<<20) + `"`},
		{"image8", `"` + strings.Repeat("A", 8<<20) + `"`},
		{"image31", `"` + strings.Repeat("A", 31<<20) + `"`},
		{"html", `"` + strings.Repeat("<>&", 2048) + `"`},
		{"unicode", `"` + strings.Repeat("😀\u2028\u2029", 2048) + `"`},
		{"escaped", `"` + strings.Repeat(`\\\u003c`, 2048) + `"`},
		{"invalid", "\"" + strings.Repeat("\xff", 2048) + "\""},
	} {
		b.Run(fixture.name, func(b *testing.B) {
			value, err := ParseValue([]byte(fixture.raw))
			if err != nil {
				b.Fatal(err)
			}
			encoded, err := EncodeValue(value)
			if err != nil {
				b.Fatal(err)
			}
			limits := Limits{BufferBytes: 128 << 20, Nodes: 1 << 20, Depth: 64}
			b.ReportAllocs()
			b.SetBytes(int64(value.Len()))
			for b.Loop() {
				meter := typedMeter{limits: limits}
				if !meter.scanRaw(value.raw) || meter.bytes != encoded.Len() {
					b.Fatal("meter differs from codec", meter.bytes, encoded.Len())
				}
			}
		})
	}
}
