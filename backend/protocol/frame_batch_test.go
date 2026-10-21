package protocol

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/bytedance/sonic"
)

func TestFrameBatchBoundaryParity(t *testing.T) {
	for _, frames := range [][]Value{nil, {}, {{}}, {parseTestValue(t, ` { "n":9007199254740993, "text":"<>&\u2028" } `)}, {StringValue("a"), StringValue("b")}, {StringValue(strings.Repeat("a", 65536))}} {
		for _, size := range []int{1, 2, 8, 64, 1 << 20} {
			for _, depth := range []int{1, 2, 8} {
				limits := DefaultLimits()
				limits.BufferBytes = size
				limits.Depth = depth
				encoded, wantErr := EncodeValue(frames)
				var want []Value
				if wantErr == nil {
					wantErr = checkResourceValue(encoded, limits, "mapping_output")
				}
				if wantErr == nil {
					want, wantErr = readArray(encoded)
				}
				copy := append([]Value(nil), frames...)
				if frames != nil && copy == nil {
					copy = []Value{}
				}
				got, gotErr := normalizeFrameBatch(copy, limits)
				if !reflect.DeepEqual(got, want) || fmt.Sprint(gotErr) != fmt.Sprint(wantErr) {
					t.Fatalf("batch parity: %v / %v", gotErr, wantErr)
				}
			}
		}
	}
}

func FuzzStringValueCodecParity(f *testing.F) {
	for _, text := range []string{"", "hello", "<>&", "quote\"\\\n", "中文", "\u2028\u2029", "\xff", strings.Repeat("x", 4096), strings.Repeat("x", 4097)} {
		f.Add(text)
	}
	f.Fuzz(func(t *testing.T, text string) {
		if len(text) > 65536 {
			t.Skip()
		}
		want, err := sonic.ConfigStd.MarshalToString(text)
		if err != nil || StringValue(text).raw != want {
			t.Fatal("scalar spelling changed", err)
		}
	})
}

func BenchmarkFrameScalar(b *testing.B) {
	for _, mode := range []string{"codec", "owned"} {
		b.Run(mode, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				var raw string
				if mode == "codec" {
					raw, _ = sonic.ConfigStd.MarshalToString("content_block_delta")
				} else {
					raw = StringValue("content_block_delta").raw
				}
				if len(raw) != 21 {
					b.Fatal(raw)
				}
			}
		})
	}
}
