package protocol

import "context"

type evaluation struct {
	coreRequest                *Request
	ctx                        context.Context
	input, item, root, context Value
	budget                     *evaluationBudget
	valueReader                *valueReader
	objectReader               *planObjectReader
	inputView                  *inputView
	inputReader, rootReader    func([]string) (JSONOutput, error)
	itemOutput                 *JSONOutput
	itemIndex                  int
	bindings                   []JSONOutput
	streamSlots                []Value
	streamTransaction          *packageStreamTransaction
	walkBody                   *compiledOutputPlan
	walkDepth                  int
	capture                    *nativeCaptureState
	native                     *nativeEvaluation
	indexes                    *indexEvaluationState
	runtime                    evaluationRuntime
}

type evaluationRuntime struct {
	continuation          *ContinuationBinding
	continuationInput     *continuationInput
	clientOutput          *ClientOutput
	observedNativeFrame   Value
	isBound               bool
	isInterleavedThinking bool
	scopeModel            string
	outputStream          *packageStreamValidation
	operation             *operationMetadata
	emissionOffset        int
	frameUsage            *Usage
	hasFrameUsage         bool
}

func bindEvaluationRuntime(compiled *Compiled, direction Direction, options EvaluationContext) evaluationRuntime {
	runtime := evaluationRuntime{clientOutput: options.ClientOutput, observedNativeFrame: options.observedNativeFrame, isBound: true, isInterleavedThinking: options.InterleavedThinking, scopeModel: options.Scope.Model, operation: options.operation, emissionOffset: options.emissionOffset}
	runtime.continuation, runtime.continuationInput = options.continuation, options.continuationInput
	stream := options.outputStream
	if stream != nil && compiled.packagePlan != nil && direction == EncodeEvent && stream.direction == EncodeEvent && stream.packageStream != nil &&
		compiled.hash == stream.compiled.hash && compiled.mappings[direction].definitionHash == stream.compiled.mappings[direction].definitionHash && options.Scope == stream.options.Scope {
		runtime.outputStream = stream.packageStream
	}
	return runtime
}

type evaluationBudget struct {
	stepLimit           int
	steps               int
	walkNodes           int
	limits              Limits
	hasReadOutputStream bool
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
