package protocol

import (
	"context"
	"fmt"
	"strings"
)

// reconcileNative applies semantic differences to the received wire document.
// Fields absent from both rendered versions belong to the original wire node.
// Arrays need a declared identity when several changed nodes carry extensions;
// guessing their association would move extensions to the wrong tool/message.
func reconcileNative(ctx context.Context, original, before, after Value, policy NativePolicy, limits Limits) (Value, error) {
	state := nativeReconciler{ctx: ctx, policy: policy, limits: limits, deferred: max(original.Len(), before.Len(), after.Len()) >= 64<<10}
	output, err := state.merge(original, before, after, "", 1)
	if err != nil {
		return Value{}, err
	}
	value, err := output.Encode(ctx)
	if err != nil {
		return Value{}, err
	}
	if err := checkValueLimits(value, limits); err != nil {
		return Value{}, err
	}
	return value, nil
}

type nativeReconciler struct {
	ctx      context.Context
	policy   NativePolicy
	limits   Limits
	nodes    int
	deferred bool
}

func (state *nativeReconciler) merge(original, before, after Value, path string, depth int) (JSONOutput, error) {
	if err := state.ctx.Err(); err != nil {
		return JSONOutput{}, err
	}
	state.nodes++
	if depth > state.limits.Depth {
		return JSONOutput{}, &ResourceLimitError{Domain: "native_reconciliation", Metric: "depth", Limit: state.limits.Depth, Observed: depth, Path: path}
	}
	if state.nodes > state.limits.Nodes {
		return JSONOutput{}, &ResourceLimitError{Domain: "native_reconciliation", Metric: "nodes", Limit: state.limits.Nodes, Observed: state.nodes, Path: path}
	}
	if equalNativeValues(before, after) {
		return JSONLeaf(original), nil
	}
	// A fully represented subtree has no unmapped native fields to associate.
	// Its semantic replacement is authoritative, including reorders and deletions.
	if equalNativeValues(original, before) {
		return JSONLeaf(after), nil
	}
	if original.IsObject() && before.IsObject() && after.IsObject() {
		return state.mergeObject(original, before, after, path, depth)
	}
	if valueType(original) == ArrayType && valueType(before) == ArrayType && valueType(after) == ArrayType {
		return state.mergeArray(original, before, after, path, depth)
	}
	return JSONLeaf(after), nil
}

func (state *nativeReconciler) mergeObject(original, before, after Value, path string, depth int) (JSONOutput, error) {
	// Container types were checked at the boundary; Value is already valid JSON.
	fields, _ := original.readObjectWithin(requestValueReader(original))
	oldFields, _ := before.readObjectWithin(requestValueReader(before))
	newFields, _ := after.readObjectWithin(requestValueReader(after))
	for key := range oldFields {
		if _, exists := newFields[key]; !exists {
			delete(fields, key)
		}
	}
	var result map[string]JSONOutput
	if state.deferred {
		result = make(map[string]JSONOutput, max(len(fields), len(newFields)))
		for key, value := range fields {
			result[key] = JSONLeaf(value)
		}
	}
	for _, key := range sortedKeys(newFields) {
		oldValue, wasPresent := oldFields[key]
		newValue := newFields[key]
		if !wasPresent {
			if state.deferred {
				result[key] = JSONLeaf(newValue)
			} else {
				fields[key] = newValue
			}
			continue
		}
		merged, err := state.merge(fields[key], oldValue, newValue, path+"/"+escapePointer(key), depth+1)
		if err != nil {
			return JSONOutput{}, err
		}
		if !merged.IsZero() {
			if state.deferred {
				result[key] = merged
			} else {
				fields[key] = merged.value
			}
		}
	}
	if state.deferred {
		return JSONObject(result), nil
	}
	value, err := EncodeValue(fields)
	return JSONLeaf(value), err
}

func (state *nativeReconciler) mergeArray(original, before, after Value, path string, depth int) (JSONOutput, error) {
	items, _ := original.readArrayWithin(requestValueReader(original))
	oldItems, _ := before.readArrayWithin(requestValueReader(before))
	newItems, _ := after.readArrayWithin(requestValueReader(after))
	if len(newItems) == 0 {
		return JSONLeaf(after), nil
	}
	if len(items) != len(oldItems) {
		return JSONOutput{}, fmt.Errorf("%s: decoder/encoder changed native array cardinality; an explicit mapping is required", path)
	}
	indices, err := matchNativeItems(oldItems, newItems, state.policy.ArrayKeys[path])
	if err != nil {
		return JSONOutput{}, fmt.Errorf("%s: %w", path, err)
	}
	var result []JSONOutput
	var values []Value
	if state.deferred {
		result = make([]JSONOutput, len(newItems))
	} else {
		values = make([]Value, len(newItems))
	}
	for index, item := range newItems {
		previous := indices[index]
		if previous < 0 {
			if state.deferred {
				result[index] = JSONLeaf(item)
			} else {
				values[index] = item
			}
			continue
		}
		merged, err := state.merge(items[previous], oldItems[previous], item, fmt.Sprintf("%s/%d", path, index), depth+1)
		if err != nil {
			return JSONOutput{}, err
		}
		if state.deferred {
			result[index] = merged
		} else {
			values[index] = merged.value
		}
	}
	if state.deferred {
		return JSONArray(result), nil
	}
	value, err := EncodeValue(values)
	return JSONLeaf(value), err
}

func matchNativeItems(before, after []Value, key string) ([]int, error) {
	indices := make([]int, len(after))
	lookup := make(map[string][]int, len(before))
	for index, item := range before {
		identity, err := nativeItemIdentity(item, key)
		if err != nil {
			return nil, err
		}
		lookup[identity] = append(lookup[identity], index)
		if key != "" && len(lookup[identity]) > 1 {
			return nil, fmt.Errorf("duplicate native array identity")
		}
	}
	used := make(map[int]bool, len(after))
	seen := make(map[string]bool, len(after))
	var unmatched []int
	for index, item := range after {
		indices[index] = -1
		identity, err := nativeItemIdentity(item, key)
		if err != nil {
			return nil, err
		}
		if key != "" && seen[identity] {
			return nil, fmt.Errorf("duplicate updated array identity")
		}
		seen[identity] = true
		matches := lookup[identity]
		if len(matches) == 0 {
			unmatched = append(unmatched, index)
			continue
		}
		previous := matches[0]
		indices[index], used[previous] = previous, true
		if key == "" {
			lookup[identity] = matches[1:]
		}
	}
	if key != "" || len(unmatched) == 0 {
		return indices, nil
	}
	var remaining []int
	for index := range before {
		if !used[index] {
			remaining = append(remaining, index)
		}
	}
	if len(remaining) == 0 {
		return indices, nil
	}
	if len(remaining) == 1 && len(unmatched) == 1 {
		indices[unmatched[0]] = remaining[0]
		return indices, nil
	}
	return nil, fmt.Errorf("ambiguous native array edits; declare native.arrayKeys for stable item identities")
}

func nativeItemIdentity(item Value, key string) (string, error) {
	if key != "" {
		pointer, err := parsePointer(key)
		if err != nil {
			return "", err
		}
		item, err = readValuePointer(item, pointer)
		if err != nil {
			return "", err
		}
		if item.IsZero() || item.IsNull() || item.IsObject() || valueType(item) == ArrayType {
			return "", fmt.Errorf("native array identity must be a present scalar")
		}
	}
	var decoded any
	if err := item.Decode(&decoded); err != nil {
		return "", err
	}
	canonical, err := EncodeValue(decoded)
	if err != nil {
		return "", err
	}
	return string(canonical.Bytes()), nil
}

// Compare immutable subvalues without decoding unrelated large strings. Only
// the root may bypass numeric validation when its complete spelling is equal.
func equalNativeValues(left, right Value) bool {
	if left.raw == right.raw {
		return true
	}
	if left.IsZero() || right.IsZero() {
		return false
	}
	leftReader, rightReader := newNativeComparisonReader(left), newNativeComparisonReader(right)
	return equalNativeRoots(left, right, leftReader, rightReader)
}

func newNativeComparisonReader(root Value) *valueReader {
	// Comparison returns no views. Borrow even small fragments for its duration,
	// retaining the root index so nested containers do not rescan large payloads.
	reader := requestValueReader(root)
	reader.minimumSharedBytes = 0
	return reader
}

func equalNativeRoots(left, right Value, leftReader, rightReader *valueReader) bool {
	if left.IsObject() && right.IsObject() {
		before, _ := left.readObjectWithin(leftReader)
		after, _ := right.readObjectWithin(rightReader)
		if len(before) != len(after) {
			return false
		}
		// Preserve the original short-field precheck: a model change should not
		// traverse unrelated history first just because map order changes.
		for key, value := range before {
			other, exists := after[key]
			if !exists || len(value.raw) <= requestViewMinBytes && len(other.raw) <= requestViewMinBytes && !equalValues(value, other) {
				return false
			}
		}
		return equalNativeObjectFields(before, after, leftReader, rightReader)
	}
	return equalNativeValuesWithin(left, right, leftReader, rightReader)
}

func equalNativeValuesWithin(left, right Value, leftReader, rightReader *valueReader) bool {
	leftRaw, rightRaw := strings.TrimSpace(left.raw), strings.TrimSpace(right.raw)
	switch leftRaw[0] {
	case '{':
		if rightRaw[0] != '{' {
			return false
		}
		before, _ := left.readObjectWithin(leftReader)
		after, _ := right.readObjectWithin(rightReader)
		return equalNativeObjectFields(before, after, leftReader, rightReader)
	case '[':
		if rightRaw[0] != '[' {
			return false
		}
		before, _ := left.readArrayWithin(leftReader)
		after, _ := right.readArrayWithin(rightReader)
		if len(before) != len(after) {
			return false
		}
		for index, value := range before {
			if !equalNativeValuesWithin(value, after[index], leftReader, rightReader) {
				return false
			}
		}
		return true
	case '"':
		if rightRaw[0] != '"' {
			return false
		}
		if leftRaw == rightRaw {
			return true
		}
		var before, after string
		return left.Decode(&before) == nil && right.Decode(&after) == nil && before == after
	case 'n', 't', 'f':
		return leftRaw == rightRaw
	default:
		if rightRaw[0] != '-' && (rightRaw[0] < '0' || rightRaw[0] > '9') {
			return false
		}
		before, ok := exactNumber(leftRaw)
		if !ok {
			return false
		}
		after, ok := exactNumber(rightRaw)
		return ok && before.Cmp(after) == 0
	}
}

func equalNativeObjectFields(before, after Object, leftReader, rightReader *valueReader) bool {
	if len(before) != len(after) {
		return false
	}
	for key, value := range before {
		other, exists := after[key]
		if !exists || !equalNativeValuesWithin(value, other, leftReader, rightReader) {
			return false
		}
	}
	return true
}
