package server

import (
	"bytes"
	"io"
	"strings"

	"github.com/bytedance/sonic"
	"github.com/bytedance/sonic/decoder"
	"github.com/bytedance/sonic/encoder"
)

var usageJSONEncoder = sonic.Config{
	SortMapKeys:      true,
	CompactMarshaler: true,
	CopyString:       true,
	ValidateString:   true,
}.Froze()

// Preserve ConfigStd's bytes without its second full-size buffer when no HTML
// escaping is needed. UTF-8 repair inserts only ASCII \ufffd, so it commutes
// with escaping the five HTML-sensitive characters in Sonic's native encoder.
func marshalUsageJSON(value any) ([]byte, error) {
	if sonic.APIKind == sonic.UseStdJSON {
		return sonic.ConfigStd.Marshal(value)
	}
	encoded, err := usageJSONEncoder.Marshal(value)
	if err != nil {
		return encoded, err
	}
	if bytes.IndexByte(encoded, '<') >= 0 || bytes.IndexByte(encoded, '>') >= 0 || bytes.IndexByte(encoded, '&') >= 0 ||
		bytes.Contains(encoded, []byte("\u2028")) || bytes.Contains(encoded, []byte("\u2029")) {
		encoded = encoder.HTMLEscape(nil, encoded)
	}
	return encoded, nil
}

// Retained log text needs exact-sized storage: Sonic's zero-copy string result
// can otherwise keep the larger HTML-escaping buffer alive in the log queue.
func encodeUsageJSON(value any) (string, error) {
	encoded, err := marshalUsageJSON(value)
	return string(encoded), err
}

// decodeUsageJSON constructs the sole mutable log tree without copying through
// protocol.Value. Require EOF so a valid prefix never hides a trailing secret.
func decodeUsageJSON(input string) (any, bool) {
	return decodeUsageJSONStrings(input, true)
}

// Borrowed fields are confined to synchronous cleaning. Only re-encoded text
// and independently owned asset metadata may escape to the usage record.
func decodeUsageJSONBorrowed(input string) (any, bool) {
	return decodeUsageJSONStrings(input, false)
}

func decodeUsageJSONStrings(input string, shouldCopyStrings bool) (any, bool) {
	trimmed := strings.TrimSpace(input)
	if len(trimmed) > 0 && (trimmed[0] == '{' || trimmed[0] == '[') {
		reader := decoder.NewDecoder(input)
		reader.UseNumber()
		if shouldCopyStrings {
			reader.CopyString()
		}
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
