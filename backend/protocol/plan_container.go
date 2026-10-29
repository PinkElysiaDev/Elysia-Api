package protocol

import "fmt"

func (plan *compiledOutputPlan) buildOperands(state evaluation) (plannedOutput, error) {
	operands, err := plan.buildOperandValues(state)
	if err != nil {
		return plannedOutput{}, err
	}
	if plan.expression.op == "merge" {
		return plan.mergeObjects(operands)
	}
	return concatPlannedArrays(operands, state.budget.limits.Nodes)
}

func (plan *compiledOutputPlan) buildOperandValues(state evaluation) ([]plannedOutput, error) {
	operands := make([]plannedOutput, 0, len(plan.items))
	logicalBytes := 0
	// Evaluate all operands before checking their container types or collisions,
	// preserving the expression evaluator's first-error order.
	for _, item := range plan.items {
		value, err := item.build(state)
		if err != nil {
			return nil, err
		}
		logicalBytes += value.bytes + 1
		if logicalBytes > state.budget.limits.BufferBytes {
			return nil, resourceFailure("evaluation", "bytes", state.budget.limits.BufferBytes, logicalBytes)
		}
		operands = append(operands, value)
	}
	return operands, nil
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

func concatPlannedArrays(operands []plannedOutput, nodeLimit int) (plannedOutput, error) {
	node := &jsonOutputNode{items: []JSONOutput{}}
	bytes := 2
	for _, operand := range operands {
		items, err := plannedArrayItems(operand.output)
		if err != nil {
			return plannedOutput{}, err
		}
		for _, item := range items {
			bytes += appendPlannedItem(node, plannedOutput{item, normalizedOutputBytes(item)})
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
	source, err := plan.source.build(state)
	if err != nil {
		return plannedOutput{}, err
	}
	items, err := plannedArrayItems(source.output)
	if err != nil {
		return plannedOutput{}, fmt.Errorf("%s/source: %w", expression.path, err)
	}
	node := &jsonOutputNode{items: make([]JSONOutput, 0, len(items))}
	bytes := 2
	for index, item := range items {
		child := state
		child.itemIndex = index
		child.item = item.value
		child.itemOutput = nil
		if item.node != nil {
			child.itemOutput = &item
		}
		value, err := plan.when.build(child)
		if err != nil {
			return plannedOutput{}, err
		}
		isMatch, err := readPlannedBoolean(value.output)
		if err != nil {
			return plannedOutput{}, err
		}
		if isMatch {
			bytes += appendPlannedItem(node, plannedOutput{item, normalizedOutputBytes(item)})
		}
	}
	return plannedOutput{JSONOutput{node: node}, bytes}, nil
}
