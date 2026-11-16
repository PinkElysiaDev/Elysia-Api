package protocol

import (
	"context"
	"fmt"
)

func preparePlannedCoreEncoding(ctx context.Context, compiled *Compiled, direction Direction, input any, options EvaluationContext) (evaluation, Limits, *provenanceReadMemo, error) {
	semanticBudget := DefaultLimits()
	if isRequestDirection(direction) {
		semanticBudget = requestLimits(ctx, semanticBudget, false)
	}
	// This is EncodeSemantic's original boundary, before execution cancellation
	// or the target mapping's potentially smaller request budget.
	inputLimits, outputLimits, workLimits := compiled.mappingLimits(ctx, direction)
	size, err := measureSemanticValueWithCache(input, semanticLimits(semanticBudget), &workLimits)
	if err != nil {
		return evaluation{}, Limits{}, nil, err
	}
	fail := func(err error) (evaluation, Limits, *provenanceReadMemo, error) {
		return evaluation{}, Limits{}, nil, compiled.mappingError(direction, err)
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	// The caller owns input throughout this synchronous encode; no mapping has
	// run yet. Reuse complete counts only to prove that the next budget fits.
	if !size.fits(inputLimits) {
		if err := checkDocumentMapping(ctx, input, inputLimits, "mapping_input"); err != nil {
			return fail(err)
		}
	}
	if event, ok := input.(*Event); ok && direction == EncodeEvent {
		if err := compiled.checkSemanticEventRanges(ctx, direction, event, options); err != nil {
			return evaluation{}, Limits{}, nil, err
		}
	}
	contextValue, err := EncodeValue(options.Values)
	if err != nil {
		return fail(err)
	}
	cache := size.cache
	read, provenance := createCoreInputReader(input, direction, cache)
	reader := valueReader{minimumSharedBytes: minimumSharedValueBytes(size.maxRawBytes), canGrowThreshold: true}
	state := evaluation{ctx: ctx, context: contextValue, runtime: bindEvaluationRuntime(compiled, direction, options), streamSlots: options.packageTransaction.values(), streamTransaction: options.packageTransaction, inputReader: read, rootReader: read, valueReader: &reader, budget: &evaluationBudget{limits: workLimits, stepLimit: compiled.limits.Nodes}}
	state.coreRequest, _ = input.(*Request)
	state.coreDocument = input
	state.cache = cache

	if err := compiled.preparePackageState(direction, state, inputLimits, options, provenance); err != nil {
		provenance.release()
		return fail(err)
	}
	state.native = compiled.newNativeEvaluation(ctx, direction, options)

	return state, outputLimits, provenance, nil
}

func createCoreInputReader(input any, direction Direction, cache *semanticCache) (func([]string) (JSONOutput, error), *provenanceReadMemo) {
	switch document := input.(type) {
	case *Request:
		return func(pointer []string) (JSONOutput, error) {
			return readCoreRequestOutput(document, pointer, cache)
		}, nil
	case *Event:
		return func(pointer []string) (JSONOutput, error) {
			return readCoreEventOutput(document, pointer, cache)
		}, nil
	case *Response:
		var provenance *provenanceReadMemo
		if direction == EncodeResponse {
			provenance = &provenanceReadMemo{}
		}
		reader := coreReadContext{cache: cache, provenance: provenance}
		return func(pointer []string) (JSONOutput, error) {
			return reader.readResponsePointer(document, pointer)
		}, provenance
	default:
		return func([]string) (JSONOutput, error) {
			return JSONOutput{}, fmt.Errorf("core input plan requires a request, response or event")
		}, nil
	}
}

func encodePlannedCore(ctx context.Context, compiled *Compiled, direction Direction, input any, options EvaluationContext) (Value, error) {
	state, outputLimits, provenance, err := preparePlannedCoreEncoding(ctx, compiled, direction, input, options)
	if err != nil {
		return Value{}, err
	}
	if provenance != nil {
		defer provenance.release()
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
	output, err := planned.materializeWithin(ctx, state.cache)
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

	if err := compiled.finishPackageExecution(ctx, direction, output, outputLimits); err != nil {
		return fail(err)
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
	if err := compiled.checkSemanticEventRanges(ctx, direction, &event, options); err != nil {
		return err
	}
	inputLimits, _, workLimits := compiled.mappingLimits(ctx, direction)
	fail := func(err error) error { return compiled.runtimeError(direction, compiled.mappingError(direction, err)) }
	contextValue, err := EncodeValue(options.Values)
	if err != nil {
		return fail(err)
	}
	read := func(pointer []string) (JSONOutput, error) { return readCoreEventOutput(&event, pointer, nil) }
	state := evaluation{ctx: ctx, context: contextValue, inputReader: read, rootReader: read, runtime: bindEvaluationRuntime(compiled, direction, options), streamSlots: options.packageTransaction.values(), streamTransaction: options.packageTransaction,
		budget: &evaluationBudget{limits: workLimits, stepLimit: compiled.limits.Nodes}}
	state.coreDocument = &event
	if err := compiled.checkPackageEffects(direction, state, inputLimits, options.Diagnostics, nil); err != nil {
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
