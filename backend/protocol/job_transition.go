package protocol

import (
	"fmt"
	"reflect"
	"time"
)

// CheckJobTransition protects pinned identities and terminal snapshots at the
// storage boundary, including callers outside the background coordinator.
func CheckJobTransition(previous, next GenerationJob) error {
	identity := func(job GenerationJob) GenerationJob {
		job.Phase, job.Revision = "", 0
		job.UpdatedAt, job.NextAttempt, job.LeaseUntil = time.Time{}, time.Time{}, time.Time{}
		job.IsCancelRequested, job.Usage, job.Issues, job.ProviderError = false, nil, nil, Value{}
		job.Task.Status, job.Task.UpstreamID, job.Task.Result = "", Value{}, nil
		return job
	}
	if !reflect.DeepEqual(identity(previous), identity(next)) {
		return fmt.Errorf("generation job identity is immutable")
	}
	if previous.Phase == JobTerminal || previous.Phase == JobUncertain {
		return fmt.Errorf("terminal or uncertain jobs cannot resume automatically")
	}
	if !previous.Task.UpstreamID.IsZero() && !equalValues(previous.Task.UpstreamID, next.Task.UpstreamID) {
		return fmt.Errorf("generation job upstream identity is immutable")
	}
	if previous.Task.Status == TaskRunning && next.Task.Status == TaskQueued {
		return fmt.Errorf("generation job cannot move from running to queued")
	}
	switch next.Phase {
	case JobSubmitting:
		if previous.Phase != JobSubmitting || next.Task.Status != TaskSubmitting {
			return fmt.Errorf("invalid submitting transition")
		}
	case JobUncertain:
		if previous.Phase != JobSubmitting || next.Task.Status != TaskUncertain {
			return fmt.Errorf("only submission can become uncertain")
		}
	case JobPolling:
		if previous.Phase == JobFetchingResult || (next.Task.Status != TaskQueued && next.Task.Status != TaskRunning) {
			return fmt.Errorf("invalid polling transition")
		}
	case JobFetchingResult:
		if next.Task.Status != TaskCompleted {
			return fmt.Errorf("result retrieval requires completed provider status")
		}
	case JobTerminal:
		if next.Task.Status != TaskCompleted && next.Task.Status != TaskFailed && next.Task.Status != TaskCancelled {
			return fmt.Errorf("terminal job requires terminal provider status")
		}
		if next.Task.Status == TaskCompleted && next.Task.Result == nil {
			return fmt.Errorf("completed job requires a retrieved result")
		}
	default:
		return fmt.Errorf("unknown generation job phase")
	}
	if (next.Phase == JobPolling || next.Phase == JobFetchingResult || next.Phase == JobTerminal) && next.Task.UpstreamID.IsZero() {
		return fmt.Errorf("accepted job requires upstream identity")
	}
	return nil
}
