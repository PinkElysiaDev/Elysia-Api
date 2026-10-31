package protocol

import "fmt"

// Index state belongs to one execution, and is allocated only when its lexical
// scope runs. Counts cover live frames; stored output trees remain immutable.
type indexEvaluationState struct {
	frames  []*outputIndex
	entries int
	bytes   int
}

type outputIndex struct {
	values map[string]plannedOutput
	policy string
	bytes  int
}

func (plan *compiledOutputPlan) buildIndexScope(state evaluation) (plannedOutput, error) {
	if state.indexes == nil {
		state.indexes = &indexEvaluationState{frames: make([]*outputIndex, plan.indexSlots)}
	}
	slot := plan.expression.indexSlot
	previous := state.indexes.frames[slot]
	index := &outputIndex{values: make(map[string]plannedOutput), policy: plan.expression.policy}
	state.indexes.frames[slot] = index
	output, err := plan.body.build(state)
	state.indexes.frames[slot] = previous
	state.indexes.entries -= len(index.values)
	state.indexes.bytes -= index.bytes
	return output, err
}

func (plan *compiledOutputPlan) rememberValue(state evaluation) (plannedOutput, error) {
	keyOutput, err := plan.source.build(state)
	if err != nil {
		return plannedOutput{}, err
	}
	value, err := plan.body.build(state)
	if err != nil {
		return plannedOutput{}, err
	}
	if plan.expression.isStreamIndex {
		if err := state.streamTransaction.rememberIndex(state, plan.expression.indexSlot, keyOutput.output, value.output); err != nil {
			return plannedOutput{}, err
		}
		return value, nil
	}
	key, err := rawIndexKey(keyOutput.output)
	if err != nil {
		return plannedOutput{}, err
	}
	index := state.indexes.frames[plan.expression.indexSlot]
	previous, exists := index.values[key]
	if exists {
		switch index.policy {
		case "reject":
			return plannedOutput{}, fmt.Errorf("duplicate index key")
		case "first":
			return value, nil
		}
	}
	entries, extraBytes := state.indexes.entries, value.bytes-previous.bytes
	if !exists {
		entries++
		extraBytes += len(key)
	}
	if entries > state.budget.limits.Nodes {
		return plannedOutput{}, resourceFailure("evaluation", "nodes", state.budget.limits.Nodes, entries)
	}
	bytes := state.indexes.bytes + extraBytes
	if bytes > state.budget.limits.BufferBytes {
		return plannedOutput{}, resourceFailure("evaluation", "bytes", state.budget.limits.BufferBytes, bytes)
	}
	index.values[key] = value
	index.bytes += extraBytes
	state.indexes.entries, state.indexes.bytes = entries, bytes
	return value, nil
}

func (plan *compiledOutputPlan) recallValue(state evaluation) (plannedOutput, error) {
	keyOutput, err := plan.source.build(state)
	if err != nil {
		return plannedOutput{}, err
	}
	if plan.expression.isStreamIndex {
		value, exists, err := state.streamTransaction.recallIndex(plan.expression.indexSlot, keyOutput.output)
		if err != nil {
			return plannedOutput{}, err
		}
		if !exists && plan.expression.policy == "reject" {
			return plannedOutput{}, fmt.Errorf("index lookup has no recorded key")
		}
		return plannedLeaf(value), nil
	}
	key, err := rawIndexKey(keyOutput.output)
	if err != nil {
		return plannedOutput{}, err
	}
	value, exists := state.indexes.frames[plan.expression.indexSlot].values[key]
	if !exists && plan.expression.policy == "reject" {
		return plannedOutput{}, fmt.Errorf("index lookup has no recorded key")
	}
	return value, nil
}

func rawIndexKey(output JSONOutput) (string, error) {
	if output.node != nil {
		return "", fmt.Errorf("raw_json index key requires existing JSON; constructed containers are not encoded")
	}
	// Missing is the empty key, distinct from JSON null. Keep escape and number
	// spelling exactly as read, before any enclosing constructor normalizes it.
	return output.value.JSONString(), nil
}
