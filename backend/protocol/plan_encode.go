package protocol

import (
	"context"
	"fmt"
)

func encodePlannedCore(ctx context.Context, compiled *Compiled, direction Direction, input any, options EvaluationContext) (Value, error) {
	semanticBudget := DefaultLimits()
	if isRequestDirection(direction) {
		semanticBudget = requestLimits(ctx, semanticBudget, false)
	}
	// This is EncodeSemantic's original boundary, before execution cancellation
	// or the target mapping's potentially smaller request budget.
	if err := checkSemanticValue(input, semanticLimits(semanticBudget)); err != nil {
		return Value{}, err
	}
	fail := func(err error) (Value, error) { return Value{}, compiled.mappingError(direction, err) }
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	inputLimits, outputLimits, workLimits := compiled.mappingLimits(ctx, direction)
	if err := checkDocumentMapping(ctx, input, inputLimits, "mapping_input"); err != nil {
		return fail(err)
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
	state := evaluation{ctx: ctx, context: contextValue, runtime: bindEvaluationRuntime(options), inputReader: read, rootReader: read, budget: &evaluationBudget{limits: workLimits, stepLimit: compiled.limits.Nodes}}
	state.coreRequest, _ = input.(*Request)
	if compiled.packagePlan != nil {
		if err := compiled.preparePackageState(direction, state, inputLimits); err != nil {
			return fail(err)
		}
	}
	mapping := compiled.mappings[direction]
	output, err := mapping.outputPlan.evaluate(state)
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
	return output, nil
}
