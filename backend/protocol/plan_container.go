package protocol

import (
	"cmp"
	"fmt"
	"slices"
)

func (plan *compiledOutputPlan) buildOperands(state evaluation) (plannedOutput, error) {
	var operands []plannedOutput
	var err error
	if len(plan.items) <= scalarOperandCapacity {
		var storage [scalarOperandCapacity]plannedOutput
		operands = storage[:len(plan.items)]
		err = plan.buildOperandValuesInto(state, operands)
	} else {
		operands, err = plan.buildOperandValues(state)
	}
	if err != nil {
		return plannedOutput{}, err
	}
	if plan.expression.op == "merge" {
		return plan.mergeObjects(operands)
	}
	return concatPlannedArrays(operands, state.budget.limits.Nodes, state.cache)
}

func (plan *compiledOutputPlan) buildOperandValues(state evaluation) (operands []plannedOutput, err error) {
	operands = make([]plannedOutput, len(plan.items))
	err = plan.buildOperandValuesInto(state, operands)
	if err != nil {
		operands = nil
	}
	return
}

func (plan *compiledOutputPlan) buildOperandValuesInto(state evaluation, operands []plannedOutput) error {
	logicalBytes := 0
	// Evaluate all operands before checking their container types or collisions,
	// preserving the expression evaluator's first-error order.
	for index, item := range plan.items {
		value, err := item.buildNode(state)
		if err != nil {
			return err
		}
		logicalBytes += value.bytes + 1
		if logicalBytes > state.budget.limits.BufferBytes {
			return resourceFailure("evaluation", "bytes", state.budget.limits.BufferBytes, logicalBytes)
		}
		operands[index] = value
	}
	return nil
}

func (plan *compiledOutputPlan) mergeObjects(operands []plannedOutput) (plannedOutput, error) {
	fields := make(map[string]JSONOutput)
	for _, operand := range operands {
		selected, err := plannedObjectFields(operand.output)
		if err != nil {
			return plannedOutput{}, err
		}
		for _, field := range selected {
			if _, exists := fields[field.key]; exists {
				switch plan.expression.policy {
				case "reject":
					return plannedOutput{}, fmt.Errorf("%s: conflicting merge field %q", plan.expression.path, field.key)
				case "first":
					continue
				}
			}
			fields[field.key] = field.value
		}
	}
	output := JSONObject(fields)
	return plannedOutput{output, normalizedOutputBytes(output)}, nil
}

func plannedObjectFields(output JSONOutput) ([]jsonOutputField, error) {
	if output.node != nil {
		if !output.node.object {
			return nil, fmt.Errorf("expected JSON object")
		}
		if output.node.hasCanonicalKeys {
			if output.node.hasSortedFields {
				return output.node.fields, nil
			}
			fields := slices.Clone(output.node.fields)
			slices.SortFunc(fields, func(a, b jsonOutputField) int { return cmp.Compare(a.key, b.key) })
			return fields, nil
		}
		// Reading an encoded object folds repeated decoded keys before merge's
		// collision policy runs. Core maps may contain distinct invalid UTF-8
		// keys that encode to the same spelling; preserve last-wins and order.
		fields := make(map[string]JSONOutput, len(output.node.fields))
		for _, field := range output.node.fields {
			fields[coreJSONKey(field.key)] = field.value
		}
		return JSONObject(fields).node.fields, nil
	}
	fields, err := output.value.readObjectWithin(&valueReader{})
	if err != nil {
		return nil, err
	}
	selected := make([]jsonOutputField, 0, len(fields))
	for _, key := range sortedKeys(fields) {
		value := fields[key]
		value.raw = compactValueString(value.raw)
		selected = append(selected, jsonOutputField{key, JSONLeaf(value)})
	}
	return selected, nil
}

func plannedArrayItems(output JSONOutput) ([]JSONOutput, error) {
	if output.node != nil {
		if output.node.object {
			return nil, fmt.Errorf("expected an array")
		}
		return output.node.items, nil
	}
	if !output.value.IsArray() {
		return nil, fmt.Errorf("expected an array")
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

func concatPlannedArrays(operands []plannedOutput, nodeLimit int, cache *semanticCache) (plannedOutput, error) {
	node := &jsonOutputNode{items: []JSONOutput{}}
	bytes := 2
	for _, operand := range operands {
		items, err := plannedArrayItems(operand.output)
		if err != nil {
			return plannedOutput{}, err
		}
		for _, item := range items {
			bytes += appendPlannedItem(node, plannedOutput{item, normalizedOutputBytes(item)}, cache)
		}
		if len(node.items) > nodeLimit {
			return plannedOutput{}, resourceFailure("evaluation", "nodes", nodeLimit, len(node.items))
		}
	}
	output := JSONOutput{node: node}
	return plannedOutput{output, bytes}, nil
}

// Intermediate logical sizes remain observable through resource limits even
// when the corresponding bytes are never materialized.
func normalizedOutputBytes(output JSONOutput) int {
	if output.node == nil {
		return output.value.Len()
	}
	node := output.node
	if node.encodedBytes != 0 {
		return node.encodedBytes
	}
	bytes, count := 2, len(node.items)
	if node.object {
		count = len(node.fields)
		for _, field := range node.fields {
			bytes += jsonStringBytes(field.key) + 1 + normalizedOutputBytes(field.value)
		}
	} else {
		for _, item := range node.items {
			bytes += normalizedOutputBytes(item)
		}
	}
	return bytes + max(0, count-1)
}

func (plan *compiledOutputPlan) buildFilter(state evaluation) (plannedOutput, error) {
	expression := plan.expression
	source, err := plan.source.buildNode(state)
	if err != nil {
		return plannedOutput{}, err
	}
	items, err := plannedArrayItems(source.output)
	if err != nil {
		return plannedOutput{}, fmt.Errorf("%s/source: %w", expression.path, err)
	}
	node := &jsonOutputNode{items: make([]JSONOutput, 0, len(items))}
	bytes := 2
	shouldCapture := plan.when.needsItemCapture(state.capture, state.walkBody)
	for index, item := range items {
		child := state
		child.capture = nil
		if shouldCapture {
			child.capture = state.capture.forItem(plan.source.expression, index)
		}
		child.itemIndex = index
		child.item = item.value
		child.itemOutput = nil
		if item.node != nil {
			child.itemOutput = &item
		}
		value, err := plan.when.buildNode(child)
		if err != nil {
			return plannedOutput{}, err
		}
		isMatch, err := readPlannedBoolean(value.output)
		if err != nil {
			return plannedOutput{}, err
		}
		if isMatch {
			bytes += appendPlannedItem(node, plannedOutput{item, normalizedOutputBytes(item)}, state.cache)
		}
	}
	return plannedOutput{JSONOutput{node: node}, bytes}, nil
}
