package server

import (
	"io"
	"strings"

	"github.com/bytedance/sonic"
	"github.com/bytedance/sonic/decoder"
)

// Retained log text needs exact-sized storage: Sonic's zero-copy string result
// can otherwise keep the larger HTML-escaping buffer alive in the log queue.
func encodeUsageJSON(value any) (string, error) {
	encoded, err := sonic.ConfigStd.Marshal(value)
	return string(encoded), err
}

// decodeUsageJSON constructs the sole mutable log tree without copying through
// protocol.Value. Require EOF so a valid prefix never hides a trailing secret.
func decodeUsageJSON(input string) (any, bool) {
	trimmed := strings.TrimSpace(input)
	if len(trimmed) > 0 && (trimmed[0] == '{' || trimmed[0] == '[') {
		reader := decoder.NewDecoder(input)
		reader.UseNumber()
		reader.CopyString()
		reader.ValidateString()
		var value any
		if reader.Decode(&value) == nil && reader.CheckTrailings() == nil {
			return value, true
		}
	}
	// Preserve the reader's existing handling of scalars and nonstandard
	// fragments (such as 0000 or a trailing NUL) outside the direct path.
	return decodeUsageJSONStream(input)
}

func decodeUsageJSONStream(input string) (any, bool) {
	reader := sonic.ConfigStd.NewDecoder(strings.NewReader(input))
	reader.UseNumber()
	var value any
	if reader.Decode(&value) != nil {
		return nil, false
	}
	var trailing any
	if reader.Decode(&trailing) != io.EOF {
		return nil, false
	}
	return value, true
}
