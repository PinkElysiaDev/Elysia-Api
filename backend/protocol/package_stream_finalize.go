package protocol

import (
	"context"
	"fmt"
)

func hasCurrentEventRead(plan *compiledOutputPlan) bool {
	if plan == nil {
		return false
	}
	if plan.expression.op == "read" && (plan.expression.from == "input" || plan.expression.from == "root") {
		return true
	}
	for _, child := range []*compiledOutputPlan{plan.source, plan.body, plan.when, plan.then, plan.otherwise} {
		if hasCurrentEventRead(child) {
			return true
		}
	}
	for _, child := range plan.items {
		if hasCurrentEventRead(child) {
			return true
		}
	}
	for _, field := range plan.fields {
		if hasCurrentEventRead(field.expression) {
			return true
		}
	}
	for _, child := range plan.cases {
		if hasCurrentEventRead(child) {
			return true
		}
	}
	return false
}

func (compiled *Compiled) finishPackageEvents(ctx context.Context, direction Direction, options EvaluationContext) ([]Value, error) {
	key := moduleStateKey{compiled: compiled, direction: direction}
	retained := options.State.packageStates[key]
	if retained == nil {
		return nil, nil
	}
	if retained.scope != options.Scope {
		return nil, streamIssue(ResourceScopeMismatch, "/state", "event state belongs to another stream scope")
	}
	defer options.State.releasePackageState(key)
	finalizer := compiled.mappings[direction].streamFinalizer
	if finalizer == nil || !retained.hasRendered {
		return nil, nil
	}
	if !retained.hasTerminal {
		return nil, streamIssue(UpstreamContractViolation, "/type", "event finalization requires an observed semantic terminal")
	}
	runtime := bindEvaluationRuntime(compiled, direction, options)
	if runtime.outputStream != nil && runtime.outputStream.delivered.isTerminal {
		return nil, nil
	}
	contextValue, err := EncodeValue(options.Values)
	if err != nil {
		return nil, err
	}
	_, limits, work := compiled.mappingLimits(ctx, direction)
	state := evaluation{ctx: ctx, context: contextValue, runtime: runtime, streamSlots: retained.slots,
		budget: &evaluationBudget{limits: work, stepLimit: compiled.limits.Nodes}}
	output, err := finalizer.build(state)
	if err != nil {
		return nil, compiled.mappingError(direction, err)
	}
	frames, err := compiled.encodePackageFrameOutput(ctx, direction, output, limits)
	if err != nil {
		return nil, compiled.mappingError(direction, fmt.Errorf("event finalization: %w", err))
	}
	return frames, nil
}

// Release discards per-stream execution data after its owner finishes or aborts.
// It does not emit terminal frames or replace FinishEvents and wire validation.
func (state *EvaluationState) Release() {
	if state == nil {
		return
	}
	clear(state.packageStates)
	clear(state.modules)
	clear(state.initialized)
	state.packageStateItems, state.packageStateBytes = 0, 0
}
