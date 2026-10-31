package protocol

// A check deliberately invokes a semantic constraint. Wire assertions retain
// its existing business diagnostic instead of treating it as a failed predicate.
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
