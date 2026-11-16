package protocol

import (
	"context"
	"slices"
	"sync"
)

// At most one default-budget report lives with the compiled graph. Weak cache
// entries do not extend its lifetime. Failed/partial executions are not evidence.
type verificationMemo struct {
	mu     sync.Mutex
	report *VerificationReport
}

// Offline fixture execution reads requestBudgetKey via RequestBodyLimit
// (wire/request/mapping/native/phase limits) and documentMetricsKey via the
// document measurement/normalization helpers. The latter contains mutable
// execution-local facts, so even a default-sized metrics owner bypasses reuse.
// EvaluationContext values/scope/state/operation come from immutable fixtures or
// synthetic builders, not ctx. Combination/profile-only keys are unreachable.
// Cancellation and deadline expiry are observed through ctx.Err(), including
// after waiting for the memo and after cloning a hit. See the correction note
// for the full call-path inventory and the excluded context readers.
func verificationMemoEligible(ctx context.Context) bool {
	return RequestBodyLimit(ctx) == DefaultRequestBodyBytes && ctx.Value(documentMetricsKey{}) == nil && ctx.Err() == nil
}

func cloneVerificationReport(report VerificationReport) VerificationReport {
	report.Covered = slices.Clone(report.Covered)
	report.Issues = slices.Clone(report.Issues)
	report.Checks = slices.Clone(report.Checks)
	for index := range report.Checks {
		report.Checks[index].Capabilities = slices.Clone(report.Checks[index].Capabilities)
	}
	if report.Target != nil {
		target := *report.Target
		report.Target = &target
	}
	return report
}
