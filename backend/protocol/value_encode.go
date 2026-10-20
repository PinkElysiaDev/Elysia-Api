package protocol

import (
	"slices"
	"strings"

	"github.com/bytedance/sonic"
)

// encodeValueContainer handles only closed containers of validated values.
// Arbitrary Go objects use the shared Sonic codec.
func encodeValueContainer(value any) (Value, bool) {
	var keyStorage, partStorage [16]string
	keys := keyStorage[:0]
	var object Object
	var items []Value
	var isObject bool
	switch container := value.(type) {
	case Object:
		if container == nil {
			return Value{raw: "null"}, true
		}
		isObject = true
		object = container
		if len(container) > len(keyStorage) {
			keys = make([]string, 0, len(container))
		}
		for key := range container {
			keys = append(keys, key)
		}
		slices.Sort(keys)
	case []Value:
		if container == nil {
			return Value{raw: "null"}, true
		}
		items = container
	default:
		return Value{}, false
	}
	for _, item := range items {
		if item.IsZero() {
			// Select the old encoder before writing anything so its exact error
			// type and first offending element remain unchanged.
			return Value{}, false
		}
	}
	for _, item := range object {
		if item.IsZero() {
			return Value{}, false
		}
	}
	count := len(items)
	if isObject {
		count = len(keys)
	}
	parts := partStorage[:min(count, len(partStorage))]
	if count > len(partStorage) {
		parts = make([]string, count)
	}
	length := 2
	add := func(count int) bool {
		if count > int(^uint(0)>>1)-length {
			return false
		}
		length += count
		return true
	}
	for index := range count {
		var item Value
		if isObject {
			item = object[keys[index]]
		} else {
			item = items[index]
		}
		parts[index] = compactValueString(item.raw)
		if !add(len(parts[index])) || index > 0 && !add(1) {
			return Value{}, false
		}
		if isObject {
			if !add(jsonStringBytes(keys[index])) || !add(1) {
				return Value{}, false
			}
		}
	}
	var output strings.Builder
	output.Grow(length)
	if isObject {
		output.WriteByte('{')
	} else {
		output.WriteByte('[')
	}
	for index, part := range parts {
		if index > 0 {
			output.WriteByte(',')
		}
		if isObject {
			key := keys[index]
			if jsonStringBytes(key) == len(key)+2 {
				output.WriteByte('"')
				output.WriteString(key)
				output.WriteByte('"')
			} else {
				encoded, _ := sonic.ConfigStd.MarshalToString(key)
				output.WriteString(encoded)
			}
			output.WriteByte(':')
		}
		output.WriteString(part)
	}
	if isObject {
		output.WriteByte('}')
	} else {
		output.WriteByte(']')
	}
	return Value{raw: output.String()}, true
}

// Validated JSON needs only whitespace compaction and the codec's HTML /
// line separator escapes. The common base64 path returns its immutable string.
func compactValueString(raw string) string {
	if !needsSemanticCompaction(raw) {
		return raw
	}
	// Use the shared codec for exceptional spellings and custom Marshaler output.
	encoded, _ := sonic.ConfigStd.MarshalToString(Value{raw: raw})
	return ownEncodedString(encoded)
}
