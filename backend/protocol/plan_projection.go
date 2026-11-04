package protocol

import (
	"fmt"
	"slices"
)

func (plan *compiledOutputPlan) buildProjection(state evaluation) (plannedOutput, error) {
	source, err := plan.source.buildNode(state)
	if err != nil {
		return plannedOutput{}, err
	}
	return plan.expression.projectObject(source.output)
}

// Projection owns its field container while retaining immutable member values.
// Object reading supplies normalized keys, last-wins duplicates and sorted order.
func (expression *compiledExpression) projectObject(source JSONOutput) (plannedOutput, error) {
	if source.node == nil {
		return expression.projectRawObject(source.value)
	}
	fields, err := plannedObjectFields(source)
	if err != nil {
		return plannedOutput{}, fmt.Errorf("%s/source: %w", expression.path, err)
	}
	isInclude := expression.policy == "include"
	capacity := len(fields)
	if isInclude {
		capacity = min(capacity, len(expression.keys))
	}
	node := &jsonOutputNode{object: true, hasCanonicalKeys: true, hasSortedFields: true, fields: make([]jsonOutputField, 0, capacity)}
	for _, field := range fields {
		_, isSelected := slices.BinarySearch(expression.keys, field.key)
		if isSelected == isInclude {
			node.fields = append(node.fields, field)
		}
	}
	output := JSONOutput{node: node}
	node.encodedBytes = normalizedOutputBytes(output)
	return plannedOutput{output, node.encodedBytes}, nil
}

func (expression *compiledExpression) projectRawObject(source Value) (plannedOutput, error) {
	// Read every key before selection so decoded duplicates keep their last
	// value. Excluded members never enter a container that needs normalization.
	fields, err := source.readObjectWithin(&valueReader{})
	if err != nil {
		return plannedOutput{}, fmt.Errorf("%s/source: %w", expression.path, err)
	}
	isInclude := expression.policy == "include"
	capacity := len(fields)
	if isInclude {
		capacity = min(capacity, len(expression.keys))
	}
	node := &jsonOutputNode{object: true, hasCanonicalKeys: true, hasSortedFields: true, fields: make([]jsonOutputField, 0, capacity)}
	for _, key := range sortedKeys(fields) {
		_, isSelected := slices.BinarySearch(expression.keys, key)
		if isSelected != isInclude {
			continue
		}
		value := fields[key]
		value.raw = compactValueString(value.raw)
		node.fields = append(node.fields, jsonOutputField{key, JSONLeaf(value)})
	}
	output := JSONOutput{node: node}
	node.encodedBytes = normalizedOutputBytes(output)
	return plannedOutput{output, node.encodedBytes}, nil
}
