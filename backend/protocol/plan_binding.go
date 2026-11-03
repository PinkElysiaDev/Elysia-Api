package protocol

func (plan *compiledOutputPlan) buildBinding(state evaluation) (plannedOutput, error) {
	source, err := plan.source.buildNode(state)
	if err != nil {
		return plannedOutput{}, err
	}
	slot := plan.expression.bindingSlot
	previous := state.bindings[slot]
	state.bindings[slot] = source.output
	var previousLocation nativeLocation
	if state.capture != nil {
		previousLocation = state.capture.bindings[slot]
		state.capture.bindings[slot] = nativeReadLocation(plan.source.expression, state.capture)
	}
	output, err := plan.body.buildNode(state)
	state.bindings[slot] = previous
	if state.capture != nil {
		state.capture.bindings[slot] = previousLocation
	}
	return output, err
}
