package protocol

import "fmt"

func (plan *compiledOutputPlan) buildOperands(state evaluation) (plannedOutput, error) {
	operands := make([]plannedOutput, 0, len(plan.items))
	logicalBytes := 0
	// Evaluate all operands before checking their container types or collisions,
	// preserving the expression evaluator's first-error order.
	for _, item := range plan.items {
		value, err := item.build(state)
		if err != nil {
			return plannedOutput{}, err
		}
		logicalBytes += value.bytes + 1
		if logicalBytes > state.budget.limits.BufferBytes {
			return plannedOutput{}, resourceFailure("evaluation", "bytes", state.budget.limits.BufferBytes, logicalBytes)
		}
		operands = append(operands, value)
	}
	if plan.expression.op == "merge" {
		return plan.mergeObjects(operands)
	}
	return concatPlannedArrays(operands, state.budget.limits.Nodes)
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
		return output.node.fields, nil
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
		value.raw = compactValueString(value.raw)
		items[index] = JSONLeaf(value)
	}
	return items, nil
}

func concatPlannedArrays(operands []plannedOutput, nodeLimit int) (plannedOutput, error) {
	node := &jsonOutputNode{items: []JSONOutput{}}
	for _, operand := range operands {
		items, err := plannedArrayItems(operand.output)
		if err != nil {
			return plannedOutput{}, err
		}
		node.items = append(node.items, items...)
		if len(node.items) > nodeLimit {
			return plannedOutput{}, resourceFailure("evaluation", "nodes", nodeLimit, len(node.items))
		}
	}
	output := JSONOutput{node: node}
	return plannedOutput{output, normalizedOutputBytes(output)}, nil
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
	source, err := expression.source.evaluate(state)
	if err != nil {
		return plannedOutput{}, err
	}
	if !source.IsArray() {
		return plannedOutput{}, fmt.Errorf("%s/source: expected an array", expression.path)
	}
	items, err := source.readArrayWithin(&valueReader{})
	if err != nil {
		return plannedOutput{}, err
	}
	node := &jsonOutputNode{items: make([]JSONOutput, 0, len(items))}
	bytes := 2
	for _, item := range items {
		child := state
		child.item = item
		value, err := expression.when.evaluate(child)
		if err != nil {
			return plannedOutput{}, err
		}
		isMatch, err := readBoolean(value)
		if err != nil {
			return plannedOutput{}, err
		}
		if isMatch {
			bytes += appendPlannedItem(node, plannedOutput{JSONLeaf(item), item.Len()})
		}
	}
	return plannedOutput{JSONOutput{node: node}, bytes}, nil
}
