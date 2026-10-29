package protocol

import (
	"context"
	"fmt"
)

// packageExecution owns effect decisions for one call. Conditions observe the
// original mapping input once; the output cannot change which effects apply.
type packageExecution struct {
	reader *valueReader
}

func (compiled *Compiled) preparePackageExecution(ctx context.Context, direction Direction, input, contextValue Value, limits Limits) (*packageExecution, error) {
	isDecode := isPackageDecodeDirection(direction)
	if isDecode {
		if err := compiled.validateWireContract(ctx, direction, input, nil, limits); err != nil {
			return nil, err
		}
	}
	execution := &packageExecution{reader: requestValueReader(input)}
	if !isDecode {
		if err := compiled.checkPackageCoreVersion(direction, input, execution.reader); err != nil {
			return nil, err
		}
	}
	var state *evaluation
	for _, effect := range compiled.packagePlan.effects[direction] {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if effect.when != nil {
			if state == nil {
				state = &evaluation{ctx: ctx, input: input, root: input, context: contextValue, valueReader: execution.reader, budget: &evaluationBudget{limits: limits, stepLimit: limits.Nodes}}
			}
			condition, err := effect.when.evaluate(*state)
			if err != nil {
				return nil, err
			}
			isApplicable, err := readBoolean(condition)
			if err != nil {
				return nil, compiled.packageEffectError(direction, effect, "semantic effect condition must produce a boolean", err)
			}
			if !isApplicable {
				continue
			}
		}
		if effect.kind == "reject" {
			return nil, compiled.packageEffectError(direction, effect, effect.reason, nil)
		}
		pointer := effect.corePointer
		if isDecode {
			pointer = effect.wirePointer
		}
		value, err := readValuePointerWithin(input, pointer, execution.reader)
		if err != nil || value.IsZero() {
			return nil, compiled.packageEffectError(direction, effect, "declared emit input is absent or inaccessible", err)
		}
	}
	return execution, nil
}

func (compiled *Compiled) finishPackageExecution(ctx context.Context, direction Direction, output Value, limits Limits) error {
	isDecode := isPackageDecodeDirection(direction)
	if !isDecode {
		if err := compiled.validateWireContract(ctx, direction, output, nil, limits); err != nil {
			return err
		}
	}
	reader := requestValueReader(output)
	if isDecode {
		if err := compiled.checkPackageCoreVersion(direction, output, reader); err != nil {
			return err
		}
	}
	return ctx.Err()
}

func (compiled *Compiled) checkPackageCoreVersion(direction Direction, value Value, reader *valueReader) error {
	version, err := readValuePointerWithin(value, []string{"schemaVersion"}, reader)
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
