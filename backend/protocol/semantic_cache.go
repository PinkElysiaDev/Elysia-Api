package protocol

import "reflect"

const semanticCacheMinBytes = 256

type semanticMetrics struct{ bytes, nodes, depth int }

type semanticValueEntry struct {
	metrics       semanticMetrics
	hasMetrics    bool
	normalized    Value
	hasNormalized bool
}

// semanticCache retains immutable values only within one execution segment.
// Reuse saves scans, never occurrences: each reference contributes its full
// encoded size and node count at its actual nesting depth.
type semanticCache struct {
	limits        Limits
	entries       map[Value]semanticValueEntry
	inline        *[2]semanticCacheEntry
	inlineCount   int
	retainedBytes int
}

type semanticCacheEntry struct {
	value Value
	entry semanticValueEntry
}

// Most frame-local segments visit only a payload and its native envelope. A
// lazily allocated two-entry workspace avoids a hash table (and hashing large
// identical strings) without increasing storage for executions that cache none.
// Wider segments spill to the same bounded map; no storage crosses requests.
func (cache *semanticCache) lookup(value Value) (semanticValueEntry, bool) {
	for index := range cache.inlineCount {
		if cached := cache.inline[index]; cached.value == value {
			return cached.entry, true
		}
	}
	entry, exists := cache.entries[value]
	return entry, exists
}

func (cache *semanticCache) size() int { return cache.inlineCount + len(cache.entries) }

func (cache *semanticCache) check(value any, limits Limits) error {
	if cache == nil {
		return checkSemanticValue(value, limits)
	}
	if plan := typedLimitPlans[reflect.TypeOf(value)]; plan != nil {
		meter := typedMeter{limits: limits, cache: cache}
		if meter.visit(plan, reflect.ValueOf(value)) && meter.fits() {
			return nil
		}
	}
	// Preserve exact observed counts and the encoder's original error priority.
	return checkSemanticValue(value, limits)
}

func (entry semanticValueEntry) retainedBytes(value Value) int {
	size := len(value.raw)
	if entry.hasNormalized && entry.normalized != value {
		size += len(entry.normalized.raw)
	}
	return size
}

func (cache *semanticCache) store(value Value, entry semanticValueEntry) {
	previous, exists := cache.lookup(value)
	if !exists && cache.size() >= cache.limits.StateItems {
		return
	}
	previousBytes := 0
	if exists {
		previousBytes = previous.retainedBytes(value)
	}
	remaining := cache.limits.BufferBytes - (cache.retainedBytes - previousBytes)
	if len(value.raw) > remaining {
		return
	}
	if entry.hasNormalized && entry.normalized != value && len(entry.normalized.raw) > remaining-len(value.raw) {
		return
	}
	for index := range cache.inlineCount {
		if cache.inline[index].value == value {
			cache.inline[index].entry = entry
			cache.retainedBytes += entry.retainedBytes(value) - previousBytes
			return
		}
	}
	if !exists && cache.inlineCount < 2 {
		if cache.inline == nil {
			cache.inline = new([2]semanticCacheEntry)
		}
		cache.inline[cache.inlineCount] = semanticCacheEntry{value, entry}
		cache.inlineCount++
	} else {
		if cache.entries == nil {
			cache.entries = make(map[Value]semanticValueEntry)
		}
		cache.entries[value] = entry
	}
	cache.retainedBytes += entry.retainedBytes(value) - previousBytes
}

func (cache *semanticCache) normalize(value Value) Value {
	entry, _ := cache.lookup(value)
	if entry.hasNormalized {
		return entry.normalized
	}
	result := value
	if needsSemanticCompaction(value.raw) {
		result, _ = EncodeValue(value)
	}
	entry.normalized, entry.hasNormalized = result, true
	if len(value.raw) >= semanticCacheMinBytes {
		cache.store(value, entry)
	}
	return result
}
