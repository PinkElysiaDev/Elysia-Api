package protocol

import "context"

type evaluation struct {
	coreRequest                *Request
	ctx                        context.Context
	input, item, root, context Value
	budget                     *evaluationBudget
	valueReader                *valueReader
	inputReader, rootReader    func([]string) (JSONOutput, error)
	itemOutput                 *JSONOutput
	itemIndex                  int
	bindings                   []JSONOutput
	walkBody                   *compiledOutputPlan
	walkDepth                  int
	capture                    *nativeCaptureState
}

type evaluationBudget struct {
	stepLimit int
	steps     int
	walkNodes int
	limits    Limits
}

// A compiled identity leaf uses one step from a fresh positive budget. Keep
// only its fallible checks, including the expression's original error location.
func (expression *compiledExpression) checkIdentityAfter(ctx context.Context, bytes int, limits Limits) error {
	if expression == nil {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return locateMappingError(expression.path, err)
	}
	if bytes > limits.BufferBytes {
		return locateMappingError(expression.path, resourceFailure("evaluation", "bytes", limits.BufferBytes, bytes))
	}
	return nil
}

func (state evaluation) advance() error {
	state.budget.steps++
	stepLimit := state.budget.stepLimit
	if stepLimit == 0 {
		stepLimit = state.budget.limits.Nodes
	}
	if state.budget.steps > stepLimit {
		return resourceFailure("evaluation", "steps", stepLimit, state.budget.steps)
	}
	return state.ctx.Err()
}
