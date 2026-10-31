package protocol

import "errors"

// Declared checks retain their business diagnostics and error chains instead
// of being reported as an unclassified failed predicate.
type declaredCheckError struct{ err error }

func (failure *declaredCheckError) Error() string { return failure.err.Error() }
func (failure *declaredCheckError) Unwrap() error { return failure.err }

func (plan *compiledOutputPlan) checkUsage(state evaluation) (plannedOutput, error) {
	value, err := plan.source.build(state)
	if err != nil {
		return plannedOutput{}, err
	}
	if value.output.IsZero() {
		return value, nil
	}
	var usage *Usage
	if err := writeCoreUsagePointer(value.output, &usage, coreOutputReader(value.output, state.valueReader)); err != nil {
		return plannedOutput{}, err
	}
	if err := ValidateUsageArithmetic(usage); err != nil {
		// The legacy validator reports stream attribution even for JSON. Its
		// code, core location and cause remain valid; this invocation owns stage.
		var failure *ConversionError
		if errors.As(err, &failure) {
			issues := append([]ConversionIssue(nil), failure.Issues...)
			for index := range issues {
				issues[index].Protocol, issues[index].Direction = Identity{}, ""
				issues[index].Stage = "core.usage"
				issues[index].Suggestion = "Correct the usage counters or the mapping that produced them."
			}
			err = &ConversionError{cause: err, Issues: issues, CandidateOnly: failure.CandidateOnly}
		}
		return plannedOutput{}, &declaredCheckError{err: err}
	}
	return value, nil
}

func (plan *compiledOutputPlan) mergeUsage(state evaluation) (plannedOutput, error) {
	operands, err := plan.buildOperandValues(state)
	if err != nil {
		return plannedOutput{}, err
	}
	var merged *Usage
	for _, operand := range operands {
		if operand.output.IsZero() {
			continue
		}
		var update *Usage
		if err := writeCoreUsagePointer(operand.output, &update, coreOutputReader(operand.output, state.valueReader)); err != nil {
			return plannedOutput{}, err
		}
		merged = MergeUsage(merged, update)
	}
	if merged == nil {
		return plannedOutput{}, nil
	}
	output, err := readCoreUsage(*merged, nil)
	if err != nil {
		return plannedOutput{}, err
	}
	return plannedOutput{output: output, bytes: normalizedOutputBytes(output)}, nil
}
