package protocol

import (
	"context"
	"fmt"
)

// ValidateRequestOutput checks the final immutable request after native
// reconciliation and wire edits. Caller intent remains read-only; a successful
// check never changes the prepared request or grants a new target disposition.
func (compiled *Compiled) ValidateRequestOutput(ctx context.Context, wire Value, options EvaluationContext) error {
	if compiled.packagePlan == nil {
		if err := compiled.CheckRequestBody(ctx, wire); err != nil {
			return err
		}
		return compiled.ValidateWireOutputContext(ctx, EncodeRequest, wire)
	}
	options = options.withInputView(wire)
	view := options.inputView
	limits := compiled.RequestLimits(ctx)
	if err := view.checkResources(ctx, wire, limits, "request_wire"); err != nil {
		return resourceIssue(err, compiled.identity, EncodeRequest, "encode.request")
	}
	if err := compiled.validateWireInputView(ctx, EncodeRequest, view, limits, compiled.selectFinalWireContract(EncodeRequest)); err != nil {
		return err
	}
	check := compiled.mappings[EncodeRequest].finalCheck
	if check == nil {
		return nil
	}
	state := evaluation{ctx: ctx, input: wire, root: wire, inputView: view, valueReader: &view.borrowed, objectReader: view.objectReader(),
		runtime: evaluationRuntime{isBound: true, clientOutput: options.ClientOutput}, budget: &evaluationBudget{limits: limits, stepLimit: compiled.limits.Nodes}}
	value, err := check.evaluate(state)
	if err != nil {
		return compiled.mappingError(EncodeRequest, err)
	}
	isValid, err := readBoolean(value)
	if err == nil && !isValid {
		err = fmt.Errorf("final request does not satisfy its declared caller-intent check")
	}
	if err != nil {
		return compiled.mappingError(EncodeRequest, locateMappingError(check.expression.path, err))
	}
	return nil
}
