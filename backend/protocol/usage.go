package protocol

import "math"

// MergeUsage merges counter snapshots, never treating an observed zero as an
// absent value. Inferred totals cannot replace a provider's observed total.
// The result owns its counters and does not mutate either input.
func MergeUsage(current, update *Usage) *Usage {
	if current == nil && update == nil {
		return nil
	}
	merged := &Usage{}
	for _, source := range []*Usage{current, update} {
		if source == nil {
			continue
		}
		if !source.Attribution.IsZero() {
			merged.Attribution = source.Attribution
		}
		mergeCounter(&merged.Input, source.Input)
		mergeCounter(&merged.Output, source.Output)
		mergeCounter(&merged.CacheRead, source.CacheRead)
		mergeCounter(&merged.CacheCreation, source.CacheCreation)
		mergeCounter(&merged.Total, source.Total)
		if len(source.Details) > 0 && merged.Details == nil {
			merged.Details = make(map[string]Counter)
		}
		for name, counter := range source.Details {
			before, exists := merged.Details[name]
			if !exists || before.Origin != ObservedCount || counter.Origin == ObservedCount {
				merged.Details[name] = counter
			}
		}
	}
	if merged.Total == nil || merged.Total.Origin == InferredCount || merged.Total.Origin == PlaceholderCount {
		merged.Total = nil
		if merged.Input != nil && merged.Output != nil && merged.Input.Count <= math.MaxInt64-merged.Output.Count {
			origin := InferredCount
			if merged.Input.Origin == PlaceholderCount || merged.Output.Origin == PlaceholderCount {
				origin = PlaceholderCount
			}
			merged.Total = &Counter{Count: merged.Input.Count + merged.Output.Count, Origin: origin}
		}
	}
	return merged
}

func mergeCounter(target **Counter, update *Counter) {
	if update == nil || (*target != nil && update.Origin == PlaceholderCount && (*target).Origin != PlaceholderCount) || (*target != nil && (*target).Origin == ObservedCount && update.Origin == InferredCount) {
		return
	}
	copy := *update
	*target = &copy
}
