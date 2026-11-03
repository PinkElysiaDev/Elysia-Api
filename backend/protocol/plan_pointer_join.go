package protocol

import (
	"fmt"
	"strings"
)

func (plan *compiledOutputPlan) buildPointerJoin(state evaluation) (plannedOutput, error) {
	base, err := plan.source.buildNode(state)
	if err != nil {
		return plannedOutput{}, err
	}
	prefix, err := readPlannedString(base.output)
	if err != nil {
		return plannedOutput{}, fmt.Errorf("pointer prefix must be a string: %w", err)
	}
	if _, err := parsePointer(prefix); err != nil {
		return plannedOutput{}, err
	}
	var pointer strings.Builder
	pointer.WriteString(prefix)
	for _, child := range plan.items {
		value, err := child.buildNode(state)
		if err != nil {
			return plannedOutput{}, err
		}
		segment, err := readPlannedString(value.output)
		if err != nil {
			return plannedOutput{}, fmt.Errorf("pointer segment must be a string: %w", err)
		}
		bytes := 1 + len(segment) + strings.Count(segment, "~") + strings.Count(segment, "/")
		if bytes > state.budget.limits.BufferBytes-pointer.Len() {
			return plannedOutput{}, resourceFailure("evaluation", "bytes", state.budget.limits.BufferBytes, pointer.Len()+bytes)
		}
		pointer.WriteByte('/')
		pointer.WriteString(escapePointer(segment))
	}
	return plannedLeaf(StringValue(pointer.String())), nil
}
