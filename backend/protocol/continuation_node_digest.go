package protocol

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
)

const continuationProjectionMinBytes = 2 << 10

func hasLargeContinuationPayload(node *Node) bool {
	if node.Payload.Len() >= continuationProjectionMinBytes || node.Input != nil && node.Input.Value.Len() >= continuationProjectionMinBytes {
		return true
	}
	for index := range node.Children {
		if hasLargeContinuationPayload(&node.Children[index]) {
			return true
		}
	}
	for index := range node.ReasoningContent {
		if hasLargeContinuationPayload(&node.ReasoningContent[index]) {
			return true
		}
	}
	for _, metadata := range node.Metadata {
		if metadata.Value.Len() >= continuationProjectionMinBytes {
			return true
		}
	}
	return false
}

// The digest consumes the same ordered Core projection as ordinary encoding,
// but never needs ownership of a complete serialized media/history document.
func hashContinuationNode(node Node) string {
	cache := &semanticCache{limits: DefaultLimits()}
	output, err := readCoreNode(node, nil, cache)
	retainContinuationLeaves(cache)
	var storage [64]string
	parts := jsonOutputParts{parts: storage[:0], cache: cache}
	if err != nil || parts.prepare(context.Background(), output) != nil {
		// The existing API hashes the absent result when Core encoding fails.
		return hashValue(Value{})
	}
	var sum [sha256.Size]byte
	if parts.bytes <= valueHashChunkBytes {
		buffer := make([]byte, parts.bytes)
		offset := 0
		for _, part := range parts.parts {
			offset += copy(buffer[offset:], part)
		}
		sum = sha256.Sum256(buffer)
	} else {
		sum = hashContinuationParts(parts.parts)
	}
	return hex.EncodeToString(sum[:])
}

// Projection has finished: the hash consumes only its normalized leaves. Keep
// their existing facts instead of scanning an expanded escaped string again.
// This cache is private to the digest and contains no semantic meter results.
func retainContinuationLeaves(cache *semanticCache) {
	cache.retainedBytes = 0
	for index := range cache.inlineCount {
		entry := &cache.inline[index]
		entry.value = spanIdentity(entry.entry.normalized.raw)
		cache.retainedBytes += entry.value.bytes
	}
	for source, entry := range cache.entries {
		target := spanIdentity(entry.normalized.raw)
		if source != target {
			delete(cache.entries, source)
			cache.entries[target] = entry
		}
	}
	for value := range cache.entries {
		cache.retainedBytes += value.bytes
	}
}

func hashContinuationParts(parts []string) [sha256.Size]byte {
	digest := sha256.New()
	var buffer [valueHashChunkBytes]byte
	for _, part := range parts {
		for len(part) > 0 {
			n := copy(buffer[:], part)
			_, _ = digest.Write(buffer[:n])
			part = part[n:]
		}
	}
	var sum [sha256.Size]byte
	digest.Sum(sum[:0])
	return sum
}
