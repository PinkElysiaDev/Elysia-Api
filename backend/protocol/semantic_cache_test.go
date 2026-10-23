package protocol

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestSemanticCachePreservesResourceCounts(t *testing.T) {
	for _, raw := range []string{`"` + strings.Repeat("a", 1024) + `"`, ` { "x" : "` + strings.Repeat("<>&\u2028", 80) + `", "n":9007199254740993 } `, `[` + strings.Repeat(`{"x":null},`, 60) + `{}]`} {
		value, err := ParseValue([]byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		request := &Request{Content: []Node{{Kind: TextNode, Payload: value}, {Kind: MessageNode, Children: []Node{{Kind: TextNode, Payload: value}}}}}
		for _, metric := range []string{"bytes", "nodes", "depth"} {
			for _, limit := range []int{1, 2, 3, 8, 64, 256, 1024, 4096, 16384} {
				limits := DefaultLimits()
				switch metric {
				case "bytes":
					limits.BufferBytes = limit
				case "nodes":
					limits.Nodes = limit
				case "depth":
					limits.Depth = limit
				}
				cache := &semanticCache{limits: limits}
				want := checkSemanticValue(request, limits)
				for range 2 {
					got := cache.check(request, limits)
					if !reflect.DeepEqual(want, got) {
						t.Fatalf("%s=%d: old=%v new=%v", metric, limit, want, got)
					}
				}
			}
		}
	}
}

func TestSemanticCacheCapacityAndNormalization(t *testing.T) {
	limits := DefaultLimits()
	limits.StateItems = 1
	limits.BufferBytes = 1024
	cache := &semanticCache{limits: limits}
	for index := range 20 {
		value := StringValue(strings.Repeat("a", 300) + fmt.Sprint(index))
		if got := cache.normalize(value); got != value {
			t.Fatal("unchanged value rewritten")
		}
		if err := cache.check(&Node{Kind: TextNode, Payload: value}, limits); err != nil {
			t.Fatal(err)
		}
		if cache.size() > 1 || cache.retainedBytes > 1024 {
			t.Fatal("cache exceeded budget")
		}
	}
	for _, raw := range []string{` { "n":1e2, "text":"<>&  " } `, `"\u0061"`, `"a b"`, `[]`, `null`} {
		value, _ := ParseValue([]byte(raw))
		want, _ := EncodeValue(value)
		if got := cache.normalize(value); got != want {
			t.Fatalf("normalization: %s != %s", got.raw, want.raw)
		}
	}
}

func TestSemanticCacheSpillAndUpdates(t *testing.T) {
	limits := DefaultLimits()
	limits.StateItems = 3
	cache := &semanticCache{limits: limits}
	var retained int
	for index := range 4 {
		value := StringValue(strings.Repeat(string(rune('a'+index)), 300))
		node := &Node{Kind: TextNode, Payload: value}
		for range 2 {
			if err := cache.check(node, limits); err != nil {
				t.Fatal(err)
			}
			if cache.normalize(value) != value {
				t.Fatal("workspace changed normalized content")
			}
		}
		entry, exists := cache.lookup(value)
		if index < 3 {
			if !exists || !entry.hasMetrics || !entry.hasNormalized {
				t.Fatal("metric/normalization updates were lost")
			}
			retained += value.Len()
		} else if exists {
			t.Fatal("workspace exceeded item budget")
		}
	}
	if cache.size() != 3 || cache.retainedBytes != retained {
		t.Fatalf("workspace accounting: %d items, %d bytes", cache.size(), cache.retainedBytes)
	}
}

func TestSemanticMeterRecordsNormalizationFacts(t *testing.T) {
	padding := strings.Repeat("x", semanticCacheMinBytes)
	for _, test := range []struct {
		name, raw    string
		isNormalized bool
	}{
		{"plain", `"` + padding + `"`, true},
		{"string whitespace", `"a b ` + padding + `"`, true},
		{"escaped HTML", `"\u003c` + padding + `"`, true},
		{"escaped separator", `"\u2028` + padding + `"`, true},
		{"outer whitespace", ` "` + padding + `" `, false},
		{"literal HTML", `"<` + padding + `"`, false},
		{"literal separator", "\"\u2028" + padding + "\"", false},
		{"invalid UTF8", "\"\xff" + padding + "\"", false},
		// Whitespace removal can cancel escape growth; equal lengths alone
		// cannot prove that a value already has its normalized spelling.
		{"balanced length", `     "<` + padding + `"`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			value := parseTestValue(t, test.raw)
			limits := DefaultLimits()
			cache := &semanticCache{limits: limits}
			meter := typedMeter{limits: limits, cache: cache}
			if !meter.raw(value.raw) {
				t.Fatal("valid value did not fit")
			}
			entry, exists := cache.lookup(value)
			if !exists || !entry.hasMetrics || entry.hasNormalized != test.isNormalized {
				t.Fatalf("normalization fact differs: %+v", entry)
			}
			want, err := EncodeValue(value)
			if err != nil || cache.normalize(value) != want {
				t.Fatalf("normalization changed: %v", err)
			}
			if meter.bytes != want.Len() {
				t.Fatalf("byte count changed: %d != %d", meter.bytes, want.Len())
			}
		})
	}

	value := StringValue(padding)
	limits := DefaultLimits()
	limits.BufferBytes = value.Len() - 1
	cache := &semanticCache{limits: DefaultLimits()}
	meter := typedMeter{limits: limits, cache: cache}
	if meter.raw(value.raw) {
		t.Fatal("oversize value accepted")
	}
	if _, exists := cache.lookup(value); exists {
		t.Fatal("incomplete measurement was cached")
	}
}

func TestCanonicalToolTextMatchesLegacy(t *testing.T) {
	for _, raw := range []string{`"plain"`, ` " a b " `, `"<>&"`, `"\u0061"`, `"line\n"`, `"\ud800"`, "\"\xff\"", `null`, `{}`, `17`} {
		value, err := ParseValue([]byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		var text string
		isValid := !value.IsNull() && value.Decode(&text) == nil
		got, ok := canonicalToolText(value)
		if ok != isValid {
			t.Fatalf("validity differs: %q", raw)
		}
		if ok {
			want, _ := EncodeValue(Object{"result": StringValue(text)})
			out, _ := EncodeValue(Object{"result": got})
			if out != want {
				t.Fatalf("string wrapping differs: %q", raw)
			}
		}
	}
}
