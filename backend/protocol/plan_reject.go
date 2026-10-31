package protocol

import "fmt"

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
