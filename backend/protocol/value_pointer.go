package protocol

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/bytedance/sonic/ast"
)

const objectMemberScanBytes = 1024

// Pointer traversal borrows intermediate raw fragments only within this call.
// The returned selection owns its bytes, so small retained metadata cannot pin
// an entire request. An empty pointer keeps the existing identity behavior.
func readValuePointer(value Value, pointer []string) (Value, error) {
	return readValuePointerWithin(value, pointer, nil)
}

func readValuePointerWithin(value Value, pointer []string, reader *valueReader) (Value, error) {
	return readValuePointerWithObjectReader(value, pointer, reader, nil)
}

func readValuePointerWithObjectReader(value Value, pointer []string, reader *valueReader, objects *planObjectReader) (Value, error) {
	if len(pointer) == 0 {
		return value, nil
	}
	var owner *planObjectReader
	for _, key := range pointer {
		if value.IsZero() {
			return Value{}, nil
		}
		if value.IsObject() {
			if enclosing := objects.ownerFor(value); enclosing != nil {
				owner = enclosing
			}
			next, err := owner.readOwnedMember(value, key)
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
	return Value{raw: reader.fragment(value.raw)}, nil
}

// Single reads of small, already validated objects do not need a parse tree.
// Keep the last matching key, including escaped and invalid Unicode spellings.
func scanObjectMember(value Value, key string) (Value, error) {
	raw := strings.TrimSpace(value.raw)
	var selected Value
	for offset := skipJSONSpace(raw, 1); raw[offset] != '}'; {
		end := jsonStringEnd(raw, offset)
		name := raw[offset+1 : end-1]
		if strings.Contains(name, `\`) || !utf8.ValidString(name) {
			var err error
			name, err = decodeObjectKey(raw[offset:end])
			if err != nil {
				return Value{}, err
			}
		}
		offset = skipJSONSpace(raw, skipJSONSpace(raw, end)+1)
		end = jsonValueEnd(raw, offset)
		if name == key {
			selected = Value{raw: raw[offset:end]}
		}
		offset = skipJSONSpace(raw, end)
		if raw[offset] == ',' {
			offset = skipJSONSpace(raw, offset+1)
		}
	}
	return selected, nil
}

// Searcher's string lookup stops at the first occurrence. Enumerate cached
// properties to preserve the same last-decoded-key-wins contract.
func sonicObjectMember(value Value, key string) (Value, error) {
	node := ast.NewRaw(value.raw)
	return readSonicObjectMember(&node, key)
}

func readSonicObjectMember(node *ast.Node, key string) (Value, error) {
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
