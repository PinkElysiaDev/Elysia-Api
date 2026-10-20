package protocol

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestJSONOutputParityAndOwnership(t *testing.T) {
	for _, raw := range []string{`null`, `false`, `900719925474099312345`, `1e2`, ` { "n":9007199254740993,"x":"<>&\u2028" } `, `"escaped\\quote\""`, `"` + strings.Repeat("A", 1<<20) + `"`} {
		leaf := parseTestValue(t, raw)
		fields := map[string]JSONOutput{"payload": JSONLeaf(leaf), "<escaped\nkey>": JSONLeaf(StringValue("value"))}
		object := JSONObject(fields)
		fields["payload"] = JSONLeaf(StringValue("mutated"))
		items := []JSONOutput{object, JSONLeaf(parseTestValue(t, `null`))}
		plan := JSONArray(items)
		items[0] = JSONLeaf(StringValue("mutated"))
		got, err := plan.Encode(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		legacy, _ := EncodeValue(Object{"payload": leaf, "<escaped\nkey>": StringValue("value")})
		want, _ := EncodeValue([]Value{legacy, parseTestValue(t, `null`)})
		if got != want {
			t.Fatalf("deferred output differs: %s / %s", got.raw, want.raw)
		}
	}
	for _, plan := range []JSONOutput{{}, JSONArray([]JSONOutput{{}}), JSONObject(map[string]JSONOutput{"absent": {}})} {
		if value, err := plan.Encode(t.Context()); err == nil || !value.IsZero() {
			t.Fatal("absent child accepted")
		}
	}
	for _, tc := range []struct {
		plan JSONOutput
		want string
	}{{JSONArray(nil), "null"}, {JSONArray([]JSONOutput{}), "[]"}, {JSONObject(nil), "null"}, {JSONObject(map[string]JSONOutput{}), "{}"}} {
		got, err := tc.plan.Encode(t.Context())
		if err != nil || got.raw != tc.want {
			t.Fatalf("empty/null changed: %s / %s, %v", got.raw, tc.want, err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if value, err := JSONArray([]JSONOutput{JSONLeaf(StringValue("x"))}).Encode(ctx); !errors.Is(err, context.Canceled) || !value.IsZero() {
		t.Fatal("cancelled output materialized", err)
	}
}

func FuzzJSONOutputParity(f *testing.F) {
	for _, raw := range []string{`null`, ` { "n":9007199254740993 } `, `"<>&\u2028"`, `[null,false,1e3]`} {
		f.Add(raw, "key", uint8(3))
	}
	f.Fuzz(func(t *testing.T, raw, key string, depth uint8) {
		if len(raw)+len(key) > 65536 {
			t.Skip()
		}
		leaf, err := ParseValue([]byte(raw))
		if err != nil {
			t.Skip()
		}
		plan, legacy := JSONLeaf(leaf), leaf
		for range int(depth%16) + 1 {
			plan = JSONObject(map[string]JSONOutput{key: JSONArray([]JSONOutput{plan})})
			array, err := EncodeValue([]Value{legacy})
			if err != nil {
				t.Fatal(err)
			}
			legacy, err = EncodeValue(Object{key: array})
			if err != nil {
				t.Fatal(err)
			}
		}
		got, err := plan.Encode(t.Context())
		if err != nil || got != legacy {
			t.Fatalf("output parity: %v", err)
		}
	})
}

func TestDeferredNativeReconciliationLargeNested(t *testing.T) {
	for _, size := range []int{8, 128 << 10} {
		payload := StringValue(strings.Repeat("A", size))
		original, _ := EncodeValue(Object{"payload": payload, "n": parseTestValue(t, `1`), "vendor": parseTestValue(t, `9007199254740993`)})
		before, _ := EncodeValue(Object{"payload": payload, "n": parseTestValue(t, `1`)})
		after, _ := EncodeValue(Object{"payload": payload, "n": parseTestValue(t, `2`)})
		want, _ := EncodeValue(Object{"payload": payload, "n": parseTestValue(t, `2`), "vendor": parseTestValue(t, `9007199254740993`)})
		for range 8 {
			original, _ = EncodeValue(Object{"nested": original})
			before, _ = EncodeValue(Object{"nested": before})
			after, _ = EncodeValue(Object{"nested": after})
			want, _ = EncodeValue(Object{"nested": want})
		}
		got, err := reconcileNative(t.Context(), original, before, after, NativePolicy{}, DefaultLimits())
		if err != nil || got != want {
			t.Fatalf("native nested output changed (size=%d): %v", size, err)
		}
		limits := DefaultLimits()
		limits.BufferBytes = 16
		if _, err := reconcileNative(t.Context(), original, before, after, NativePolicy{}, limits); err == nil {
			t.Fatal("deferred native output bypassed final resource budget")
		}
	}
}

func BenchmarkNestedJSONOutput(b *testing.B) {
	for _, size := range []int{64 << 10, 2 << 20} {
		for _, depth := range []int{2, 8} {
			leaf := StringValue(strings.Repeat("A", size))
			for _, mode := range []string{"legacy", "deferred"} {
				b.Run(fmt.Sprintf("%d/%d/%s", size, depth, mode), func(b *testing.B) {
					b.ReportAllocs()
					for b.Loop() {
						if mode == "legacy" {
							value := leaf
							for range depth {
								array, _ := EncodeValue([]Value{value})
								value, _ = EncodeValue(Object{"items": array})
							}
							if value.Len() < size {
								b.Fatal("payload lost")
							}
						} else {
							plan := JSONLeaf(leaf)
							for range depth {
								plan = JSONObject(map[string]JSONOutput{"items": JSONArray([]JSONOutput{plan})})
							}
							if value, err := plan.Encode(b.Context()); err != nil || value.Len() < size {
								b.Fatal("payload lost", err)
							}
						}
					}
				})
			}
		}
	}
}
