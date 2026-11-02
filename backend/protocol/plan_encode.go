package protocol

import (
	"context"
	"fmt"
)

func preparePlannedCoreEncoding(ctx context.Context, compiled *Compiled, direction Direction, input any, options EvaluationContext) (evaluation, Limits, error) {
	semanticBudget := DefaultLimits()
	if isRequestDirection(direction) {
		semanticBudget = requestLimits(ctx, semanticBudget, false)
	}
	// This is EncodeSemantic's original boundary, before execution cancellation
	// or the target mapping's potentially smaller request budget.
	size, err := measureSemanticValue(input, semanticLimits(semanticBudget))
	if err != nil {
		return evaluation{}, Limits{}, err
	}
	fail := func(err error) (evaluation, Limits, error) {
		return evaluation{}, Limits{}, compiled.mappingError(direction, err)
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	inputLimits, outputLimits, workLimits := compiled.mappingLimits(ctx, direction)
	// The caller owns input throughout this synchronous encode; no mapping has
	// run yet. Reuse complete counts only to prove that the next budget fits.
	if !size.fits(inputLimits) {
		if err := checkDocumentMapping(ctx, input, inputLimits, "mapping_input"); err != nil {
			return fail(err)
		}
	}
	contextValue, err := EncodeValue(options.Values)
	if err != nil {
		return fail(err)
	}
	read := func(pointer []string) (JSONOutput, error) {
		switch document := input.(type) {
		case *Request:
			return readCoreRequestOutput(document, pointer)
		case *Response:
			return readCoreResponseOutput(document, pointer)
		case *Event:
			return readCoreEventOutput(document, pointer)
		default:
			return JSONOutput{}, fmt.Errorf("core input plan requires a request, response or event")
		}
	}
	reader := valueReader{minimumSharedBytes: minimumSharedValueBytes(size.maxRawBytes), canGrowThreshold: true}
	state := evaluation{ctx: ctx, context: contextValue, runtime: bindEvaluationRuntime(compiled, direction, options), streamSlots: options.packageTransaction.values(), streamTransaction: options.packageTransaction, inputReader: read, rootReader: read, valueReader: &reader, budget: &evaluationBudget{limits: workLimits, stepLimit: compiled.limits.Nodes}}
	state.coreRequest, _ = input.(*Request)
	if compiled.packagePlan != nil {
		if err := compiled.preparePackageState(direction, state, inputLimits, options); err != nil {
			return fail(err)
		}
		state.native = compiled.newNativeEvaluation(ctx, direction, options)
	}
	return state, outputLimits, nil
}

func encodePlannedCore(ctx context.Context, compiled *Compiled, direction Direction, input any, options EvaluationContext) (Value, error) {
	state, outputLimits, err := preparePlannedCoreEncoding(ctx, compiled, direction, input, options)
	if err != nil {
		return Value{}, err
	}
	fail := func(err error) (Value, error) { return Value{}, compiled.mappingError(direction, err) }
	mapping := compiled.mappings[direction]
	planned, err := mapping.outputPlan.build(state)
	if err != nil {
		return fail(err)
	}
	if err := options.packageTransaction.resolveDispositions(); err != nil {
		return fail(err)
	}
	output, err := planned.materialize(ctx)
	if err != nil {
		return fail(err)
	}
	if output.IsZero() {
		return fail(fmt.Errorf("mapping omitted its entire output"))
	}
	if err := checkResourceValue(output, outputLimits, "mapping_output"); err != nil {
		return fail(err)
	}
	if err := mapping.output.validate(output, "/output", outputLimits); err != nil {
		return fail(err)
	}
	if compiled.packagePlan != nil {
		if err := compiled.finishPackageExecution(ctx, direction, output, outputLimits); err != nil {
			return fail(err)
		}
	}
	if state.budget.hasReadOutputStream && direction == EncodeEvent {
		event := input.(*Event)
		target := compiled.target(direction, options)
		if compiled.native.Preserve && event.Native != nil && CanPreserveNative(event.Native.Source, target) {
			// Keep the original provenance and resource errors ahead of this
			// unsupported combination. Never drop Native to make it encode.
			if _, err := ReplayNative(*event.Native, target, nil, compiled.wireLimits(ctx, direction)); err != nil {
				return fail(err)
			}
			if err := ctx.Err(); err != nil {
				return fail(err)
			}
			return fail(IssuesError([]ConversionIssue{{Code: UnsupportedNative, Severity: SeverityError, Protocol: compiled.identity, Direction: direction,
				Stage: "preserve", Path: "/native", Reason: "output stream metadata cannot be combined with automatic native reconciliation without declared dynamic field ownership"}}))
		}
	}
	if err := options.packageTransaction.stage(state, true); err != nil {
		return fail(err)
	}
	return output, nil
}

func (compiled *Compiled) checkNativeEventEffects(ctx context.Context, direction Direction, event Event, options EvaluationContext) error {
	inputLimits, _, workLimits := compiled.mappingLimits(ctx, direction)
	fail := func(err error) error { return compiled.runtimeError(direction, compiled.mappingError(direction, err)) }
	contextValue, err := EncodeValue(options.Values)
	if err != nil {
		return fail(err)
	}
	read := func(pointer []string) (JSONOutput, error) { return readCoreEventOutput(&event, pointer) }
	state := evaluation{ctx: ctx, context: contextValue, inputReader: read, rootReader: read, runtime: bindEvaluationRuntime(compiled, direction, options), streamSlots: options.packageTransaction.values(), streamTransaction: options.packageTransaction,
		budget: &evaluationBudget{limits: workLimits, stepLimit: compiled.limits.Nodes}}
	if err := compiled.checkPackageEffects(direction, state, inputLimits); err != nil {
		return fail(err)
	}
	if observer := compiled.mappings[direction].streamObserver; observer != nil {
		if observer.hasNativeTarget {
			state.native = &nativeEvaluation{target: compiled.target(direction, options), limits: compiled.nativeLimits(ctx, direction)}
		}
		output, err := observer.build(state)
		if err != nil {
			return fail(err)
		}
		if err := checkPlannedOutputLimits(output, inputLimits, "mapping_observe"); err != nil {
			return fail(err)
		}
	}
	if err := options.packageTransaction.resolveDispositions(); err != nil {
		return fail(err)
	}
	return options.packageTransaction.stage(state, false)
}
