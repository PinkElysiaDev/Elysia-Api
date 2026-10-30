package protocol

import "fmt"

// ObjectMergeConflictError identifies a repeated non-object value. Path is an
// RFC 6901 pointer relative to the merged object; no operand values are exposed.
type ObjectMergeConflictError struct {
	Path string
}

func (failure *ObjectMergeConflictError) Error() string {
	return fmt.Sprintf("deep object merge conflicts at %s", failure.Path)
}

type deepObjectMerge struct {
	state   evaluation
	members int
}

func (plan *compiledOutputPlan) buildDeepMerge(state evaluation) (plannedOutput, error) {
	operands, err := plan.buildOperandValues(state)
	if err != nil {
		return plannedOutput{}, err
	}
	merge := deepObjectMerge{state: state}
	output, err := merge.objects(operands, "", 1)
	if err != nil {
		if conflict, ok := err.(*ObjectMergeConflictError); ok && plan.expression.onConflict != nil {
			err = &mergeConflictDiagnosticError{diagnostic: plan.expression.onConflict, conflict: conflict}
		}
		return plannedOutput{}, err
	}
	return plannedOutput{output: output, bytes: normalizedOutputBytes(output)}, nil
}

func (merge *deepObjectMerge) objects(operands []plannedOutput, path string, depth int) (JSONOutput, error) {
	if err := merge.state.ctx.Err(); err != nil {
		return JSONOutput{}, err
	}
	if depth > merge.state.budget.limits.Depth {
		return JSONOutput{}, resourceFailure("evaluation", "depth", merge.state.budget.limits.Depth, depth)
	}
	fields := make(map[string]JSONOutput)
	for _, operand := range operands {
		selected, err := plannedObjectFields(operand.output)
		if err != nil {
			return JSONOutput{}, err
		}
		for _, field := range selected {
			merge.members++
			if merge.members > merge.state.budget.limits.Nodes {
				return JSONOutput{}, resourceFailure("evaluation", "nodes", merge.state.budget.limits.Nodes, merge.members)
			}
			if err := merge.state.advance(); err != nil {
				return JSONOutput{}, err
			}
			value := field.value
			if previous, exists := fields[field.key]; exists {
				childPath := path + "/" + escapePointer(field.key)
				if plannedJSONType(previous) != "object" || plannedJSONType(value) != "object" {
					return JSONOutput{}, &ObjectMergeConflictError{Path: childPath}
				}
				pair := [2]plannedOutput{{output: previous}, {output: value}}
				value, err = merge.objects(pair[:], childPath, depth+1)
				if err != nil {
					return JSONOutput{}, err
				}
			}
			fields[field.key] = value
		}
	}
	// New containers carry no origin tag. Unchanged children remain immutable
	// references; only overlapping object pairs acquire replacement containers.
	return JSONObject(fields), nil
}
