package protocol

const (
	cacheCreation30Minutes = "cache_creation.30m_tokens"
	cacheCreation12Hours   = "cache_creation.12h_tokens"
	cacheRetentionBuckets  = 2
)

// Retention buckets partition cache creation; they never add tokens to input
// or total. A sparse snapshot does not imply a zero in the missing bucket.
func validateCacheWriteRetention(usage *Usage) error {
	remaining, count := int64(0), 0
	if usage.CacheCreation != nil {
		remaining = usage.CacheCreation.Count
	}
	for _, name := range [...]string{cacheCreation30Minutes, cacheCreation12Hours} {
		counter, hasCounter := usage.Details[name]
		if !hasCounter {
			continue
		}
		count++
		if counter.Count < 0 {
			return streamIssue(InvalidInput, "/usage/details/"+name, "cache retention count must be nonnegative")
		}
		if usage.CacheCreation == nil {
			continue
		}
		if counter.Count > remaining {
			return streamIssue(InvalidInput, "/usage/details/"+name, "cache retention breakdown exceeds cache creation")
		}
		remaining -= counter.Count
	}
	if usage.CacheCreation != nil && count == cacheRetentionBuckets && remaining != 0 {
		return streamIssue(InvalidInput, "/usage/cacheCreation", "complete cache retention breakdown disagrees with cache creation")
	}
	if count != 0 && usage.Input != nil && usage.CacheCreation != nil {
		if usage.CacheCreation.Count > usage.Input.Count || usage.CacheRead != nil && usage.CacheRead.Count > usage.Input.Count-usage.CacheCreation.Count {
			return streamIssue(InvalidInput, "/usage/input", "cache reads and writes must remain within total input")
		}
	}
	return nil
}
