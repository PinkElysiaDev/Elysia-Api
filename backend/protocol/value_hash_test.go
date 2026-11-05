package protocol

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

func TestValueHashKeepsExactBytesAcrossChunks(t *testing.T) {
	for _, raw := range []string{
		"", ` { "number" : 1e+3, "escaped":"\u003c" } `,
		`"` + strings.Repeat("x", valueHashChunkBytes-2) + `"`,
		`{"payload":"` + strings.Repeat("x", 2*valueHashChunkBytes) + `","number":1.0}`,
	} {
		sum := sha256.Sum256([]byte(raw))
		if got := hashValue(Value{raw: raw}); got != hex.EncodeToString(sum[:]) {
			t.Fatal("hash changed original bytes at length", len(raw))
		}
	}
}

func BenchmarkValueHashLarge(b *testing.B) {
	value := StringValue(strings.Repeat("x", 2<<20))
	for _, chunked := range []bool{false, true} {
		name := "byte-copy"
		if chunked {
			name = "chunks"
		}
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if chunked {
					_ = hashValue(value)
				} else {
					sum := sha256.Sum256(value.Bytes())
					_ = hex.EncodeToString(sum[:])
				}
			}
		})
	}
}
