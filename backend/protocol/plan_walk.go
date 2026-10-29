package protocol

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
