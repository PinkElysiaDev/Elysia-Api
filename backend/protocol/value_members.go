package protocol

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// ReadObjectMembers reads selected object fields into caller-owned slots without
// materializing a field map. Slots must match names in length and are cleared on
// every successful read; absent fields stay zero. Duplicate decoded keys use the
// last occurrence, matching ReadObject. Returned Values own independent storage.
func (value Value) ReadObjectMembers(names []string, slots []Value) error {
	if len(names) != len(slots) {
		return fmt.Errorf("object member slots must match names")
	}
	raw := strings.TrimSpace(value.raw)
	if len(raw) == 0 || raw[0] != '{' {
		return fmt.Errorf("expected JSON object")
	}
	clear(slots)
	for offset := skipJSONSpace(raw, 1); raw[offset] != '}'; {
		end := jsonStringEnd(raw, offset)
		key := raw[offset+1 : end-1]
		if strings.Contains(key, `\`) || !utf8.ValidString(key) {
			var err error
			key, err = decodeObjectKey(raw[offset:end])
			if err != nil {
				return err
			}
		}
		offset = skipJSONSpace(raw, skipJSONSpace(raw, end)+1)
		end = jsonValueEnd(raw, offset)
		for index, name := range names {
			if key == name {
				slots[index] = Value{raw: strings.Clone(raw[offset:end])}
			}
		}
		offset = skipJSONSpace(raw, end)
		if raw[offset] == ',' {
			offset = skipJSONSpace(raw, offset+1)
		}
	}
	return nil
}
