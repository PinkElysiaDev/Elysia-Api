package protocol

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
)

type compiledPackageIndex struct {
	keyMode, policy, path string
	schema                *wireSchemaPlan
	canRetainSource       bool
}

type packageIndexUpdate struct {
	values    map[string]Value
	additions int
}

func (transaction *packageStreamTransaction) indexSize(slot int) int {
	count := 0
	if transaction.previous != nil {
		count = len(transaction.previous.indexes[slot])
	}
	if transaction.indexUpdates != nil {
		count += transaction.indexUpdates[slot].additions
	}
	return count
}

func compilePackageIndexes(declarations map[string]ProtocolStateIndex, direction Direction, path string, limits Limits, scope *expressionScope) ([]compiledPackageIndex, error) {
	if len(declarations) == 0 {
		return nil, nil
	}
	if !isEventDirection(direction) || len(declarations) > limits.StateItems {
		return nil, fmt.Errorf("%s: indexes require an event direction and bounded table count", path)
	}
	indexes := make([]compiledPackageIndex, 0, len(declarations))
	scope.streamIndexes = make(map[string]int, len(declarations))
	for _, name := range sortedKeys(declarations) {
		declaration := declarations[name]
		at := path + "/" + escapePointer(name)
		if declaration.CanRetainSource && !isEventDecoder(direction) {
			return nil, fmt.Errorf("%s/retainSource: source retention requires an event decoder", at)
		}
		if !definitionIdentifier.MatchString(name) || !slices.Contains([]string{"string", "integer", "raw_json"}, declaration.KeyMode) || !slices.Contains([]string{"first", "last", "reject"}, declaration.Policy) {
			return nil, fmt.Errorf("%s: requires an identifier, explicit keyMode and duplicate policy", at)
		}
		schema, err := compileWireSchemaPlan(&declaration.Schema, limits)
		if err != nil {
			return nil, locateMappingError(at+"/schema", err)
		}
		scope.streamIndexes[name] = len(indexes)
		indexes = append(indexes, compiledPackageIndex{keyMode: declaration.KeyMode, policy: declaration.Policy, path: at, schema: schema, canRetainSource: declaration.CanRetainSource})
	}
	return indexes, nil
}

func (index compiledPackageIndex) key(output JSONOutput) (string, error) {
	switch index.keyMode {
	case "raw_json":
		return rawIndexKey(output)
	case "string":
		return readPlannedString(output)
	case "integer":
		if output.node != nil {
			return "", fmt.Errorf("integer index key requires a nonnegative integer literal")
		}
		value, err := strconv.ParseInt(strings.TrimSpace(output.value.raw), 10, 64)
		if err != nil || value < 0 {
			return "", fmt.Errorf("integer index key requires a nonnegative integer literal")
		}
		return strconv.FormatInt(value, 10), nil
	}
	panic("uncompiled persistent index key mode")
}

func (transaction *packageStreamTransaction) lookupIndex(slot int, key string) (Value, bool) {
	if transaction.indexUpdates != nil {
		if value, exists := transaction.indexUpdates[slot].values[key]; exists {
			return value, true
		}
	}
	if transaction.previous != nil {
		value, exists := transaction.previous.indexes[slot][key]
		return value, exists
	}
	return Value{}, false
}

func (transaction *packageStreamTransaction) recallIndex(slot int, output JSONOutput) (JSONOutput, bool, error) {
	key, err := transaction.plan.indexes[slot].key(output)
	if err != nil {
		return JSONOutput{}, false, err
	}
	value, exists := transaction.lookupIndex(slot, key)
	return transaction.indexOutput(slot, key, value), exists, nil
}

func (transaction *packageStreamTransaction) rememberIndex(state evaluation, slot int, keyOutput, output JSONOutput) error {
	index := transaction.plan.indexes[slot]
	key, err := index.key(keyOutput)
	if err != nil {
		return err
	}
	previous, exists := transaction.lookupIndex(slot, key)
	if exists {
		switch index.policy {
		case "first":
			return nil
		case "reject":
			return fmt.Errorf("duplicate index key")
		}
	}
	var value Value
	if !output.IsZero() {
		value, err = output.Encode(state.ctx)
		if err != nil {
			return err
		}
		if err := index.schema.validate(state.ctx, value, index.path, state.budget.limits); err != nil {
			return err
		}
	}
	items, bytes := transaction.indexItemDelta, transaction.indexByteDelta+len(value.raw)-len(previous.raw)
	if !exists {
		items++
		bytes += len(key)
	}
	retainedItems := transaction.owner.packageStateItems + items
	if retainedItems > state.budget.limits.StateItems {
		return resourceFailure("stream", "state_items", state.budget.limits.StateItems, retainedItems)
	}
	if err := transaction.checkRetainedBytes(bytes - transaction.indexByteDelta); err != nil {
		return err
	}
	if transaction.indexUpdates == nil {
		transaction.indexUpdates = make([]packageIndexUpdate, len(transaction.plan.indexes))
	}
	if transaction.indexUpdates[slot].values == nil {
		transaction.indexUpdates[slot].values = make(map[string]Value)
	}
	// All leaves point into independently owned JSON. Only an explicitly
	// declared source table retains genuine capture tags; Native stays plain.
	value.raw = strings.Clone(value.raw)
	ownedKey := strings.Clone(key)
	if index.canRetainSource {
		source, err := retainIndexSources(state.ctx, output, value)
		if err != nil {
			return err
		}
		if transaction.indexSourceUpdates == nil {
			transaction.indexSourceUpdates = make([]map[string]JSONOutput, len(transaction.plan.indexes))
		}
		if transaction.indexSourceUpdates[slot] == nil {
			transaction.indexSourceUpdates[slot] = make(map[string]JSONOutput)
		}
		transaction.indexSourceUpdates[slot][ownedKey] = source
	}
	transaction.indexUpdates[slot].values[ownedKey] = value
	if !exists {
		transaction.indexUpdates[slot].additions++
	}
	transaction.indexItemDelta, transaction.indexByteDelta = items, bytes
	return nil
}

func (transaction *packageStreamTransaction) commitIndexes() ([]map[string]Value, int, int) {
	var indexes []map[string]Value
	items, bytes := transaction.indexItemDelta, transaction.indexByteDelta
	if transaction.previous != nil {
		indexes = transaction.previous.indexes
		items += transaction.previous.indexItems
		bytes += transaction.previous.indexBytes
	} else if len(transaction.plan.indexes) != 0 {
		indexes = make([]map[string]Value, len(transaction.plan.indexes))
	}
	for slot, update := range transaction.indexUpdates {
		updates := update.values
		if len(updates) == 0 {
			continue
		}
		if indexes[slot] == nil {
			indexes[slot] = make(map[string]Value, len(updates))
		}
		for key, value := range updates {
			indexes[slot][key] = value
		}
	}
	return indexes, items, bytes
}

func (plan *compiledOutputPlan) readStreamIndexValues(state evaluation) (plannedOutput, error) {
	transaction, slot := state.streamTransaction, plan.expression.indexSlot
	var previous map[string]Value
	if transaction.previous != nil {
		previous = transaction.previous.indexes[slot]
	}
	keys := make([]string, 0, len(previous))
	for key := range previous {
		keys = append(keys, key)
	}
	if transaction.indexUpdates != nil {
		for key := range transaction.indexUpdates[slot].values {
			if _, exists := previous[key]; !exists {
				keys = append(keys, key)
			}
		}
	}
	slices.Sort(keys)
	node := &jsonOutputNode{items: make([]JSONOutput, 0, len(keys))}
	bytes := 2
	for _, key := range keys {
		if err := state.advance(); err != nil {
			return plannedOutput{}, err
		}
		value, _ := transaction.lookupIndex(slot, key)
		if value.IsZero() {
			continue
		}
		bytes += appendPlannedItem(node, plannedOutput{output: transaction.indexOutput(slot, key, value), bytes: value.Len()})
		if bytes > state.budget.limits.BufferBytes {
			return plannedOutput{}, resourceFailure("evaluation", "bytes", state.budget.limits.BufferBytes, bytes)
		}
	}
	return plannedOutput{output: JSONOutput{node: node}, bytes: bytes}, nil
}
