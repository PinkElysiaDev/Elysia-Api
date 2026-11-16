package protocol

import (
	"context"
	"fmt"
)

func hasCurrentEventRead(plan *compiledOutputPlan) bool {
	return readsCurrentEvent(plan, false)
}

func readsCurrentEvent(plan *compiledOutputPlan, isInputBound bool) bool {
	if plan == nil {
		return false
	}
	if plan.expression.op == "read" && (plan.expression.from == "root" || plan.expression.from == "input" && !isInputBound) {
		return true
	}
	if plan.expression.op == "with_input" {
		return readsCurrentEvent(plan.source, isInputBound) || readsCurrentEvent(plan.body, true)
	}
	for _, child := range []*compiledOutputPlan{plan.source, plan.body, plan.when, plan.then, plan.otherwise} {
		if readsCurrentEvent(child, isInputBound) {
			return true
		}
	}
	for _, child := range plan.items {
		if readsCurrentEvent(child, isInputBound) {
			return true
		}
	}
	fields, cases := plan.readCollections()
	for _, field := range fields {
		if readsCurrentEvent(field.expression, isInputBound) {
			return true
		}
	}
	for _, child := range cases {
		if readsCurrentEvent(child, isInputBound) {
			return true
		}
	}
	return false
}

func (compiled *Compiled) finishPackageEvents(ctx context.Context, direction Direction, options EvaluationContext) ([]Value, error) {
	key := executionStateKey{compiled: compiled, direction: direction}
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
		streamTransaction: &packageStreamTransaction{owner: options.State, key: key, scope: options.Scope, previous: retained, plan: compiled.mappings[direction].streamState, conversion: options.conversion, diagnostics: options.Diagnostics},
		budget:            &evaluationBudget{limits: work, stepLimit: compiled.limits.Nodes}}
	output, err := finalizer.build(state)
	if err != nil {
		return nil, compiled.mappingError(direction, err)
	}
	if err := state.streamTransaction.resolveDispositions(); err != nil {
		return nil, compiled.mappingError(direction, err)
	}
	frames, err := compiled.encodePackageFrameOutput(ctx, direction, output, limits)
	if err != nil {
		return nil, compiled.mappingError(direction, fmt.Errorf("event finalization: %w", err))
	}
	state.streamTransaction.commitDiagnostics()
	return frames, nil
}

// Release discards per-stream execution data after its owner finishes or aborts.
// It does not emit terminal frames or replace FinishEvents and wire validation.
func (state *EvaluationState) Release() {
	if state == nil {
		return
	}
	clear(state.packageStates)
	for _, validation := range state.sourceStreams {
		validation.Release()
	}
	clear(state.sourceStreams)
	state.packageStateItems, state.packageStateBytes = 0, 0
	state.retention.release()
}
