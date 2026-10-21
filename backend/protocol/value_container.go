package protocol

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/bytedance/sonic"
)

// readObject uses spans of the already validated, immutable JSON string. Child
// values keep their exact spelling without decoding and revalidating the same
// subtree at each adapter layer.
func (value Value) readObject() (Object, error) {
	return value.readObjectWithin(nil)
}

func (value Value) readObjectWithin(reader *valueReader) (Object, error) {
	raw := strings.TrimSpace(value.raw)
	if len(raw) == 0 || raw[0] != '{' {
		return nil, fmt.Errorf("expected JSON object")
	}
	fields := Object{}
	for offset := skipJSONSpace(raw, 1); raw[offset] != '}'; {
		end := jsonStringEnd(raw, offset)
		key := raw[offset+1 : end-1]
		if strings.Contains(key, `\`) || !utf8.ValidString(key) {
			if err := sonic.ConfigStd.Unmarshal([]byte(raw[offset:end]), &key); err != nil {
				return nil, err
			}
		} else {
			key = strings.Clone(key)
		}
		offset = skipJSONSpace(raw, skipJSONSpace(raw, end)+1)
		end = reader.valueEnd(raw, offset)
		// Retained metadata must not keep an entire large frame alive through a
		// tiny substring; resource accounting measures the child value's bytes.
		fields[key] = Value{raw: reader.fragment(raw[offset:end])}
		offset = skipJSONSpace(raw, end)
		if raw[offset] == ',' {
			offset = skipJSONSpace(raw, offset+1)
		}
	}
	return fields, nil
}

func (value Value) readArray() ([]Value, error) {
	return value.readArrayWithin(nil)
}

func (value Value) readArrayWithin(reader *valueReader) ([]Value, error) {
	raw := strings.TrimSpace(value.raw)
	if raw == "null" {
		return nil, nil
	}
	if len(raw) == 0 || raw[0] != '[' {
		return nil, fmt.Errorf("expected JSON array")
	}
	items := []Value{}
	for offset := skipJSONSpace(raw, 1); raw[offset] != ']'; {
		end := reader.valueEnd(raw, offset)
		items = append(items, Value{raw: reader.fragment(raw[offset:end])})
		offset = skipJSONSpace(raw, end)
		if raw[offset] == ',' {
			offset = skipJSONSpace(raw, offset+1)
		}
	}
	return items, nil
}

func skipJSONSpace(raw string, offset int) int {
	for offset < len(raw) && strings.ContainsRune(" \t\r\n", rune(raw[offset])) {
		offset++
	}
	return offset
}

// These span scanners operate only on Value's validated JSON, never raw input.
func jsonStringEnd(raw string, start int) int {
	// IndexByte uses the platform's bulk byte search for large plain strings.
	// A quote ends the string iff its immediately preceding backslash run is even.
	for offset := start + 1; offset < len(raw); {
		next := strings.IndexByte(raw[offset:], '"')
		if next < 0 {
			return len(raw)
		}
		quote := offset + next
		back := quote - 1
		for back > start && raw[back] == '\\' {
			back--
		}
		if (quote-1-back)%2 == 0 {
			return quote + 1
		}
		offset = quote + 1
	}
	return len(raw)
}

func jsonValueEnd(raw string, start int) int {
	if raw[start] == '"' {
		return jsonStringEnd(raw, start)
	}
	if raw[start] != '{' && raw[start] != '[' {
		offset := start + 1
		for offset < len(raw) && !strings.ContainsRune(" \t\r\n,]}", rune(raw[offset])) {
			offset++
		}
		return offset
	}
	depth := 0
	for offset := start; offset < len(raw); offset++ {
		switch raw[offset] {
		case '"':
			offset = jsonStringEnd(raw, offset) - 1
		case '{', '[':
			depth++
		case '}', ']':
			depth--
			if depth == 0 {
				return offset + 1
			}
		}
	}
	return len(raw)
}
