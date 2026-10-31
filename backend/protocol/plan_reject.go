package protocol

import "fmt"

func (plan *compiledOutputPlan) reject(state evaluation) error {
	if plan.source == nil {
		return plan.expression.rejection
	}
	location, err := plan.source.build(state)
	if err != nil {
		return err
	}
	path, err := readPlannedString(location.output)
	if err != nil {
		return fmt.Errorf("rejection location requires a pointer string: %w", err)
	}
	if _, err := parsePointer(path); err != nil {
		return fmt.Errorf("invalid rejection location: %w", err)
	}
	// The compiled diagnostic is shared by requests; only a taken rejection
	// owns its selected source location.
	failure := *plan.expression.rejection
	failure.diagnostic.Path = path
	return &failure
}

type declaredRejectionError struct {
	diagnostic RejectDiagnostic
	reason     string
}

func (failure *declaredRejectionError) Error() string {
	return fmt.Sprintf("%s at %s: %s", failure.diagnostic.Code, failure.diagnostic.Path, failure.reason)
}

func (failure *declaredRejectionError) issue(identity Identity, direction Direction) ConversionIssue {
	diagnostic := failure.diagnostic
	if diagnostic.Direction != "" {
		direction = diagnostic.Direction
	}
	return ConversionIssue{Code: diagnostic.Code, Severity: SeverityError, Protocol: identity, Direction: direction,
		Stage: diagnostic.Stage, Path: diagnostic.Path, Reason: failure.reason, Suggestion: diagnostic.Suggestion}
}
