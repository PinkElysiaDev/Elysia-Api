// Package protocol defines the versioned semantic contract and execution rules
// shared by gateway adapters, protocol verification, the editor and the Agent.
package protocol

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/bytedance/sonic"
	"github.com/elysia-api/backend/internal/jsonutil"
)

// Value is immutable JSON. Its zero value means absent; JSON null, false, zero
// and empty containers are distinct values. Numbers retain their JSON spelling.
type Value struct{ raw string }

// Match ConfigStd's decoder ownership/validation options while preserving
// interface numbers. Direct string decoding avoids streaming-buffer growth.
var numberValueDecoder = sonic.Config{UseNumber: true, CopyString: true, ValidateString: true}.Froze()

// Small codec results already have compact owned storage. Large codec results
// can expose a grown backing buffer through their immutable string; detach them
// so long-lived Values cannot retain excess encoder capacity.
func ownEncodedString(raw string) string {
	if len(raw) > 64<<10 {
		return strings.Clone(raw)
	}
	return raw
}

// ParseValue validates and copies one JSON value without converting numbers.
func ParseValue(raw []byte) (Value, error) {
	if !jsonutil.Valid(raw) {
		return Value{}, fmt.Errorf("invalid JSON value")
	}
	return Value{raw: string(raw)}, nil
}

// EncodeValue converts a Go value at an adapter boundary.
func EncodeValue(value any) (Value, error) {
	if encoded, handled := encodeValueContainer(value); handled {
		return encoded, nil
	}
	raw, err := sonic.ConfigStd.MarshalToString(value)
	if err != nil {
		return Value{}, err
	}
	return Value{raw: ownEncodedString(raw)}, nil
}

// StringValue constructs a JSON string; strings cannot fail JSON encoding.
func StringValue(value string) Value {
	// Frame metadata, item IDs and short text are usually plain ASCII. Build
	// their exact owned representation instead of acquiring a generic encoder
	// workspace for each scalar in the streaming pipeline. Complex/large text
	// retains the codec's Unicode, invalid-byte and HTML-escaping behavior.
	if len(value) <= 4096 && plainJSONString(value) {
		return Value{raw: `"` + value + `"`}
	}
	raw, _ := sonic.ConfigStd.MarshalToString(value)
	return Value{raw: ownEncodedString(raw)}
}

func plainJSONString(value string) bool {
	for index := range len(value) {
		character := value[index]
		if character < 0x20 || character >= 0x80 || character == '"' || character == '\\' || character == '<' || character == '>' || character == '&' {
			return false
		}
	}
	return true
}

// IsString compares decoded string content without changing the stored wire bytes.
// Absent, null and non-string values never match, including an empty string.
func (value Value) IsString(expected string) bool {
	if value.IsNull() {
		return false
	}
	var text string
	return value.Decode(&text) == nil && text == expected
}

// IsZero reports absence, allowing the codec's omitzero to preserve null.
func (value Value) IsZero() bool { return value.raw == "" }

// IsNull reports an explicitly supplied JSON null.
func (value Value) IsNull() bool {
	return strings.TrimSpace(value.raw) == "null"
}

// IsObject reports the JSON container kind without decoding its fields.
func (value Value) IsObject() bool {
	trimmed := strings.TrimSpace(value.raw)
	return len(trimmed) > 0 && trimmed[0] == '{'
}

// IsArray reports the JSON container kind without copying its contents.
func (value Value) IsArray() bool {
	trimmed := strings.TrimSpace(value.raw)
	return len(trimmed) > 0 && trimmed[0] == '['
}

// IsStringValue reports a JSON string without decoding or copying it.
func (value Value) IsStringValue() bool {
	trimmed := strings.TrimSpace(value.raw)
	return len(trimmed) > 0 && trimmed[0] == '"'
}

// Bytes returns a copy of the original JSON representation.
func (value Value) Bytes() []byte { return []byte(value.raw) }

// JSONString returns the immutable original JSON spelling without a byte copy.
// Callers retaining a small selection of a larger view must detach that selection.
func (value Value) JSONString() string { return value.raw }

// Len reports the encoded byte length without materializing a mutable copy.
func (value Value) Len() int { return len(value.raw) }

// Decode reads into a typed destination. Interface destinations receive
// json.Number, never float64, so integers larger than 2^53 remain exact.
func (value Value) Decode(target any) error {
	if value.IsZero() {
		return fmt.Errorf("cannot decode an absent value")
	}
	// Closed typed destinations cannot coerce numbers through interface{}.
	// Avoid a streaming decoder and its buffers for these single-value reads.
	switch typed := target.(type) {
	case *Object:
		if value.IsNull() {
			*typed = nil
			return nil
		}
		object, err := value.readObject()
		if err == nil {
			*typed = object
		}
		return err
	case *[]Value:
		items, err := value.readArray()
		if err == nil {
			*typed = items
		}
		return err
	case *string, *bool, *int, *int64, *uint64, *json.Number,
		*Request, *Response, *Event, *[]Event:
		return sonic.ConfigStd.UnmarshalFromString(value.raw, target)
	}
	return numberValueDecoder.UnmarshalFromString(value.raw, target)
}

// MarshalJSON encodes a present value. Use omitzero on optional struct fields;
// absent values inside arrays and maps are invalid rather than implicit null.
func (value Value) MarshalJSON() ([]byte, error) {
	if value.IsZero() {
		return nil, fmt.Errorf("absent JSON value requires omission")
	}
	return value.Bytes(), nil
}

// UnmarshalJSON retains the native representation, including explicit null.
func (value *Value) UnmarshalJSON(raw []byte) error {
	parsed, err := ParseValue(raw)
	if err != nil {
		return err
	}
	*value = parsed
	return nil
}

// Object holds fields with explicit presence. Omission means no map entry.
type Object map[string]Value

// ReadObject decodes an object while preserving every field's native JSON.
func (value Value) ReadObject() (Object, error) {
	return value.readObject()
}
