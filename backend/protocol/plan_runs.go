package protocol

import "fmt"

func (plan *compiledOutputPlan) buildRuns(state evaluation) (plannedOutput, error) {
	source, err := plan.source.build(state)
	if err != nil {
		return plannedOutput{}, err
	}
	items, err := plannedArrayItems(source.output)
	if err != nil {
		return plannedOutput{}, fmt.Errorf("%s/source: %w", plan.expression.path, err)
	}
	groups := &jsonOutputNode{}
	var run *jsonOutputNode
	var previous bool
	bytes := 2
	for index, item := range items {
		child := state
		child.itemIndex, child.item, child.itemOutput = index, item.value, nil
		if item.node != nil {
			child.itemOutput = &item
		}
		value, err := plan.when.build(child)
		if err != nil {
			return plannedOutput{}, err
		}
		isMatch, err := readPlannedBoolean(value.output)
		if err != nil {
			return plannedOutput{}, fmt.Errorf("%s/when: %w", plan.expression.path, err)
		}
		if run == nil || isMatch != previous {
			run = &jsonOutputNode{}
			bytes += appendPlannedItem(groups, plannedOutput{output: JSONOutput{node: run}, bytes: 2})
			previous = isMatch
		}
		// Groups own only the new array containers. Original members remain
		// immutable views, so a large text or tool payload is not encoded here.
		bytes += appendPlannedItem(run, plannedOutput{output: item, bytes: normalizedOutputBytes(item)})
		if bytes > state.budget.limits.BufferBytes {
			return plannedOutput{}, resourceFailure("evaluation", "bytes", state.budget.limits.BufferBytes, bytes)
		}
	}
	return plannedOutput{output: JSONOutput{node: groups}, bytes: bytes}, nil
}
