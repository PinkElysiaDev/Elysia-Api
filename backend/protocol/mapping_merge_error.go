package protocol

import (
	"fmt"
	"strings"
)

// MergeConflictDiagnostic classifies a merge_deep leaf collision without
// changing rejection behavior. PathPrefix locates the merged object; the
// collision's RFC 6901 pointer is appended to it. Reason is static text.
type MergeConflictDiagnostic struct {
	Code       IssueCode `json:"code"`
	Stage      string    `json:"stage"`
	PathPrefix string    `json:"pathPrefix,omitempty"`
	Reason     string    `json:"reason"`
	Suggestion string    `json:"suggestion,omitempty"`
}

func compileMergeConflictDiagnostic(diagnostic *MergeConflictDiagnostic, path string) (*MergeConflictDiagnostic, error) {
	if diagnostic == nil {
		return nil, fmt.Errorf("%s: requires a diagnostic object", path)
	}
	if err := validateDiagnosticClassification(diagnostic.Code, diagnostic.Stage, path); err != nil {
		return nil, err
	}
	if _, err := parsePointer(diagnostic.PathPrefix); err != nil {
		return nil, fmt.Errorf("%s/pathPrefix: %w", path, err)
	}
	if strings.TrimSpace(diagnostic.Reason) == "" {
		return nil, fmt.Errorf("%s/reason: requires a static explanation", path)
	}
	copy := *diagnostic
	return &copy, nil
}

type mergeConflictDiagnosticError struct {
	diagnostic *MergeConflictDiagnostic
	conflict   *ObjectMergeConflictError
}

func (failure *mergeConflictDiagnosticError) Error() string { return failure.conflict.Error() }
func (failure *mergeConflictDiagnosticError) Unwrap() error { return failure.conflict }

func (failure *mergeConflictDiagnosticError) issue(identity Identity, direction Direction) ConversionIssue {
	diagnostic := failure.diagnostic
	return ConversionIssue{Code: diagnostic.Code, Severity: SeverityError, Protocol: identity, Direction: direction,
		Stage: diagnostic.Stage, Path: diagnostic.PathPrefix + failure.conflict.Path, Reason: diagnostic.Reason, Suggestion: diagnostic.Suggestion}
}
