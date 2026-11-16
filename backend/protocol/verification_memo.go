package protocol

import (
	"slices"
	"sync"
)

// At most one default-budget report lives with the compiled graph. Weak cache
// entries do not extend its lifetime. Failed/partial executions are not evidence.
type verificationMemo struct {
	mu     sync.Mutex
	report *VerificationReport
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
