package protocol

import (
	"strings"
	"testing"
)

func TestRawStringMeterBoundaries(t *testing.T) {
	for _, size := range []int{0, 1, 7, 8, 31, 32, 33, 4096} {
		for _, suffix := range []string{"", "<>&", "\u2028\u2029", "😀", "\xe2", "\xe2\x80", "\xe2\x80\xa8", "\xff", `\u2028`, `\\\"`} {
			raw := `"` + strings.Repeat("A", size) + suffix + `"`
			value, err := ParseValue([]byte(raw))
			if err != nil {
				t.Fatal(err)
			}
			encoded, err := EncodeValue(value)
			if err != nil {
				t.Fatal(err)
			}
			for _, delta := range []int{-1, 0, 1} {
				meter := typedMeter{limits: Limits{BufferBytes: encoded.Len() + delta, Nodes: 8, Depth: 8}}
				fits := meter.scanRaw(value.raw)
				if fits != (delta >= 0) || delta >= 0 && meter.bytes != encoded.Len() {
					t.Fatalf("size=%d suffix=%q delta=%d: bytes=%d fits=%v want=%d", size, suffix, delta, meter.bytes, fits, encoded.Len())
				}
				if delta >= 0 && compactValueString(value.raw) != encoded.raw {
					t.Fatalf("normalization differs: %q", raw)
				}
			}
		}
	}
}
