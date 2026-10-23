package server

import (
	"io"
	"strings"

	"github.com/bytedance/sonic"
	"github.com/bytedance/sonic/decoder"
)

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
