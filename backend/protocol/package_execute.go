package protocol

import (
	"context"
	"fmt"
)

// packageExecution shares input views between effects and the mapping. Conditions
// observe the original input once; output cannot change which effects apply.
type packageExecution struct {
	reader *valueReader
}

func (compiled *Compiled) preparePackageExecution(ctx context.Context, direction Direction, input, contextValue Value, limits Limits) (*packageExecution, error) {
	execution := &packageExecution{reader: requestValueReader(input)}
	state := evaluation{ctx: ctx, input: input, root: input, context: contextValue, valueReader: execution.reader}
	if err := compiled.preparePackageState(direction, state, limits); err != nil {
		return nil, err
	}
	return execution, nil
}

func (compiled *Compiled) preparePackageState(direction Direction, state evaluation, limits Limits) error {
	isDecode := isPackageDecodeDirection(direction)
	if isDecode {
		if err := compiled.validateWireContract(state.ctx, direction, state.input, nil, limits); err != nil {
			return err
		}
	} else {
		version, err := state.readPackageInput([]string{"schemaVersion"})
		if err := compiled.checkPackageVersion(direction, version, err); err != nil {
			return err
		}
	}
	var conditions *evaluation
	if direction == EncodeRequest && !compiled.packagePlan.requestProjection.IsZero() {
		if err := compiled.checkCacheDispositions(state, limits); err != nil {
			return err
		}
	}
	for _, effect := range compiled.packagePlan.effects[direction] {
		if effect.scope == cacheIntentScope {
			continue // Projection belongs to the overridable conversion rule.
		}
		if err := state.ctx.Err(); err != nil {
			return err
		}
		if effect.when != nil {
			if conditions == nil {
				conditionState := state
				conditionState.budget = &evaluationBudget{limits: limits, stepLimit: limits.Nodes}
				conditions = &conditionState
			}
			condition, err := effect.when.evaluate(*conditions)
			if err != nil {
				return err
			}
			isApplicable, err := readBoolean(condition)
			if err != nil {
				return compiled.packageEffectError(direction, effect, "semantic effect condition must produce a boolean", err)
			}
			if !isApplicable {
				continue
			}
		}
		if effect.kind == "reject" {
			return compiled.packageEffectError(direction, effect, effect.reason, nil)
		}
		pointer := effect.corePointer
		if isDecode {
			pointer = effect.wirePointer
		}
		value, err := state.readPackageInput(pointer)
		if err != nil || value.IsZero() {
			return compiled.packageEffectError(direction, effect, "declared emit input is absent or inaccessible", err)
		}
		if effect.kind == "aggregate" && !isDecode {
			if err := checkPackageAggregateInput(state, value); err != nil {
				return compiled.packageEffectError(direction, effect, "aggregate input violates its core owner contract", err)
			}
		}
	}
	return nil
}

func (state evaluation) readPackageInput(pointer []string) (JSONOutput, error) {
	if state.inputReader != nil {
		return state.inputReader(pointer)
	}
	value, err := readValuePointerWithin(state.input, pointer, state.valueReader)
	return JSONLeaf(value), err
}

func (compiled *Compiled) finishPackageExecution(ctx context.Context, direction Direction, output Value, limits Limits) error {
	if isPackageDecodeDirection(direction) {
		return compiled.finishPackageCoreOutput(ctx, direction, JSONLeaf(output), requestValueReader(output))
	}
	if err := compiled.validateWireContract(ctx, direction, output, nil, limits); err != nil {
		return err
	}
	return ctx.Err()
}

func (compiled *Compiled) finishPackageCoreOutput(ctx context.Context, direction Direction, output JSONOutput, reader *valueReader) error {
	version, err := readOutputPointer(output, []string{"schemaVersion"}, reader)
	if err := compiled.checkPackageVersion(direction, version, err); err != nil {
		return err
	}
	return ctx.Err()
}

func (compiled *Compiled) checkPackageVersion(direction Direction, output JSONOutput, err error) error {
	version := output.value
	if err == nil && output.node != nil {
		version, err = output.Encode(context.Background())
	}
	var number int
	if err == nil {
		err = version.Decode(&number)
	}
	if err != nil || number != compiled.packagePlan.coreVersion {
		return &ConversionError{cause: err, Issues: []ConversionIssue{{Code: InvalidInput, Severity: SeverityError, Protocol: compiled.identity, Direction: direction, Stage: "mapping.core", Path: "/schemaVersion", Reason: fmt.Sprintf("mapping requires core schemaVersion %d", compiled.packagePlan.coreVersion)}}}
	}
	return nil
}

func (compiled *Compiled) packageEffectError(direction Direction, effect compiledSemanticEffect, reason string, cause error) error {
	path := effect.path
	if isPackageDecodeDirection(direction) {
		path = effect.target
	}
	return &ConversionError{cause: cause, Issues: []ConversionIssue{{Code: ConversionRejected, Severity: SeverityError, Protocol: compiled.identity, Direction: direction, Stage: "mapping.effects", Path: path, RuleID: effect.id, Reason: reason, Fidelity: "rejected", Evidence: "protocol-mapping"}}}
}
