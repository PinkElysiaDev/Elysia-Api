package protocol

import (
	"strings"
	"testing"
)

// These two helpers isolate the owner-local workspace changes; they are not
// gateway latency benchmarks. The same source runs on the preceding revision.
func BenchmarkProfilingWorkspace(b *testing.B) {
	b.Run("normalize-event", func(b *testing.B) {
		event := &Event{SchemaVersion: 1, Type: ItemDelta, ItemID: StringValue("id"), Delta: StringValue(strings.Repeat("x", 256))}
		limits := DefaultLimits()
		b.ReportAllocs()
		for b.Loop() {
			got := normalizeMappingSemantic(event, limits)
			if got.Delta != event.Delta {
				b.Fatal("normalization changed content")
			}
		}
	})
	b.Run("text-append", func(b *testing.B) {
		var tracker TextTracker
		tracker.Append("warm")
		delta := strings.Repeat("x", 256)
		b.ReportAllocs()
		for b.Loop() {
			tracker.Append(delta)
		}
	})
}
