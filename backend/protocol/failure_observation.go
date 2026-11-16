package protocol

import (
	"context"
	"errors"
	"fmt"
)

func compileFailureObservation(expression *Expression, direction Direction, path string, compiler *expressionCompiler) (*compiledOutputPlan, error) {
	if expression == nil {
		return nil, nil
	}
	path += "/failureObservation"
	if direction != DecodeEvent {
		return nil, fmt.Errorf("%s: requires upstream event decoding", path)
	}
	plan, err := compiler.compilePlan(*expression, path, expressionScope{}, 1)
	if err != nil {
		return nil, err
	}
	if !isAssignable(plan.expression.result, ObjectType) || !localProjectionCondition(plan) {
		return nil, fmt.Errorf("%s: requires an object from local input without execution state", path)
	}
	return plan, nil
}

func (compiled *Compiled) observeRejectedFrame(ctx context.Context, frame Value, rejection error) error {
	plan := compiled.mappings[DecodeEvent].failureObservation
	var resourceLimit *ResourceLimitError
	if plan == nil || ctx.Err() != nil || errors.As(rejection, &resourceLimit) {
		return rejection
	}
	state := evaluation{ctx: ctx, input: frame, root: frame, budget: &evaluationBudget{limits: compiled.limits, stepLimit: compiled.limits.Nodes}}
	output, err := plan.build(state)
	if err != nil {
		return errors.Join(rejection, compiled.runtimeError(DecodeEvent, err))
	}
	if err := checkPlannedOutputLimits(output, compiled.limits, "failure_observation"); err != nil {
		return errors.Join(rejection, compiled.runtimeError(DecodeEvent, err))
	}
	observed, err := output.materialize(ctx)
	if err != nil {
		return errors.Join(rejection, compiled.runtimeError(DecodeEvent, err))
	}
	if observed.IsZero() || observed.IsNull() {
		return rejection
	}
	var outcome struct {
		Error  Value `json:"error"`
		Status Value `json:"status"`
	}
	if err := decodeContract(observed.Bytes(), &outcome); err != nil {
		return errors.Join(rejection, compiled.runtimeError(DecodeEvent, err))
	}
	response := &Response{Error: outcome.Error, Status: outcome.Status}
	return errors.Join(CheckGenerationOutcome(response), rejection)
}
