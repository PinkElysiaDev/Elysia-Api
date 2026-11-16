package protocol

import (
	"fmt"
	"slices"
)

const ReasoningControlsFeature = "conversion.reasoning.v1"

// Core reasoning checks have no intrinsic wire direction. The request boundary
// supplies it; they must not identify a request failure as an upstream event.
func reasoningValidationIssue(code IssueCode, path, reason string) error {
	return &ConversionError{Issues: []ConversionIssue{{
		Code: code, Severity: SeverityError, Stage: "request.validation",
		Path: path, Reason: reason, Suggestion: fmt.Sprintf("correct the request controls or declared %s rule", path),
	}}}
}

// ReasoningIntent keeps independent controls independent: Anthropic effort
// also affects generation when thinking is disabled or a budget is present.
type ReasoningIntent struct {
	Mode        string `json:"mode,omitempty"`
	Effort      Value  `json:"effort,omitzero"`
	EffortScope string `json:"effortScope,omitempty"`
	Budget      Value  `json:"budget,omitzero"`
	Summary     Value  `json:"summary,omitzero"`
	Interleaved bool   `json:"interleaved,omitempty"`
	// HistoryRetention controls supplied history independently of new generation.
	HistoryRetention Value `json:"historyRetention,omitzero"`
}

func controlPresent(v Value) bool { return !v.IsZero() && !v.IsNull() }
func controlText(v Value) string  { var s string; _ = v.Decode(&s); return s }
func controlInt(v Value) int64    { var n int64; _ = v.Decode(&n); return n }
func controlValue(v any) Value    { result, _ := EncodeValue(v); return result }

func ValidateReasoningIntent(r *ReasoningIntent) error {
	if r == nil {
		return nil
	}
	if !r.HistoryRetention.IsZero() && !r.HistoryRetention.IsString("all") {
		return reasoningValidationIssue(InvalidInput, "/reasoning/historyRetention", "history retention must be all when present")
	}
	if !slices.Contains([]string{"", "enabled", "disabled", "auto", "between_tools"}, r.Mode) {
		return reasoningValidationIssue(InvalidInput, "/reasoning/mode", "unknown thinking mode")
	}
	if controlPresent(r.Effort) {
		var effort string
		if r.Effort.Decode(&effort) != nil || !slices.Contains([]string{"none", "minimal", "low", "medium", "high", "xhigh", "max"}, effort) {
			return reasoningValidationIssue(InvalidInput, "/reasoning/effort", "unknown or non-string effort")
		}
		if r.EffortScope != "reasoning" && r.EffortScope != "generation" {
			return reasoningValidationIssue(InvalidInput, "/reasoning/effortScope", "effort requires its reasoning or generation scope")
		}
	}
	if controlPresent(r.Budget) {
		var budget int64
		if r.Budget.Decode(&budget) != nil || budget < -1 {
			return reasoningValidationIssue(InvalidInput, "/reasoning/budget", "budget must be an integer >= -1 without overflow")
		}
	}
	if controlPresent(r.Budget) && ((r.Mode == "disabled" && controlInt(r.Budget) != 0) || (r.Mode == "auto" && controlInt(r.Budget) != -1)) {
		return reasoningValidationIssue(InvalidInput, "/reasoning/budget", "budget conflicts with thinking mode")
	}
	if controlPresent(r.Effort) && r.EffortScope == "reasoning" && r.Mode == "disabled" && controlText(r.Effort) != "none" {
		return reasoningValidationIssue(InvalidInput, "/reasoning/effort", "reasoning effort conflicts with disabled mode")
	}
	if controlPresent(r.Budget) && r.Mode == "enabled" && controlInt(r.Budget) <= 0 {
		return reasoningValidationIssue(InvalidInput, "/reasoning/budget", "enabled thinking requires a positive budget")
	}
	if r.Interleaved && (r.Mode != "enabled" || !controlPresent(r.Budget) || controlInt(r.Budget) <= 0) {
		return reasoningValidationIssue(InvalidInput, "/reasoning/interleaved", "interleaved thinking requires manual thinking with a positive budget")
	}
	if controlPresent(r.Summary) {
		var summary string
		if r.Summary.Decode(&summary) != nil || !slices.Contains([]string{"auto", "concise", "detailed", "summarized", "omitted", "updates"}, summary) {
			return reasoningValidationIssue(InvalidInput, "/reasoning/summary", "unknown or non-string summary preference")
		}
	}
	return nil
}
