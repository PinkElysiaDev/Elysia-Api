package protocol

func (plan *compiledOutputPlan) buildBinding(state evaluation) (plannedOutput, error) {
	source, err := plan.source.build(state)
	if err != nil {
		return plannedOutput{}, err
	}
	slot := plan.expression.bindingSlot
	previous := state.bindings[slot]
	state.bindings[slot] = source.output
	output, err := plan.body.build(state)
	state.bindings[slot] = previous
	return output, err
}
