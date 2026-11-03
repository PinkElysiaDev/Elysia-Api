package protocol

import "fmt"

func (plan *compiledOutputPlan) buildEntries(state evaluation) (plannedOutput, error) {
	source, err := plan.source.build(state)
	if err != nil {
		return plannedOutput{}, err
	}
	readFields := plannedObjectFields
	if plan.expression.policy == "preserve" {
		readFields = coreObjectFields
	}
	fields, err := readFields(source.output)
	if err != nil {
		return plannedOutput{}, err
	}
	node := &jsonOutputNode{items: make([]JSONOutput, 0, len(fields))}
	bytes := 2
	for _, field := range fields {
		if err := state.advance(); err != nil {
			return plannedOutput{}, err
		}
		entry := JSONOutput{node: &jsonOutputNode{object: true, fields: []jsonOutputField{
			{key: "key", value: JSONLeaf(StringValue(field.key))}, {key: "value", value: field.value},
		}}}
		bytes += appendPlannedItem(node, plannedOutput{output: entry, bytes: normalizedOutputBytes(entry)}, state.cache)
		if bytes > state.budget.limits.BufferBytes {
			return plannedOutput{}, resourceFailure("evaluation", "bytes", state.budget.limits.BufferBytes, bytes)
		}
	}
	return plannedOutput{output: JSONOutput{node: node}, bytes: bytes}, nil
}

func (plan *compiledOutputPlan) buildObjectEntries(state evaluation) (plannedOutput, error) {
	source, err := plan.source.build(state)
	if err != nil {
		return plannedOutput{}, err
	}
	items, err := plannedArrayItems(source.output)
	if err != nil {
		return plannedOutput{}, err
	}
	fields := make(map[string]JSONOutput, len(items))
	for _, item := range items {
		if err := state.advance(); err != nil {
			return plannedOutput{}, err
		}
		members, err := plannedObjectFields(item)
		if err != nil {
			return plannedOutput{}, err
		}
		if len(members) != 2 || members[0].key != "key" || members[1].key != "value" {
			return plannedOutput{}, fmt.Errorf("object entry requires exactly key and value")
		}
		key, err := readPlannedString(members[0].value)
		if err != nil {
			return plannedOutput{}, err
		}
		value := members[1].value
		if previous, exists := fields[key]; exists && plan.expression.policy != "last" {
			if plan.expression.policy == "identical" {
				isEqual, err := equalJSONOutputBytes(state.ctx, previous, value)
				if err != nil {
					return plannedOutput{}, err
				}
				if isEqual {
					continue
				}
			}
			conflict := &ObjectMergeConflictError{Path: "/" + escapePointer(key)}
			if diagnostic := plan.expression.onConflict; diagnostic != nil {
				return plannedOutput{}, &mergeConflictDiagnosticError{diagnostic: diagnostic, conflict: conflict}
			}
			return plannedOutput{}, conflict
		}
		fields[key] = value
	}
	output := JSONObject(fields)
	return plannedOutput{output: output, bytes: normalizedOutputBytes(output)}, nil
}
