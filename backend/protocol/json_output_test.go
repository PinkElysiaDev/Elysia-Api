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

func TestJSONOutputFragmentWorkspaceBoundaries(t *testing.T) {
	for _, count := range []int{0, 1, 63, 64, 65, 129} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			var parts jsonOutputParts
			var expected strings.Builder
			for index := range count {
				fragment := strings.Repeat("界", index%3)
				if err := parts.add(fragment); err != nil {
					t.Fatal(err)
				}
				expected.WriteString(fragment)
			}
			ctx := &jsonOutputCheckContext{Context: t.Context()}
			encoded, err := parts.encode(ctx)
			if err != nil || encoded.raw != expected.String() || parts.bytes != expected.Len() || ctx.checks != count+1 {
				t.Fatal("fragment order, empty fragments or cancellation checks changed", err, ctx.checks)
			}
			if err := parts.add("later"); err != nil {
				t.Fatal(err)
			}
			if encoded.raw != expected.String() {
				t.Fatal("encoded bytes alias the mutable workspace")
			}
		})
	}
	var parts jsonOutputParts
	parts.bytes = int(^uint(0)>>1) - 1
	if err := parts.add("x"); err != nil {
		t.Fatal("last representable byte rejected", err)
	}
	if err := parts.add(""); err != nil {
		t.Fatal("empty fragment rejected at integer capacity", err)
	}
	if err := parts.add("x"); err == nil || err.Error() != "JSON output exceeds integer capacity" || parts.bytes != int(^uint(0)>>1) {
		t.Fatal("integer capacity failure changed", err, parts.bytes)
	}
}

func TestJSONOutputFragmentBoundaryOwnership(t *testing.T) {
	for _, fixture := range []struct {
		items        int
		isFirstEmpty bool
	}{{31, false}, {31, true}, {32, false}, {128, false}} {
		plan, values := jsonOutputArrayFixture(fixture.items, fixture.isFirstEmpty)
		want, err := EncodeValue(values)
		if err != nil {
			t.Fatal(err)
		}
		got, err := plan.Encode(t.Context())
		if err != nil || got != want {
			t.Fatal("fragment boundary differs from the value encoder", fixture, got, want, err)
		}
		for _, pair := range [][2]JSONOutput{{plan, JSONLeaf(want)}, {JSONLeaf(want), plan}, {plan, plan}} {
			isEqual, err := equalJSONOutputBytes(t.Context(), pair[0], pair[1])
			if err != nil || !isEqual {
				t.Fatal("different fragment boundaries changed byte equality", fixture, err)
			}
		}
		changed := parseTestValue(t, strings.Replace(want.raw, "1", "2", 1))
		isEqual, err := equalJSONOutputBytes(t.Context(), plan, JSONLeaf(changed))
		if err != nil || isEqual {
			t.Fatal("equal-size differing output compared equal", fixture, err)
		}
	}
	payload := StringValue(strings.Repeat("A", 2<<20))
	plan := JSONArray([]JSONOutput{JSONLeaf(payload)})
	encoded, err := plan.Encode(t.Context())
	if err != nil || sharesValueStorage(payload, encoded) {
		t.Fatal("container output did not own its final bytes", err)
	}
	selected, err := readValuePointer(encoded, []string{"0"})
	if err != nil || selected != payload {
		t.Fatal("owned output lost its large leaf", err)
	}
}

func TestJSONOutputFragmentCancellationBoundaries(t *testing.T) {
	for _, fixture := range []struct {
		name             string
		items            int
		isFirstEmpty     bool
		encodeChecks     int
		comparisonChecks int
	}{{"63 fragments", 31, false, 97, 99}, {"64 fragments", 31, true, 98, 100}, {"65 fragments", 32, false, 100, 102}} {
		plan, values := jsonOutputArrayFixture(fixture.items, fixture.isFirstEmpty)
		wire, err := EncodeValue(values)
		if err != nil {
			t.Fatal(err)
		}
		t.Run(fixture.name, func(t *testing.T) {
			checkJSONOutputCancellation(t, fixture.encodeChecks, func(ctx context.Context) error {
				_, err := plan.Encode(ctx)
				return err
			})
			checkJSONOutputCancellation(t, fixture.comparisonChecks, func(ctx context.Context) error {
				_, err := equalJSONOutputBytes(ctx, plan, JSONLeaf(wire))
				return err
			})
		})
	}
	plan := JSONObject(map[string]JSONOutput{"": JSONLeaf(Value{raw: "1"})})
	checkJSONOutputCancellation(t, 10, func(ctx context.Context) error {
		_, err := plan.Encode(ctx)
		return err
	})
	checkJSONOutputCancellation(t, 14, func(ctx context.Context) error {
		_, err := equalJSONOutputBytes(ctx, plan, plan)
		return err
	})
}

type jsonOutputCheckContext struct {
	context.Context
	checks   int
	cancelAt int
}

func (ctx *jsonOutputCheckContext) Err() error {
	ctx.checks++
	if ctx.cancelAt != 0 && ctx.checks >= ctx.cancelAt {
		return context.Canceled
	}
	return ctx.Context.Err()
}

func checkJSONOutputCancellation(t *testing.T, checks int, run func(context.Context) error) {
	t.Helper()
	ctx := &jsonOutputCheckContext{Context: t.Context()}
	if err := run(ctx); err != nil || ctx.checks != checks {
		t.Fatal("successful context-check sequence changed", err, ctx.checks, checks)
	}
	for cancelAt := 1; cancelAt <= checks; cancelAt++ {
		ctx := &jsonOutputCheckContext{Context: t.Context(), cancelAt: cancelAt}
		if err := run(ctx); !errors.Is(err, context.Canceled) || ctx.checks != cancelAt {
			t.Fatal("cancellation boundary changed", cancelAt, ctx.checks, err)
		}
	}
}

func jsonOutputArrayFixture(count int, isFirstEmpty bool) (JSONOutput, []Value) {
	items, values := make([]JSONOutput, count), make([]Value, count)
	for index := range count {
		values[index] = Value{raw: "1"}
		items[index] = JSONLeaf(values[index])
	}
	if isFirstEmpty {
		values[0] = Value{raw: "[]"}
		items[0] = JSONArray([]JSONOutput{})
	}
	return JSONArray(items), values
}
