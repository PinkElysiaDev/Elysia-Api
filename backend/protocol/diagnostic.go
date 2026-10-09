package protocol

import (
	"fmt"
	"time"
)

// Severity determines whether an issue blocks compilation or execution.
type Severity string

const (
	SeverityError   Severity = "error"
	SeverityWarning Severity = "warning"
)

// IssueCode is stable across human-readable message revisions.
type IssueCode string

const (
	InvalidDefinition         IssueCode = "invalid_definition"
	InvalidInput              IssueCode = "invalid_input"
	UnsupportedCapability     IssueCode = "unsupported_capability"
	UnsupportedNative         IssueCode = "unsupported_native"
	ResourceScopeMismatch     IssueCode = "resource_scope_mismatch"
	InvalidAssociation        IssueCode = "invalid_association"
	InvalidMutation           IssueCode = "invalid_mutation"
	LimitExceeded             IssueCode = "limit_exceeded"
	VerificationRequired      IssueCode = "verification_required"
	VerificationMismatch      IssueCode = "verification_mismatch"
	IncompleteCoverage        IssueCode = "incomplete_coverage"
	UpstreamContractViolation IssueCode = "upstream_contract_violation"
	ConversionDegraded        IssueCode = "conversion_degraded"
	ConversionRejected        IssueCode = "conversion_rejected"
	ContinuationUnavailable   IssueCode = "continuation_unavailable"
)

// ConversionIssue locates a compatibility failure without capturing request
// bodies or credentials. Evidence identifies a sample, rule or provider record.
type ConversionIssue struct {
	PolicyRevision string     `json:"policyRevision,omitempty"`
	Fidelity       string     `json:"fidelity,omitempty"`
	RuleID         string     `json:"ruleId,omitempty"`
	PolicyHash     string     `json:"policyHash,omitempty"`
	Code           IssueCode  `json:"code"`
	Severity       Severity   `json:"severity"`
	Protocol       Identity   `json:"protocol"`
	Direction      Direction  `json:"direction"`
	Stage          string     `json:"stage"`
	Path           string     `json:"path"`
	Capability     Capability `json:"capability,omitempty"`
	Reason         string     `json:"reason"`
	Suggestion     string     `json:"suggestion"`
	Evidence       string     `json:"evidence,omitempty"`
}

// DiagnosticSink accumulates non-blocking conversion issues for one request.
// A nil sink disables collection and never changes the conversion result, so
// callers that do not record diagnostics pay nothing for the channel.
type DiagnosticSink struct {
	issues []ConversionIssue
}

// Add records one issue, collapsing an exact repeat. A stream renders usage
// more than once, so the same omission must not accumulate into a flood.
func (sink *DiagnosticSink) Add(issue ConversionIssue) {
	if sink == nil {
		return
	}
	for _, existing := range sink.issues {
		if existing.Code == issue.Code && existing.Path == issue.Path && existing.Stage == issue.Stage && existing.RuleID == issue.RuleID && existing.PolicyHash == issue.PolicyHash && existing.Reason == issue.Reason {
			return
		}
	}
	sink.issues = append(sink.issues, issue)
}

// Issues returns the accumulated diagnostics in emission order.
func (sink *DiagnosticSink) Issues() []ConversionIssue {
	if sink == nil {
		return nil
	}
	return sink.issues
}

// ConversionError carries machine-readable diagnostics through Go error APIs.
type ConversionError struct {
	Issues []ConversionIssue `json:"issues"`
	cause  error
}

// Unwrap retains transport/authorization/cancellation causes without exposing
// them as serialized configuration or payload data.
func (failure *ConversionError) Unwrap() error { return failure.cause }

// Error renders the first blocking diagnostic; Issues contains the full report.
func (failure *ConversionError) Error() string {
	if len(failure.Issues) == 0 {
		return "protocol conversion failed"
	}
	issue := failure.Issues[0]
	return fmt.Sprintf("%s at %s: %s", issue.Code, issue.Path, issue.Reason)
}

// IssuesError converts blocking issues into an error without hiding warnings
// from the caller's original diagnostic list.
func IssuesError(issues []ConversionIssue) error {
	var blocking []ConversionIssue
	for _, issue := range issues {
		if issue.Severity == SeverityError {
			blocking = append(blocking, issue)
		}
	}
	if len(blocking) == 0 {
		return nil
	}
	return &ConversionError{Issues: blocking}
}

// VerificationKind keeps offline evidence distinct from real provider checks.
type VerificationKind string

const (
	OfflineVerification  VerificationKind = "offline"
	UpstreamVerification VerificationKind = "upstream"
)

// VerificationReport binds evidence to the exact definition, engine and suite.
type VerificationReport struct {
	DefinitionHash  string              `json:"definitionHash"`
	CompilerVersion string              `json:"compilerVersion"`
	SamplesHash     string              `json:"samplesHash"`
	Kind            VerificationKind    `json:"kind"`
	VerifiedAt      time.Time           `json:"verifiedAt"`
	Passed          bool                `json:"passed"`
	Covered         []Capability        `json:"covered"`
	Issues          []ConversionIssue   `json:"issues"`
	Target          *Scope              `json:"target,omitempty"`
	Checks          []VerificationCheck `json:"checks,omitempty"`
}

// VerificationCheck records reproducible sample evidence without payloads.
type VerificationCheck struct {
	SampleID     string       `json:"sampleId"`
	Direction    Direction    `json:"direction"`
	Passed       bool         `json:"passed"`
	Rejected     bool         `json:"rejected,omitempty"`
	Capabilities []Capability `json:"capabilities,omitempty"`
	Skipped      bool         `json:"skipped,omitempty"`
	Reason       string       `json:"reason,omitempty"`
}

// IsCurrent checks evidence binding, not whether verification was successful.
func (report VerificationReport) IsCurrent(definitionHash, compilerVersion, samplesHash string) bool {
	return definitionHash != "" && compilerVersion != "" && samplesHash != "" &&
		report.DefinitionHash == definitionHash && report.CompilerVersion == compilerVersion && report.SamplesHash == samplesHash
}
