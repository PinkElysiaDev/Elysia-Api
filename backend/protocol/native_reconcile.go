package protocol

import (
	"context"
	"fmt"
)

// reconcileNative applies semantic differences to the received wire document.
// Fields absent from both rendered versions belong to the original wire node.
// Arrays need a declared identity when several changed nodes carry extensions;
// guessing their association would move extensions to the wrong tool/message.
func reconcileNative(ctx context.Context, original, before, after Value, policy NativePolicy, limits Limits) (Value, error) {
	state := nativeReconciler{ctx: ctx, policy: policy, limits: limits}
	value, err := state.merge(original, before, after, "", 1)
	if err != nil {
		return Value{}, err
	}
	if err := checkValueLimits(value, limits); err != nil {
		return Value{}, err
	}
	return value, nil
}

type nativeReconciler struct {
	ctx    context.Context
	policy NativePolicy
	limits Limits
	nodes  int
}

func (state *nativeReconciler) merge(original, before, after Value, path string, depth int) (Value, error) {
	if err := state.ctx.Err(); err != nil {
		return Value{}, err
	}
	state.nodes++
	if depth > state.limits.Depth || state.nodes > state.limits.Nodes {
		return Value{}, fmt.Errorf("%s: native reconciliation exceeds limits", path)
	}
	if equalValues(before, after) {
		return original, nil
	}
	// A fully represented subtree has no unmapped native fields to associate.
	// Its semantic replacement is authoritative, including reorders and deletions.
	if equalValues(original, before) {
		return after, nil
	}
	if original.IsObject() && before.IsObject() && after.IsObject() {
		return state.mergeObject(original, before, after, path, depth)
	}
	if valueType(original) == ArrayType && valueType(before) == ArrayType && valueType(after) == ArrayType {
		return state.mergeArray(original, before, after, path, depth)
	}
	return after, nil
}

func (state *nativeReconciler) mergeObject(original, before, after Value, path string, depth int) (Value, error) {
	// Container types were checked at the boundary; Value is already valid JSON.
	fields, _ := original.ReadObject()
	oldFields, _ := before.ReadObject()
	newFields, _ := after.ReadObject()
	for key := range oldFields {
		if _, exists := newFields[key]; !exists {
			delete(fields, key)
		}
	}
	for _, key := range sortedKeys(newFields) {
		oldValue, wasPresent := oldFields[key]
		newValue := newFields[key]
		if !wasPresent {
			fields[key] = newValue
			continue
		}
		merged, err := state.merge(fields[key], oldValue, newValue, path+"/"+escapePointer(key), depth+1)
		if err != nil {
			return Value{}, err
		}
		if !merged.IsZero() {
			fields[key] = merged
		}
	}
	return EncodeValue(fields)
}

func (state *nativeReconciler) mergeArray(original, before, after Value, path string, depth int) (Value, error) {
	items, _ := readArray(original)
	oldItems, _ := readArray(before)
	newItems, _ := readArray(after)
	if len(newItems) == 0 {
		return after, nil
	}
	if len(items) != len(oldItems) {
		return Value{}, fmt.Errorf("%s: decoder/encoder changed native array cardinality; an explicit mapping is required", path)
	}
	indices, err := matchNativeItems(oldItems, newItems, state.policy.ArrayKeys[path])
	if err != nil {
		return Value{}, fmt.Errorf("%s: %w", path, err)
	}
	result := make([]Value, len(newItems))
	for index, item := range newItems {
		previous := indices[index]
		if previous < 0 {
			result[index] = item
			continue
		}
		merged, err := state.merge(items[previous], oldItems[previous], item, fmt.Sprintf("%s/%d", path, index), depth+1)
		if err != nil {
			return Value{}, err
		}
		result[index] = merged
	}
	return EncodeValue(result)
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
