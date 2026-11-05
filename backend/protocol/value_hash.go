package protocol

import (
	"crypto/sha256"
	"encoding/hex"
)

const valueHashChunkBytes = 32 << 10

// Hash the exact stored bytes. Compaction would change provenance and revision
// identities; large values need only a bounded scratch buffer, not a full copy.
func hashValue(value Value) string {
	var sum [sha256.Size]byte
	if value.Len() <= valueHashChunkBytes {
		sum = sha256.Sum256(value.Bytes())
	} else {
		sum = hashValueChunks(value.raw)
	}
	return hex.EncodeToString(sum[:])
}

func hashValueChunks(raw string) [sha256.Size]byte {
	hash := sha256.New()
	var buffer [valueHashChunkBytes]byte
	for len(raw) != 0 {
		n := copy(buffer[:], raw)
		_, _ = hash.Write(buffer[:n])
		raw = raw[n:]
	}
	var sum [sha256.Size]byte
	hash.Sum(sum[:0])
	return sum
}
