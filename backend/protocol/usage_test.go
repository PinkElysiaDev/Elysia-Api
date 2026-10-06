package protocol

import (
	"math"
	"testing"
)

func TestUsageTotalsRequireBothComponents(t *testing.T) {
	partial := MergeUsage(nil, &Usage{Input: &Counter{Count: 7, Origin: ObservedCount}})
	if partial.Total != nil || partial.Output != nil {
		t.Fatal("missing output treated as zero", partial)
	}
	complete := MergeUsage(partial, &Usage{Output: &Counter{Count: 0, Origin: ObservedCount}})
	if complete.Total == nil || complete.Total.Count != 7 || complete.Total.Origin != InferredCount {
		t.Fatal(complete)
	}
	overflow := MergeUsage(nil, &Usage{Input: &Counter{Count: math.MaxInt64, Origin: ObservedCount}, Output: &Counter{Count: 1, Origin: ObservedCount}})
	if overflow.Total != nil {
		t.Fatal("overflowing total was fabricated", overflow.Total)
	}
	issues := CheckResponse(&Response{SchemaVersion: 1, Usage: overflow}, Target{Capabilities: CapabilitySet{UsageCapability: true}}, DefaultLimits())
	if IssuesError(issues) == nil {
		t.Fatal("overflowing upstream usage accepted")
	}
}
