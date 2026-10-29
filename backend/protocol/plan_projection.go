package protocol

import (
	"fmt"
	"slices"
)

func (plan *compiledOutputPlan) buildProjection(state evaluation) (plannedOutput, error) {
	source, err := plan.source.build(state)
	if err != nil {
		return plannedOutput{}, err
	}
	return plan.expression.projectObject(source.output)
}

// Projection owns its field container while retaining immutable member values.
// Object reading supplies normalized keys, last-wins duplicates and sorted order.
func (expression *compiledExpression) projectObject(source JSONOutput) (plannedOutput, error) {
	fields, err := plannedObjectFields(source)
	if err != nil {
		return plannedOutput{}, fmt.Errorf("%s/source: %w", expression.path, err)
	}
	isInclude := expression.policy == "include"
	capacity := len(fields)
	if isInclude {
		capacity = min(capacity, len(expression.keys))
	}
	node := &jsonOutputNode{object: true, fields: make([]jsonOutputField, 0, capacity)}
	for _, field := range fields {
		_, isSelected := slices.BinarySearch(expression.keys, field.key)
		if isSelected == isInclude {
			node.fields = append(node.fields, field)
		}
	}
	output := JSONOutput{node: node}
	return plannedOutput{output, normalizedOutputBytes(output)}, nil
}
