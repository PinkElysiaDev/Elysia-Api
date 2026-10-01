package protocol

import (
	"crypto/sha256"
	"fmt"
	"hash"
)

// TextTracker verifies cumulative snapshots against the prefix already sent.
// It retains a digest and byte count, not the entire forwarded text.
type TextTracker struct {
	digest hash.Hash
	length int
}

// Append records a delta without retaining its payload.
func (tracker *TextTracker) Append(delta string) {
	if tracker.digest == nil {
		tracker.digest = sha256.New()
	}
	_, _ = tracker.digest.Write([]byte(delta))
	tracker.length += len(delta)
}

// Snapshot returns only a new suffix. An incompatible rewrite is an error;
// an append-only downstream cannot retract a prefix it has already received.
func (tracker *TextTracker) Snapshot(snapshot string) (string, error) {
	if len(snapshot) < tracker.length {
		return "", streamIssue(UpstreamContractViolation, "snapshot", "cumulative content shortened an already forwarded prefix")
	}
	if tracker.length > 0 {
		prefix := sha256.Sum256([]byte(snapshot[:tracker.length]))
		if string(prefix[:]) != string(tracker.digest.Sum(nil)) {
			return "", streamIssue(UpstreamContractViolation, "snapshot", "cumulative content rewrote an already forwarded prefix")
		}
	}
	delta := snapshot[tracker.length:]
	tracker.Append(delta)
	return delta, nil
}

// Length reports the number of bytes already forwarded.
func (tracker *TextTracker) Length() int { return tracker.length }

func streamIssue(code IssueCode, path, reason string) error {
	return &ConversionError{Issues: []ConversionIssue{{
		Code: code, Severity: SeverityError, Direction: DecodeEvent, Stage: "stream",
		Path: path, Reason: reason, Suggestion: fmt.Sprintf("correct the upstream stream or declared %s rule", path),
	}}}
}
