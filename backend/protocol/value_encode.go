package protocol

import (
	"strings"

	"github.com/bytedance/sonic"
)

// encodeValueContainer handles only closed containers of validated values.
// Arbitrary Go objects use the shared Sonic codec.
func encodeValueContainer(value any) (Value, bool) {
	var keys []string
	var items []Value
	var isObject bool
	switch container := value.(type) {
	case Object:
		if container == nil {
			return Value{raw: "null"}, true
		}
		isObject = true
		keys = sortedKeys(container)
		items = make([]Value, len(keys))
		for index, key := range keys {
			items[index] = container[key]
		}
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
	parts := make([]string, len(items))
	length := 2
	add := func(count int) bool {
		if count > int(^uint(0)>>1)-length {
			return false
		}
		length += count
		return true
	}
	for index, item := range items {
		parts[index] = compactValueString(item.raw)
		if !add(len(parts[index])) || index > 0 && !add(1) {
			return Value{}, false
		}
		if isObject {
			key, _ := sonic.ConfigStd.Marshal(keys[index])
			keys[index] = string(key)
			if !add(len(key)) || !add(1) {
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
			output.WriteString(keys[index])
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
	encoded, _ := sonic.ConfigStd.Marshal(Value{raw: raw})
	return string(encoded)
}
