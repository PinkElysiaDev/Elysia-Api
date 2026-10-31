package protocol

import (
	"context"
	"fmt"
)

// Auxiliary operation values have their own contracts, not core response
// schemaVersion or Native. A bound wire contract includes the whole-value budget.
func (compiled *Compiled) executePackageOperationPlan(ctx context.Context, mapping compiledMapping, direction Direction, path string, input, contextValue Value, source, target compiledWireContract, options EvaluationContext) (Value, error) {
	fail := func(err error) (Value, error) {
		return Value{}, compiled.mappingError(direction, locateMappingError(path, err))
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	if source.contextValidator != nil {
		if err := source.contextValidator.validate(ctx, direction, input, compiled.limits); err != nil {
			return Value{}, compiled.runtimeError(direction, err)
		}
	} else if err := checkResourceValue(input, compiled.limits, "mapping_input"); err != nil {
		return fail(err)
	}
	state := evaluation{ctx: ctx, input: input, root: input, context: contextValue, runtime: bindEvaluationRuntime(compiled, direction, options),
		valueReader: requestValueReader(input), objectReader: mapping.outputPlan.newRootObjectReader(input),
		budget: &evaluationBudget{limits: compiled.limits, stepLimit: compiled.limits.Nodes}}
	output, err := mapping.outputPlan.evaluate(state)
	if err != nil {
		return fail(err)
	}
	if output.IsZero() {
		return fail(fmt.Errorf("mapping omitted its entire output"))
	}
	if target.contextValidator != nil {
		if err := target.contextValidator.validate(ctx, direction, output, compiled.limits); err != nil {
			return Value{}, compiled.runtimeError(direction, err)
		}
	} else if err := checkResourceValue(output, compiled.limits, "mapping_output"); err != nil {
		return fail(err)
	}
	return output, nil
}
