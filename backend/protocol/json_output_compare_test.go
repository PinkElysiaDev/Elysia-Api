package protocol

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestJSONOutputByteComparison(t *testing.T) {
	leaf := func(raw string) JSONOutput { return JSONLeaf(parseTestValue(t, raw)) }
	object := func(raw string) JSONOutput {
		return JSONObject(map[string]JSONOutput{"": leaf(raw)})
	}
	for _, tc := range []struct {
		name        string
		left, right JSONOutput
		want        bool
	}{
		{"root whitespace", leaf(` { "x":1 } `), leaf(`{"x":1}`), false},
		{"root key order", leaf(`{"b":2,"a":1}`), leaf(`{"a":1,"b":2}`), false},
		{"root duplicate keys", leaf(`{"a":1,"a":2}`), leaf(`{"a":2}`), false},
		{"root number spelling", leaf(`1`), leaf(`1.0`), false},
		{"root string spelling", leaf(`"a"`), leaf(`"\u0061"`), false},
		{"root HTML spelling", leaf(`"<"`), leaf(`"\u003c"`), false},
		{"child whitespace", object(` { "x":1 } `), object(`{"x":1}`), true},
		{"child HTML escaping", object(`"<>&"`), object(`"\u003c\u003e\u0026"`), true},
		{"child number spelling", object(`1`), object(`1.0`), false},
		{"different segment boundaries", object(` [ 1, false ] `), leaf(`{"": [1,false]}`), false},
		{"raw and constructed roots", object(` [ 1, false ] `), leaf(`{"":[1,false]}`), true},
		{"empty object", JSONObject(map[string]JSONOutput{}), leaf(`{}`), true},
		{"empty array", JSONArray([]JSONOutput{}), leaf(`[]`), true},
		{"nil object", JSONObject(nil), leaf(`null`), true},
		{"nil array", JSONArray(nil), leaf(`null`), true},
		{"null versus empty object", JSONObject(nil), JSONObject(map[string]JSONOutput{}), false},
		{"null versus empty array", JSONArray(nil), JSONArray([]JSONOutput{}), false},
		{"replacement key collision", JSONObject(map[string]JSONOutput{"\xfe": leaf(`2`), "\xff": leaf(`1`)}), leaf(`{"\ufffd":2,"\ufffd":1}`), true},
		{"replacement keys stay distinct", JSONObject(map[string]JSONOutput{"\xfe": leaf(`2`), "\xff": leaf(`1`)}), JSONObject(map[string]JSONOutput{"\ufffd": leaf(`1`)}), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			left, err := tc.left.Encode(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			right, err := tc.right.Encode(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if want := left.raw == right.raw; want != tc.want {
				t.Fatalf("encoding contract differs: %q / %q", left.raw, right.raw)
			}
			for _, pair := range [][2]JSONOutput{{tc.left, tc.right}, {tc.right, tc.left}} {
				got, err := equalJSONOutputBytes(t.Context(), pair[0], pair[1])
				if err != nil || got != tc.want {
					t.Fatalf("comparison = %v, %v; want %v", got, err, tc.want)
				}
			}
		})
	}
}

func TestJSONOutputByteComparisonRejectsAbsentValues(t *testing.T) {
	valid := JSONLeaf(StringValue("valid"))
	for _, malformed := range []JSONOutput{{}, JSONArray([]JSONOutput{{}}), JSONObject(map[string]JSONOutput{"missing": {}})} {
		_, want := malformed.Encode(t.Context())
		if want == nil {
			t.Fatal("absent value encoded")
		}
		for _, pair := range [][2]JSONOutput{{malformed, valid}, {valid, malformed}, {malformed, malformed}} {
			got, err := equalJSONOutputBytes(t.Context(), pair[0], pair[1])
			if got || err == nil || err.Error() != want.Error() || errors.Unwrap(err) != errors.Unwrap(want) {
				t.Fatalf("absent value comparison = %v, %v; want %v", got, err, want)
			}
		}
	}
}

func TestJSONOutputByteComparisonCancellation(t *testing.T) {
	cause := errors.New("comparison canceled by caller")
	canceled, cancel := context.WithCancelCause(t.Context())
	cancel(cause)
	expired, stop := context.WithDeadlineCause(t.Context(), time.Now().Add(-time.Second), cause)
	defer stop()
	plan := JSONObject(map[string]JSONOutput{"value": JSONLeaf(StringValue("body"))})
	for _, ctx := range []context.Context{canceled, expired} {
		_, want := plan.Encode(ctx)
		for _, left := range []JSONOutput{plan, {}} {
			got, err := equalJSONOutputBytes(ctx, left, plan)
			if got || err != want || !errors.Is(err, ctx.Err()) || errors.Is(err, cause) {
				t.Fatalf("cancellation = %v, %v; want context error %v", got, err, want)
			}
		}
	}
}

func TestJSONOutputByteComparisonBorrowsPayload(t *testing.T) {
	payload := StringValue(strings.Repeat("A", 2<<20))
	plan := JSONObject(map[string]JSONOutput{"payload": JSONLeaf(payload)})
	wire, err := plan.Encode(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var storage [64]string
	parts := jsonOutputParts{parts: storage[:0]}
	if err := parts.prepare(t.Context(), plan); err != nil {
		t.Fatal(err)
	}
	if parts.bytes != len(wire.raw) {
		t.Fatalf("prepared length = %d; want %d", parts.bytes, len(wire.raw))
	}
	hasPayload := false
	for _, part := range parts.parts {
		if part == payload.raw {
			hasPayload = true
			if !sharesValueStorage(payload, Value{raw: part}) {
				t.Fatal("fragment preparation copied the payload")
			}
		}
	}
	if !hasPayload {
		t.Fatal("payload was materialized into another container")
	}
	allocations := testing.AllocsPerRun(5, func() {
		got, err := equalJSONOutputBytes(t.Context(), plan, JSONLeaf(wire))
		if err != nil || !got {
			t.Fatalf("large comparison = %v, %v", got, err)
		}
	})
	// The collector may allocate the two fragment descriptor arrays, but must
	// not allocate another result or any payload-sized intermediate buffer.
	if allocations > 2 {
		t.Fatalf("comparison allocated beyond its fragment arrays: %g", allocations)
	}
}

func FuzzJSONOutputByteComparison(f *testing.F) {
	for _, raw := range []string{`null`, ` { "n":9007199254740993 } `, `"<>&\u2028"`, `[null,false,1e3]`} {
		f.Add(raw, "", uint8(2))
	}
	f.Fuzz(func(t *testing.T, raw, key string, depth uint8) {
		if len(raw)+len(key) > 65536 {
			t.Skip()
		}
		value, err := ParseValue([]byte(raw))
		if err != nil {
			t.Skip()
		}
		plan := JSONLeaf(value)
		for range int(depth % 8) {
			plan = JSONObject(map[string]JSONOutput{key: JSONArray([]JSONOutput{plan})})
		}
		wire, err := plan.Encode(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		for _, other := range []JSONOutput{JSONLeaf(wire), JSONLeaf(value), JSONArray([]JSONOutput{plan})} {
			encoded, err := other.Encode(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			got, err := equalJSONOutputBytes(t.Context(), plan, other)
			if want := wire.raw == encoded.raw; err != nil || got != want {
				t.Fatalf("comparison = %v, %v; want %v", got, err, want)
			}
		}
	})
}
