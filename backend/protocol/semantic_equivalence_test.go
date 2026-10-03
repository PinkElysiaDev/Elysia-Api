package protocol

import "testing"

func TestWireUsageComparisonRetainsMissingZeroAndNonredundantDetails(t *testing.T) {
	observed := func(count int64) *Counter { return &Counter{Count: count, Origin: ObservedCount} }
	base := &Usage{Input: observed(10), Output: observed(2), Total: observed(12), CacheRead: observed(4)}
	equivalent := &Usage{Input: observed(10), Output: observed(2), Total: &Counter{Count: 12, Origin: InferredCount}, CacheRead: observed(4), Details: map[string]Counter{"uncached_input_tokens": {Count: 6, Origin: ObservedCount}}}
	encode := func(usage *Usage) Value { return fixtureValue(t, comparableWireUsage(usage)) }
	if !equalValues(encode(base), encode(equivalent)) {
		t.Fatal("wire-independent counter provenance blocked equal usage")
	}
	for _, change := range []func(*Usage){
		func(usage *Usage) { usage.CacheRead = nil },
		func(usage *Usage) { usage.CacheRead = observed(0) },
		func(usage *Usage) { usage.CacheCreation = observed(0) },
		func(usage *Usage) {
			usage.Details = map[string]Counter{"uncached_input_tokens": {Count: 7, Origin: ObservedCount}}
		},
		func(usage *Usage) {
			usage.Details = map[string]Counter{"ephemeral_5m_input_tokens": {Count: 0, Origin: ObservedCount}}
		},
		func(usage *Usage) { usage.Total = observed(11) },
	} {
		copy := *base
		change(&copy)
		if equalValues(encode(base), encode(&copy)) {
			t.Fatal("counter difference erased", copy)
		}
	}
}
