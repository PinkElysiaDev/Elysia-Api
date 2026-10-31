package protocol

import "fmt"

func (plan *compiledOutputPlan) buildVisit(state evaluation) (plannedOutput, error) {
	source, err := plan.source.build(state)
	if err != nil {
		return plannedOutput{}, err
	}
	if source.output.IsZero() {
		return plannedOutput{}, fmt.Errorf("visit requires a present value")
	}
	state.walkDepth++
	state.budget.walkNodes++
	if state.walkDepth > state.budget.limits.Depth {
		return plannedOutput{}, resourceFailure("evaluation", "depth", state.budget.limits.Depth, state.walkDepth)
	}
	if state.budget.walkNodes > state.budget.limits.Nodes {
		return plannedOutput{}, resourceFailure("evaluation", "nodes", state.budget.limits.Nodes, state.budget.walkNodes)
	}
	state.walkBody, state.itemIndex, state.item = plan.body, 0, source.output.value
	state.itemOutput = nil
	if state.capture != nil {
		capture := *state.capture
		capture.item = nativeReadLocation(plan.source.expression, state.capture)
		state.capture = &capture
	}
	var reader *planObjectReader
	if source.output.node != nil {
		state.itemOutput = &source.output
	} else if plan.body.itemObjectReads >= objectReuseReads && source.output.value.IsObject() {
		reader = &planObjectReader{root: spanIdentity(source.output.value.raw), parent: state.objectReader}
		state.objectReader = reader
	}
	output, err := plan.body.build(state)
	reader.releaseItem(err)
	return output, err
}

func (plan *compiledOutputPlan) buildWalk(state evaluation) (plannedOutput, error) {
	// The selector observes the caller's item and lexical bindings. Only the
	// chosen array enters the recursive body; unrelated JSON stays opaque.
	source, err := plan.source.build(state)
	if err != nil {
		return plannedOutput{}, err
	}
	depth := state.walkDepth + 1
	if depth > state.budget.limits.Depth {
		return plannedOutput{}, resourceFailure("evaluation", "depth", state.budget.limits.Depth, depth)
	}
	if plan.expression.op == "walk" {
		state.walkBody = plan.body
	}
	state.walkDepth = depth
	return plan.buildIterationOutput(state, source, state.walkBody)
}
