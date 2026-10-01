package protocol

import "crypto/sha256"

type sequenceRecord struct {
	sequence int64
	digest   [sha256.Size]byte
}

// SequenceTracker deduplicates declared sequence numbers within a bounded
// window. Reusing a number for different content, or replaying an expired
// sequence, is an error. Streams without sequence numbers are not deduplicated.
type SequenceTracker struct {
	records []sequenceRecord
	seen    map[int64][sha256.Size]byte
	limit   int
	next    int
	last    int64
}

// NewSequenceTracker constructs a window using validated stream limits.
func NewSequenceTracker(limits Limits) *SequenceTracker {
	return &SequenceTracker{limit: limits.StateItems, seen: make(map[int64][sha256.Size]byte)}
}

// Accept returns false for a known identical event. Payload must be canonical
// JSON from the adapter, so insignificant wire whitespace does not conflict.
func (tracker *SequenceTracker) Accept(sequence int64, payload []byte) (bool, error) {
	digest := sha256.Sum256(payload)
	if previous, exists := tracker.seen[sequence]; exists {
		if previous == digest {
			return false, nil
		}
		return false, streamIssue(UpstreamContractViolation, "sequence", "sequence number was reused with different event content")
	}
	if len(tracker.records) > 0 && sequence <= tracker.last {
		return false, streamIssue(UpstreamContractViolation, "sequence", "event sequence moved backwards or exceeded the replay window")
	}
	tracker.last = sequence
	tracker.seen[sequence] = digest
	record := sequenceRecord{sequence: sequence, digest: digest}
	if len(tracker.records) < tracker.limit {
		tracker.records = append(tracker.records, record)
	} else {
		delete(tracker.seen, tracker.records[tracker.next].sequence)
		tracker.records[tracker.next] = record
		tracker.next = (tracker.next + 1) % tracker.limit
	}
	return true, nil
}
