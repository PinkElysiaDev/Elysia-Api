package protocol

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
