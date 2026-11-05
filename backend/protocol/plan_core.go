package protocol

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

//go:generate go run -tags=coreplan_bootstrap ../internal/coreplan

const coreDirectDecodeBytes = 4 << 10

// Generated readers complete every child before publishing the parent. Record
// its size once; each later logical occurrence still pays the same full budget.
func finishCoreOutput(node *jsonOutputNode) JSONOutput {
	output := JSONOutput{node: node}
	node.encodedBytes = normalizedOutputBytes(output)
	return output
}

// Wire capture alone establishes provenance. Explicit source indexes retain
// that receipt with owned JSON; ordinary serialization or reconstruction cannot
// create it. Immutable reads preserve existing tags.
type coreOriginTag uint8

const (
	capturedNativeOrigin coreOriginTag = iota + 1
	capturedProvenanceOrigin
)

type untrustedCoreOriginError struct{ kind string }

func (err *untrustedCoreOriginError) Error() string {
	if err.kind == "Provenance" {
		return "Provenance in a package decoder must come from capture_native or capture_source"
	}
	return err.kind + " in a package decoder must come from capture_native"
}

func requireCoreOrigin(output JSONOutput, reader *valueReader, origin coreOriginTag) error {
	if reader == nil || !reader.requireCapturedOrigins || output.node != nil && output.node.origin == origin {
		return nil
	}
	kind := "Native"
	if origin == capturedProvenanceOrigin {
		kind = "Provenance"
	}
	return &untrustedCoreOriginError{kind: kind}
}

func capturedCoreReader(output JSONOutput) *valueReader {
	reader := coreOutputReader(output, nil)
	if reader == nil {
		reader = &valueReader{minimumSharedBytes: requestViewMinBytes}
	}
	reader.requireCapturedOrigins = true
	return reader
}

// decodeCoreOutput writes a completed plan directly into the closed core model.
// A rejected write may materialize once to recover the contract decoder's exact
// diagnostic. That result can never turn failure into success or rerun a mapping.
func decodeCoreOutput[T any](output JSONOutput, write func(JSONOutput, *T, *valueReader) error) (*T, error) {
	return decodeCoreOutputWithin(output, write, nil)
}

func decodeCoreOutputWithin[T any](output JSONOutput, write func(JSONOutput, *T, *valueReader) error, reader *valueReader) (*T, error) {
	var result T
	// A small raw leaf already has JSON bytes. The contract decoder consumes it
	// once without building the temporary field/index views needed by the large
	// borrowed writer. Constructed plans still write directly into the core.
	if (reader == nil || !reader.requireCapturedOrigins) && output.node == nil && output.value.Len() > 0 && output.value.Len() <= coreDirectDecodeBytes {
		if err := decodeContract(output.value.Bytes(), &result); err != nil {
			return nil, err
		}
		return &result, nil
	}
	if err := write(output, &result, reader); err != nil {
		var originError *untrustedCoreOriginError
		if errors.As(err, &originError) {
			return nil, err
		}
		value, encodingErr := output.Encode(context.Background())
		if encodingErr != nil {
			return nil, encodingErr
		}
		var original T
		if contractErr := decodeContract(value.Bytes(), &original); contractErr != nil {
			return nil, contractErr
		}
		return nil, fmt.Errorf("core output writer rejected a valid contract: %w", err)
	}
	return &result, nil
}

// A raw subtree can back every nested field. Keep its sharing threshold through
// the whole write; resetting it per container lets small descendants pin a much
// larger ancestor. Constructed output leaves retain their own ownership boundary.
func coreOutputReader(output JSONOutput, reader *valueReader) *valueReader {
	if reader == nil && output.node == nil {
		return requestValueReader(output.value)
	}
	// The provenance context starts on a constructed plan. Preserve the original
	// ownership threshold when a later raw subtree introduces a large root.
	if output.node == nil && reader != nil && reader.requireCapturedOrigins && reader.index == nil && output.value.Len() >= requestViewMinBytes {
		root := requestValueReader(output.value)
		root.requireCapturedOrigins = true
		return root
	}
	return reader
}

func coreOutputIsNull(output JSONOutput) bool {
	return output.node == nil && output.value.IsNull()
}

func coreObjectFields(output JSONOutput) ([]jsonOutputField, error) {
	iterator, err := newCoreObjectIterator(output)
	if err != nil {
		return nil, err
	}
	if iterator.raw == "" {
		return iterator.fields, nil
	}
	var fields []jsonOutputField
	for {
		field, hasField, err := iterator.next()
		if err != nil || !hasField {
			return fields, err
		}
		field.key = strings.Clone(field.key)
		fields = append(fields, field)
	}
}

// Core struct decoding merges repeated objects in wire order. Keep that order
// without an AST or field table; only writes into retained Core values detach.
type coreObjectIterator struct {
	fields []jsonOutputField
	raw    string
	offset int
}

func newCoreObjectIterator(output JSONOutput) (coreObjectIterator, error) {
	if output.node != nil {
		if !output.node.object {
			return coreObjectIterator{}, fmt.Errorf("expected core object")
		}
		return coreObjectIterator{fields: output.node.fields}, nil
	}
	if !output.value.IsObject() {
		return coreObjectIterator{}, fmt.Errorf("expected core object")
	}
	raw := strings.TrimSpace(output.value.raw)
	return coreObjectIterator{raw: raw, offset: skipJSONSpace(raw, 1)}, nil
}

func (iterator *coreObjectIterator) next() (jsonOutputField, bool, error) {
	if iterator.raw == "" {
		if iterator.offset == len(iterator.fields) {
			return jsonOutputField{}, false, nil
		}
		field := iterator.fields[iterator.offset]
		iterator.offset++
		return field, true, nil
	}
	raw, offset := iterator.raw, iterator.offset
	if raw[offset] == '}' {
		return jsonOutputField{}, false, nil
	}
	end := jsonStringEnd(raw, offset)
	key := raw[offset+1 : end-1]
	if strings.Contains(key, `\`) || !utf8.ValidString(key) {
		var err error
		key, err = decodeObjectKey(raw[offset:end])
		if err != nil {
			return jsonOutputField{}, false, err
		}
	}
	offset = skipJSONSpace(raw, skipJSONSpace(raw, end)+1)
	end = jsonValueEnd(raw, offset)
	field := jsonOutputField{key: key, value: JSONLeaf(Value{raw: raw[offset:end]})}
	offset = skipJSONSpace(raw, end)
	if raw[offset] == ',' {
		offset = skipJSONSpace(raw, offset+1)
	}
	iterator.offset = offset
	return field, true, nil
}

func coreArrayItems(output JSONOutput) ([]JSONOutput, error) {
	if output.node != nil {
		if output.node.object {
			return nil, fmt.Errorf("expected core array")
		}
		return output.node.items, nil
	}
	if !output.value.IsArray() {
		return nil, fmt.Errorf("expected core array")
	}
	values, err := output.value.readArrayWithin(&valueReader{})
	if err != nil {
		return nil, err
	}
	items := make([]JSONOutput, len(values))
	for index, value := range values {
		items[index] = JSONLeaf(value)
	}
	return items, nil
}

func writeCoreScalar[T any](output JSONOutput, target *T) error {
	if output.node != nil {
		return fmt.Errorf("expected core scalar")
	}
	return numberValueDecoder.UnmarshalFromString(output.value.raw, target)
}

func assignCoreValue(output JSONOutput, target *Value, reader *valueReader) error {
	if output.IsZero() {
		return fmt.Errorf("absent JSON value requires omission")
	}
	if output.node != nil {
		value, err := output.Encode(context.Background())
		*target = value
		return err
	}
	*target = output.value
	if reader != nil {
		target.raw = reader.fragment(target.raw)
	} else if target.Len() < requestViewMinBytes {
		target.raw = strings.Clone(target.raw)
	}
	return nil
}

func coreJSONKey(key string) string {
	if !utf8.ValidString(key) {
		return string([]rune(key))
	}
	return key
}

func ownCoreMapKey(key string) string { return strings.Clone(coreJSONKey(key)) }

func foldedCoreField(key string, fields []string) string {
	key = coreJSONKey(key)
	for _, field := range fields {
		if len(key) != len(field) {
			continue
		}
		isMatch := true
		for index := range len(key) {
			character := key[index]
			if character >= 'A' && character <= 'Z' {
				character += 'a' - 'A'
			}
			expected := field[index]
			if expected >= 'A' && expected <= 'Z' {
				expected += 'a' - 'A'
			}
			if character != expected {
				isMatch = false
				break
			}
		}
		if isMatch {
			return field
		}
	}
	return ""
}

func unknownCoreField(key string) error {
	return fmt.Errorf("unknown core field %q", key)
}

func coreArrayIndex(key string, length int) (int, error) {
	index, err := strconv.Atoi(key)
	if err != nil || index < 0 || strconv.Itoa(index) != key {
		return 0, fmt.Errorf("invalid array index in JSON pointer")
	}
	if index >= length {
		return -1, nil
	}
	return index, nil
}

func readCoreLeaf(value Value, pointer []string, cache *semanticCache) (JSONOutput, error) {
	if value.IsZero() {
		return JSONOutput{}, fmt.Errorf("absent JSON value requires omission")
	}
	reader := valueReader{minimumSharedBytes: minimumSharedValueBytes(value.Len())}
	selected, err := readValuePointerWithin(value, pointer, &reader)
	if err != nil || selected.IsZero() {
		return JSONOutput{}, err
	}
	selected = cache.normalize(selected)
	return JSONLeaf(selected), nil
}

func readCoreScalar(value Value, pointer []string) (JSONOutput, error) {
	if len(pointer) != 0 {
		return JSONOutput{}, fmt.Errorf("JSON pointer traverses a scalar")
	}
	return JSONLeaf(value), nil
}

func selectNullCoreObject(pointer []string) (Object, bool, error) {
	if len(pointer) != 0 {
		return nil, false, fmt.Errorf("JSON pointer traverses a scalar")
	}
	return nil, true, nil
}

func coreInteger(value int64) Value { return Value{raw: strconv.FormatInt(value, 10)} }

func coreBoolean(value bool) Value {
	if value {
		return Value{raw: "true"}
	}
	return Value{raw: "false"}
}

// Invalid UTF-8 map keys are normalized by JSON encoding. Preserve its sorted
// key order and last decoded occurrence when selecting through the virtual view.
func coreMapKey[V any](values map[string]V, selected string) (string, bool) {
	hasInvalidKey := false
	for key := range values {
		if !utf8.ValidString(key) {
			hasInvalidKey = true
			break
		}
	}
	if !hasInvalidKey {
		_, ok := values[selected]
		return selected, ok
	}
	key, found := "", false
	for _, candidate := range sortedKeys(values) {
		if coreJSONKey(candidate) == selected {
			key, found = candidate, true
		}
	}
	return key, found
}
