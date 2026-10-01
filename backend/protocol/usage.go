package protocol

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
	if merged.Total == nil || merged.Total.Origin == InferredCount {
		if merged.Input != nil || merged.Output != nil {
			merged.Total = &Counter{Origin: InferredCount}
			if merged.Input != nil {
				merged.Total.Count += merged.Input.Count
			}
			if merged.Output != nil {
				merged.Total.Count += merged.Output.Count
			}
		}
	}
	return merged
}

func mergeCounter(target **Counter, update *Counter) {
	if update == nil || (*target != nil && (*target).Origin == ObservedCount && update.Origin == InferredCount) {
		return
	}
	copy := *update
	*target = &copy
}
