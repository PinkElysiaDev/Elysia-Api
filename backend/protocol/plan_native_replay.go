package protocol

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

type nativeEvaluation struct {
	target       Target
	limits       Limits
	continuation *continuationInput
}

type nativeLocationPattern struct {
	prefix, suffix string
}

func (compiled *Compiled) newNativeEvaluation(ctx context.Context, direction Direction, options EvaluationContext) *nativeEvaluation {
	if !compiled.mappings[direction].outputPlan.hasNativeTarget {
		return nil
	}
	return &nativeEvaluation{
		target:       Target{Protocol: compiled.identity, Direction: direction, Scope: options.Scope},
		limits:       compiled.nativeLimits(ctx, direction),
		continuation: options.continuationInput,
	}
}

func (plan *compiledOutputPlan) buildNativeTarget(state evaluation) (plannedOutput, error) {
	if state.native == nil {
		return plannedOutput{}, fmt.Errorf("native target is not bound")
	}
	source, err := plan.source.buildNode(state)
	if err != nil {
		return plannedOutput{}, err
	}
	if plan.expression.op == "native_matches" {
		return plan.matchNativeLocation(source.output, state.native.target)
	}
	if source.output.IsZero() || coreOutputIsNull(source.output) {
		return plannedOutput{}, IssuesError([]ConversionIssue{{
			Code: UnsupportedCapability, Severity: SeverityError, Stage: "wire", Path: "/native",
			Reason:     "native node has no equivalent in the target protocol",
			Suggestion: "Use a target with an equivalent declared mapping; preserve the original capability.",
		}})
	}
	native, err := decodeCoreOutput(source.output, writeCoreNative)
	if err != nil {
		return plannedOutput{}, err
	}
	// Authorize the snapshot observed by the mapping. Typed core reads retain
	// their existing JSON normalization; replay never bypasses that boundary.
	value, err := ReplayNative(*native, state.native.target, nil, state.native.limits)
	if err == nil && state.native.continuation != nil && state.native.continuation.containsRemoval(native.Source.Path) {
		selection := state.native.continuation
		if selection.removals[native.Source.Path] {
			return plannedOutput{}, operationMetadataError("/continuation", "consumed carrier cannot be replayed as provider content")
		}
		if native.Source.Protocol != selection.compiled.identity {
			return plannedOutput{}, operationMetadataError("/continuation", "native carrier source revision changed")
		}
		original, readErr := readValuePointer(selection.input, mustContinuationPointer(native.Source.Path))
		if readErr != nil || !equalValues(original, native.Value) {
			return plannedOutput{}, operationMetadataError("/continuation", "native carrier fragment no longer matches its source")
		}
		value, err = selection.strip(state.ctx, value, native.Source.Path, state.native.limits)
	}
	return plannedLeaf(value), err
}

func (plan *compiledOutputPlan) matchNativeLocation(output JSONOutput, target Target) (plannedOutput, error) {
	if output.IsZero() || coreOutputIsNull(output) {
		return plannedLeaf(coreBoolean(false)), nil
	}
	origin, err := decodeCoreOutput(output, writeCoreProvenance)
	if err != nil {
		return plannedOutput{}, err
	}
	if !CanPreserveNative(*origin, target) {
		return plannedLeaf(coreBoolean(false)), nil
	}
	location := plan.expression.nativeLocation
	index, hasParent := strings.CutPrefix(origin.Path, location.prefix)
	index, hasField := strings.CutSuffix(index, location.suffix)
	if !hasParent || !hasField {
		return plannedLeaf(coreBoolean(false)), nil
	}
	position, err := strconv.Atoi(index)
	return plannedLeaf(coreBoolean(err == nil && position >= 0)), nil
}
