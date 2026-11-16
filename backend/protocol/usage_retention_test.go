package protocol

import (
	"math"
	"reflect"
	"testing"
)

func buildRetentionUsage(total, short, long int64) *Usage {
	return &Usage{CacheCreation: &Counter{Count: total, Origin: ObservedCount}, Details: map[string]Counter{
		cacheCreation30Minutes: {Count: short, Origin: ObservedCount}, cacheCreation12Hours: {Count: long, Origin: ObservedCount},
	}}
}

func TestCacheRetentionArithmetic(t *testing.T) {
	for _, fixture := range []struct {
		name    string
		usage   *Usage
		isValid bool
	}{
		{"zero", buildRetentionUsage(0, 0, 0), true},
		{"split", buildRetentionUsage(7000, 3000, 4000), true},
		{"short-only", buildRetentionUsage(7000, 7000, 0), true},
		{"long-only", buildRetentionUsage(7000, 0, 7000), true},
		{"large-integer", buildRetentionUsage(9007199254740993, 9007199254740993, 0), true},
		{"overflow-safe", buildRetentionUsage(math.MaxInt64, math.MaxInt64, 1), false},
		{"negative", buildRetentionUsage(0, -1, 1), false},
		{"missing-count", buildRetentionUsage(7000, 3000, 3000), false},
		{"excess-count", buildRetentionUsage(7000, 3000, 5000), false},
		{"sparse", &Usage{CacheCreation: &Counter{Count: 7000, Origin: ObservedCount}, Details: map[string]Counter{cacheCreation30Minutes: {Count: 3000, Origin: ObservedCount}}}, true},
		{"unreported-total", &Usage{Details: map[string]Counter{cacheCreation30Minutes: {Count: 3000, Origin: ObservedCount}, cacheCreation12Hours: {Count: 4000, Origin: ObservedCount}}}, true},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			before := MergeUsage(nil, fixture.usage)
			err := ValidateUsageArithmetic(fixture.usage)
			if (err == nil) != fixture.isValid {
				t.Fatal("retention arithmetic accepted the wrong partition", err)
			}
			if !reflect.DeepEqual(fixture.usage, before) {
				t.Fatal("validation changed reported counters or fabricated a total")
			}
		})
	}
}

func TestCacheRetentionSnapshotsAndInputContainment(t *testing.T) {
	usage := buildRetentionUsage(7000, 3000, 4000)
	usage.Input, usage.CacheRead = &Counter{Count: 12000, Origin: ObservedCount}, &Counter{Count: 4000, Origin: ObservedCount}
	usage.Output, usage.Total = &Counter{Count: 5, Origin: ObservedCount}, &Counter{Count: 12005, Origin: ObservedCount}
	if err := ValidateUsageArithmetic(usage); err != nil {
		t.Fatal(err)
	}
	repeated := MergeUsage(usage, usage)
	if !reflect.DeepEqual(repeated, usage) {
		t.Fatal("repeated retention snapshot double-counted input or details")
	}
	updated := MergeUsage(usage, &Usage{CacheCreation: &Counter{Count: 4000, Origin: ObservedCount}, Details: map[string]Counter{cacheCreation30Minutes: {Count: 0, Origin: ObservedCount}}})
	if err := ValidateUsageArithmetic(updated); err != nil || updated.Details[cacheCreation30Minutes].Count != 0 || updated.Details[cacheCreation12Hours].Count != 4000 {
		t.Fatal("observed zero did not replace its previous snapshot", err)
	}
	invalid := MergeUsage(usage, &Usage{Details: map[string]Counter{cacheCreation12Hours: {Count: 5000, Origin: ObservedCount}}})
	if ValidateUsageArithmetic(invalid) == nil {
		t.Fatal("valid sparse update hid an invalid merged partition")
	}
	usage.Input.Count = 10000
	if ValidateUsageArithmetic(usage) == nil {
		t.Fatal("read and write partitions exceeded model input")
	}
}
