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
