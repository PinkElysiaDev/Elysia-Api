package protocol

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/bytedance/sonic/ast"
)

// Pointer traversal borrows intermediate raw fragments only within this call.
// The returned selection owns its bytes, so small retained metadata cannot pin
// an entire request. An empty pointer keeps the existing identity behavior.
func readValuePointer(value Value, pointer []string) (Value, error) {
	if len(pointer) == 0 {
		return value, nil
	}
	for _, key := range pointer {
		if value.IsZero() {
			return Value{}, nil
		}
		if value.IsObject() {
			next, err := sonicObjectMember(value, key)
			if err != nil {
				return Value{}, err
			}
			value = next
			continue
		}
		if !value.IsArray() {
			return Value{}, fmt.Errorf("JSON pointer traverses a scalar")
		}
		index, err := strconv.Atoi(key)
		if err != nil || index < 0 || strconv.Itoa(index) != key {
			return Value{}, fmt.Errorf("invalid array index in JSON pointer")
		}
		searcher := ast.NewSearcher(value.raw)
		searcher.ValidateJSON = false // only Value's already validated, immutable JSON
		node, err := searcher.GetByPath(index)
		if errors.Is(err, ast.ErrNotExist) {
			return Value{}, nil
		}
		if err != nil {
			return Value{}, err
		}
		raw, err := node.Raw()
		if err != nil {
			return Value{}, err
		}
		value = Value{raw: raw}
	}
	return Value{raw: strings.Clone(value.raw)}, nil
}

// Searcher's string lookup stops at the first occurrence. Enumerating one
// object's properties preserves last-decoded-key-wins, even for escaped keys,
// without materializing/cloning every sibling Value into a map.
func sonicObjectMember(value Value, key string) (Value, error) {
	node := ast.NewRaw(value.raw)
	properties, err := node.Properties()
	if err != nil {
		return Value{}, err
	}
	var pair ast.Pair
	var selected ast.Node
	found := false
	for properties.Next(&pair) {
		name := pair.Key
		// Match the standard decoder's replacement of invalid UTF-8 bytes.
		if !utf8.ValidString(name) {
			name = string([]rune(name))
		}
		if name == key {
			selected, found = pair.Value, true
		}
	}
	if err := node.Check(); err != nil {
		return Value{}, err
	}
	if !found {
		return Value{}, nil
	}
	raw, err := selected.Raw()
	return Value{raw: raw}, err
}
