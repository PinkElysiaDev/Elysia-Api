package protocol

import "testing"

func TestToolConstraintComparisonKeepsAutomaticAndUnknownIntent(t *testing.T) {
	compare := func(options Object) Value {
		return fixtureValue(t, comparableToolOptions([]Tool{{Kind: FunctionTool, Options: options}}))
	}
	disabled := Object{"strictDefault": StringValue("disabled")}
	before := fixtureValue(t, disabled)
	for _, options := range []Object{nil, {"strict": Value{raw: "false"}}, {"strict": Value{raw: "null"}, "strictDefault": StringValue("disabled")}} {
		if !equalValues(compare(disabled), compare(options)) {
			t.Fatal("declared disabled forms differ", options)
		}
	}
	for _, options := range []Object{{"strictDefault": StringValue("automatic")}, {"strict": Value{raw: "true"}}, {"strictDefault": StringValue("disabled"), "future": StringValue("retain")}} {
		if equalValues(compare(disabled), compare(options)) {
			t.Fatal("different constraint or unknown option erased", options)
		}
	}
	if fixtureValue(t, disabled) != before {
		t.Fatal("comparison mutated caller options")
	}
}

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
