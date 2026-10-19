package protocol

import (
	"encoding/json"
	"strings"
	"testing"
)

// Investigation-only prototype. It is never called by production code.
// Both implementations accept an already validated JSON string token.
func investigationStringScan(raw string) (end, encodedBytes int) {
	encodedBytes = 1
	for index := 1; index < len(raw); index++ {
		encodedBytes++
		switch raw[index] {
		case '"':
			return index + 1, encodedBytes
		case '\\':
			index++
			encodedBytes++
		case '<', '>', '&':
			encodedBytes += 5
		case 0xe2:
			if index+2 < len(raw) && raw[index+1] == 0x80 && (raw[index+2] == 0xa8 || raw[index+2] == 0xa9) {
				encodedBytes += 5
				index += 2
			}
		}
	}
	return len(raw), encodedBytes
}

func TestInvestigationStringScan(t *testing.T) {
	for _, raw := range []string{`""`, `"abc"`, `"a\\b\"c"`, `"\u0061"`, `"<>&"`, `"\u2028"`, "\"\u2028\u2029\"", `"汉字"`, string([]byte{'"', 0xff, '"'})} {
		if !json.Valid([]byte(raw)) {
			t.Fatal("invalid fixture")
		}
		meter := typedMeter{limits: DefaultLimits()}
		if !meter.scanRaw(raw) {
			t.Fatal("reference rejected")
		}
		end, size := investigationStringScan(raw)
		if end != len(raw) || size != meter.bytes {
			t.Fatalf("%q: end=%d bytes=%d reference=%d", raw, end, size, meter.bytes)
		}
	}
}

func FuzzInvestigationStringScan(f *testing.F) {
	for _, raw := range []string{`"plain"`, `"a\\b\"<>&"`, "\"\u2028\u2029\"", `"\u0061"`} {
		f.Add(raw)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		if len(raw) < 2 || len(raw) > 65536 || raw[0] != '"' || raw[len(raw)-1] != '"' || !json.Valid([]byte(raw)) {
			t.Skip()
		}
		meter := typedMeter{limits: DefaultLimits()}
		if !meter.scanRaw(raw) {
			t.Fatal("reference rejected")
		}
		end, size := investigationStringScan(raw)
		if end != len(raw) || size != meter.bytes {
			t.Fatalf("scan differs: end=%d bytes=%d reference=%d", end, size, meter.bytes)
		}
	})
}

func BenchmarkInvestigationStringScan(b *testing.B) {
	for _, fixture := range []struct{ name, raw string }{{"small", `"` + strings.Repeat("A", 256) + `"`}, {"2MiB", `"` + strings.Repeat("A", 2<<20) + `"`}, {"escaped", `"` + strings.Repeat(`a\\b\"<>&\u2028`, 1000) + `"`}} {
		b.Run(fixture.name, func(b *testing.B) {
			b.Run("current", func(b *testing.B) {
				b.SetBytes(int64(len(fixture.raw)))
				for b.Loop() {
					meter := typedMeter{limits: Limits{BufferBytes: 32 << 20, Nodes: 100000, Depth: 64}}
					if !meter.scanRaw(fixture.raw) {
						b.Fatal("limit")
					}
				}
			})
			b.Run("onePassPrototype", func(b *testing.B) {
				b.SetBytes(int64(len(fixture.raw)))
				for b.Loop() {
					end, size := investigationStringScan(fixture.raw)
					if end != len(fixture.raw) || size < len(fixture.raw) {
						b.Fatal("scan")
					}
				}
			})
		})
	}
}

func BenchmarkInvestigationCacheLookup(b *testing.B) {
	for _, size := range []int{256, 2 << 20} {
		name := "small"
		if size > 256 {
			name = "2MiB"
		}
		b.Run(name, func(b *testing.B) {
			// Multiple entries prevent the single-entry string-map shortcut from
			// disguising key hashing costs present in real semantic caches.
			key := StringValue(strings.Repeat("A", size))
			entries := map[Value]int{key: 1}
			for index := 0; index < 32; index++ {
				entries[StringValue(strings.Repeat(string(rune('a'+index)), size))] = index + 2
			}
			b.SetBytes(int64(size))
			b.ResetTimer()
			for b.Loop() {
				if entries[key] != 1 {
					b.Fatal("cache miss")
				}
			}
		})
	}
}
