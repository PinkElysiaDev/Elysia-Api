package protocol

type operationMetadata struct {
	id, startedAt Value
}

// WithOperation binds one gateway-owned execution identity and its Unix start
// time. Reuse the returned context for every frame and finalization in this
// execution; expressions cannot replace these facts through context Values.
func (options EvaluationContext) WithOperation(id string, startedAt int64) (EvaluationContext, error) {
	if id == "" {
		return options, operationMetadataError("/operation/id", "operation identity must not be empty")
	}
	if startedAt < 0 {
		return options, operationMetadataError("/operation/startedAt", "operation start time must be nonnegative Unix seconds")
	}
	options.operation = &operationMetadata{id: StringValue(id), startedAt: coreInteger(startedAt)}
	return options, nil
}

func operationMetadataError(path, reason string) error {
	return &ConversionError{Issues: []ConversionIssue{{Code: InvalidInput, Severity: SeverityError, Stage: "runtime", Path: path, Reason: reason}}}
}
