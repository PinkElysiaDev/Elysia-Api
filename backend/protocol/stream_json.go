package protocol

import (
	"encoding/json"
	"io"
	"strings"
)

// JSONToolSnapshotSuffix handles a complete JSON tool snapshot after streamed
// arguments. Semantic serialization may change whitespace or escaping, but
// cannot change types, exact numbers, or values. Partial JSON and text inputs
// retain the ordinary append-only prefix rule. Callers retain their original
// bytes, including those used for continuation provenance.
func JSONToolSnapshotSuffix(previous, snapshot string) (string, error) {
	if strings.HasPrefix(snapshot, previous) {
		return snapshot[len(previous):], nil
	}
	a, aerr := ParseValue([]byte(previous))
	b, berr := ParseValue([]byte(snapshot))
	if aerr == nil && berr == nil && sameJSONToolTokens(a, b) {
		return "", nil
	}
	return "", streamIssue(UpstreamContractViolation, "snapshot", "JSON tool snapshot changed already forwarded arguments")
}

// Compare tokens rather than maps: duplicate properties and their order cannot
// disappear under a last-key-wins object decoder. Numbers use exact arithmetic.
func sameJSONToolTokens(a, b Value) bool {
	x, y := json.NewDecoder(strings.NewReader(a.raw)), json.NewDecoder(strings.NewReader(b.raw))
	x.UseNumber()
	y.UseNumber()
	for {
		left, le := x.Token()
		right, re := y.Token()
		if le == io.EOF && re == io.EOF {
			return true
		}
		if le != nil || re != nil || !equalJSON(left, right) {
			return false
		}
	}
}
