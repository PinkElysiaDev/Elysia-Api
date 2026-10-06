package agent

import (
	"math"
	"testing"

	"github.com/elysia-api/backend/protocol"
)

func TestAgentUsageSumPreservesPresenceAndProvenance(t *testing.T) {
	observed := func(count int64) *protocol.Counter {
		return &protocol.Counter{Count: count, Origin: protocol.ObservedCount}
	}
	first := &protocol.Usage{Input: observed(10), Output: observed(2), Total: observed(12), CacheRead: observed(0), CacheCreation: observed(5), Details: map[string]protocol.Counter{"cache_creation_5m": *observed(5)}}
	next := &protocol.Usage{Input: observed(20), Output: observed(3), Total: &protocol.Counter{Count: 23, Origin: protocol.InferredCount}, CacheRead: observed(0)}
	sum, err := addUsage(first, next)
	if err != nil || sum.Input.Count != 30 || sum.CacheRead == nil || sum.CacheRead.Count != 0 || sum.CacheCreation != nil || len(sum.Details) != 0 || sum.Total.Origin != protocol.InferredCount {
		t.Fatal(sum, err)
	}
	if first.Input.Count != 10 {
		t.Fatal("summation mutated persisted usage")
	}
	next.CacheRead = nil
	sum, err = addUsage(first, next)
	if err != nil || sum.CacheRead != nil {
		t.Fatal("missing cache count became zero", sum, err)
	}
	if sum, err = addUsage(first, nil); err != nil || sum != nil {
		t.Fatal("unreported call acquired complete totals")
	}
	first.Input.Count = math.MaxInt64
	if _, err = addUsage(first, next); err == nil {
		t.Fatal("counter overflow accepted")
	}
}
