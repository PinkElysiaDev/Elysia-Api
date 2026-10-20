package protocol

import (
	"crypto/sha256"
	"reflect"
	"strings"
	"testing"
)

func TestSemanticDigestMatchesValueEncoding(t *testing.T) {
	for _, payload := range []Value{
		StringValue("small"),
		StringValue(strings.Repeat("large", 32<<10)),
		parseTestValue(t, ` { "number":9007199254740993,"null":null,"escape":"<>&\u2028" } `),
	} {
		events := []Event{{SchemaVersion: 1, Type: ItemDelta, ItemID: StringValue("id"), Delta: payload}}
		encoded, err := EncodeValue(events)
		if err != nil {
			t.Fatal(err)
		}
		got, err := digestSemanticEvents(events)
		if err != nil || got != sha256.Sum256(encoded.Bytes()) {
			t.Fatalf("single-buffer digest differs from Value encoding: %v", err)
		}
		events[0].Delta = StringValue("changed")
		changed, err := digestSemanticEvents(events)
		if err != nil || changed == got {
			t.Fatal("digest missed an external event mutation")
		}
	}
}

func TestSemanticCacheInlineSpillAndUpdates(t *testing.T) {
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
	if cache.inlineCount != 2 || len(cache.entries) != 1 || cache.size() != 3 || cache.retainedBytes != retained {
		t.Fatalf("workspace accounting: %d inline, %d map, %d bytes", cache.inlineCount, len(cache.entries), cache.retainedBytes)
	}
	// Repeated immutable references still contribute at every occurrence.
	value := StringValue(strings.Repeat("x", 300))
	request := &Request{Content: []Node{{Kind: TextNode, Payload: value}, {Kind: TextNode, Payload: value}}}
	limits.BufferBytes = 500
	cache = &semanticCache{limits: limits}
	if got, want := cache.check(request, limits), checkSemanticValue(request, limits); got == nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("workspace skipped repeated resource accounting: %v / %v", got, want)
	}
}

func TestTextTrackerWorkspaceClearedAndAllocationFree(t *testing.T) {
	var tracker TextTracker
	tracker.Append("prefix")
	for _, delta := range []string{"", "suffix", strings.Repeat("x", 1025)} {
		tracker.Append(delta)
		for _, b := range tracker.scratch {
			if b != 0 {
				t.Fatal("tracker retained plaintext in workspace")
			}
		}
	}
	if allocations := testing.AllocsPerRun(100, func() { tracker.Append("delta") }); allocations != 0 {
		t.Fatalf("established tracker allocates per delta: %g", allocations)
	}
	var snapshot TextTracker
	snapshot.Append("prefix")
	if allocations := testing.AllocsPerRun(100, func() {
		if suffix, err := snapshot.Snapshot("prefix"); err != nil || suffix != "" {
			t.Fatalf("unchanged snapshot: %q, %v", suffix, err)
		}
	}); allocations != 0 {
		t.Fatalf("established tracker allocates per snapshot: %g", allocations)
	}
}
