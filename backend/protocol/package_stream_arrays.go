package protocol

import (
	"context"
	"strconv"
	"strings"
)

// Array entities retain only selected elements. Appends never re-encode or scan
// the existing prefix; complete wire snapshots use the shared prefix verifier.
func (state *packageStreamValidation) consumeEntityArray(ctx context.Context, item *wireEntityState, action compiledPackageStreamEvent, value Value, read func(streamField) (Value, error), reader *valueReader) error {
	definition := state.contract.entities[item.definition]
	if item.sealed && (action.action == wireEntityAppend || action.sealsPayload) {
		return state.itemIssue(action, action.delta.path, "array arrived after its completion", nil)
	}
	if action.offset.path != "" {
		position, err := read(action.offset)
		if err != nil {
			return err
		}
		offset, err := state.readCount(position, action.offset.path)
		if err != nil {
			return err
		}
		if offset != int64(len(item.array)) {
			return state.itemIssue(action, action.offset.path, "array position must append after its existing elements", nil)
		}
	}
	if value.IsNull() && !action.isArrayItem {
		if !definition.schema.nullable {
			return state.itemIssue(action, action.delta.path, "array payload cannot be null", nil)
		}
		if action.action != wireEntityAppend && (len(item.array) != 0 || item.isFinished && !item.arrayIsNull) {
			return state.itemIssue(action, action.delta.path, "null snapshot changed an established array", nil)
		}
		if !item.hasArrayValue || action.action != wireEntityAppend {
			item.arrayIsNull = true
		}
		item.hasArrayValue = true
		return state.retainPayloadLocation(item, action)
	}
	var values []Value
	if action.isArrayItem {
		if value.IsZero() {
			return state.itemIssue(action, action.delta.path, "array element is missing", nil)
		}
		values = []Value{value}
	} else {
		var err error
		values, err = value.readArrayWithin(reader)
		if err != nil || !value.IsArray() {
			return state.itemIssue(action, action.delta.path, "array payload requires an array", err)
		}
	}
	offset := 0
	if action.action != wireEntityAppend {
		if item.isFinished && item.arrayIsNull {
			return state.itemIssue(action, action.delta.path, "completed null payload changed to an array", nil)
		}
		var err error
		offset, err = verifyArrayPrefix(ctx, item.array, len(values), 0, func(index int) (Value, error) { return values[index], nil })
		if err != nil {
			return state.itemIssue(action, action.delta.path, "array snapshot changed its established prefix", err)
		}
	}
	if (item.isFinished || item.sealed) && offset != len(values) {
		return state.itemIssue(action, action.delta.path, "completed array acquired additional elements", nil)
	}
	for index, value := range values[offset:] {
		elementPath := action.delta.path
		if !action.isArrayItem {
			elementPath += "/" + strconv.Itoa(offset+index)
		}
		meter := valueTokenMeter{limits: state.limits, domain: "stream", depth: 1, nodes: item.arrayNodes + 2}
		if err := meter.scan(value.raw); err != nil {
			return state.itemIssue(action, action.delta.path, "retained array exceeds its resource limits", err)
		}
		if definition.arrayItems != nil {
			if err := definition.arrayItems.validateShape(ctx, value, elementPath, state.limits, reader); err != nil {
				return state.itemIssue(action, elementPath, "array element violates its declared schema", err)
			}
		}
		if err := state.checkRetainedState(state.identityBytes, state.snapshotBytes+value.Len(), action.delta.path); err != nil {
			return err
		}
		item.array = append(item.array, Value{raw: strings.Clone(value.raw)})
		item.arrayBytes += value.Len()
		item.arrayNodes = meter.nodes - 2
		state.bufferedBytes += value.Len()
	}
	if action.action != wireEntityAppend || len(values) != 0 || !item.hasArrayValue {
		item.hasArrayValue, item.arrayIsNull = true, false
	}
	return state.retainPayloadLocation(item, action)
}

func (state *packageStreamValidation) verifyEntityChildren(item *wireEntityState, action compiledPackageStreamEvent) error {
	for _, namespace := range action.verifiedChildren {
		for _, child := range item.children[namespace] {
			if child.verifiedFrame != state.frameNumber {
				return state.itemIssue(action, action.key.path, "entity snapshot omitted a registered child collection", nil)
			}
		}
	}
	return nil
}
