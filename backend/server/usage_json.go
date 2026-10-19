package server

import (
	"encoding/json"
	"io"
)

// decodeUsageJSON constructs the sole mutable log tree without copying through
// protocol.Value. Require EOF so a valid prefix never hides a trailing secret.
func decodeUsageJSON(input io.Reader) (any, bool) {
	decoder := json.NewDecoder(input)
	decoder.UseNumber()
	var value any
	if decoder.Decode(&value) != nil {
		return nil, false
	}
	var trailing any
	if decoder.Decode(&trailing) != io.EOF {
		return nil, false
	}
	return value, true
}
