package protocol

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"hash"
)

// ContinuationHistoryDigest accumulates the existing role:digest history format.
// Sum reads a prefix without consuming it; each owner has independent state.
type ContinuationHistoryDigest struct {
	mac      hash.Hash
	hasParts bool
}

// NewHistoryDigest starts a keyed history accumulator without exposing its key.
func (codec *ContinuationCodec) NewHistoryDigest() *ContinuationHistoryDigest {
	return &ContinuationHistoryDigest{mac: hmac.New(sha256.New, codec.key)}
}

// Append adds one unchanged semantic node's role and digest in wire order.
func (digest *ContinuationHistoryDigest) Append(role, node string) {
	if digest.hasParts {
		_, _ = digest.mac.Write([]byte{'\n'})
	}
	_, _ = digest.mac.Write([]byte(role))
	_, _ = digest.mac.Write([]byte{':'})
	_, _ = digest.mac.Write([]byte(node))
	digest.hasParts = true
}

// Sum returns the current prefix in the same form as ContinuationCodec.Digest.
func (digest *ContinuationHistoryDigest) Sum() string {
	var sum [sha256.Size]byte
	return hex.EncodeToString(digest.mac.Sum(sum[:0]))
}
