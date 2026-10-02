package protocol

import (
	"context"
	"errors"
	"fmt"
	"time"
)

const (
	DefaultJobPollInterval     = time.Second
	DefaultJobOperationTimeout = 30 * time.Second
	DefaultJobCommitTimeout    = 5 * time.Second
	DefaultJobLease            = DefaultJobOperationTimeout + 2*DefaultJobCommitTimeout
	DefaultJobBatchSize        = 16
	JobMutationAttempts        = 3
)

// JobCoordinator owns durable transitions; it never replays a generation submit
// after an uncertain result. Operator/client retries retrieve the reserved job.
type JobCoordinator struct {
	repository       JobRepository
	executor         JobExecutor
	settlements      JobSettlementWriter
	pollInterval     time.Duration
	operationTimeout time.Duration
	lease            time.Duration
}

// NewJobCoordinator binds shared persistence, provider I/O and usage delivery.
func NewJobCoordinator(repository JobRepository, executor JobExecutor, settlements JobSettlementWriter) (*JobCoordinator, error) {
	if repository == nil || executor == nil || settlements == nil {
		return nil, fmt.Errorf("job repository, executor and settlement writer are required")
	}
	return &JobCoordinator{repository: repository, executor: executor, settlements: settlements, pollInterval: DefaultJobPollInterval, operationTimeout: DefaultJobOperationTimeout, lease: DefaultJobLease}, nil
}

// Submit reserves an identity before I/O. The returned boolean says whether a
// new upstream submission occurred, allowing callers to avoid duplicate usage.
func (coordinator *JobCoordinator) Submit(ctx context.Context, job GenerationJob, request *Request) (GenerationJob, bool, error) {
	if err := ctx.Err(); err != nil {
		return GenerationJob{}, false, err
	}
	now := time.Now().UTC()
	job.Task.Status, job.Phase, job.Revision = TaskSubmitting, JobSubmitting, 1
	job.CreatedAt, job.UpdatedAt, job.LeaseUntil = now, now, now.Add(coordinator.lease)
	digest, err := JobRequestDigest(request)
	if err != nil {
		return GenerationJob{}, false, err
	}
	job.RequestHash = digest
	if err := CheckNewJob(job); err != nil {
		return GenerationJob{}, false, err
	}
	reserved, isNew, err := coordinator.repository.CreateGenerationJob(ctx, job)
	if err != nil {
		return GenerationJob{}, false, err
	}
	if !isNew {
		if reserved.RequestHash != job.RequestHash {
			return reserved, false, ErrIdempotencyConflict
		}
		return reserved, false, nil
	}
	deadline, stop := context.WithTimeout(ctx, coordinator.operationTimeout)
	update, operationErr := coordinator.executor.Submit(deadline, reserved, request)
	stop()
	if operationErr == nil {
		operationErr = applyJobUpdate(&reserved, update)
	}
	if operationErr != nil {
		reserved.Phase, reserved.Task.Status = JobUncertain, TaskUncertain
		reserved.Issues = jobIssues(reserved, "submit", operationErr)
	}
	updated, err := coordinator.commit(ctx, reserved)
	if err != nil {
		return reserved, true, err
	}
	return updated, true, operationErr
}

// Read returns only jobs belonging to the authenticated owner. Gateway group
// authorization remains a separate check because access may change over time.
func (coordinator *JobCoordinator) Read(ctx context.Context, id, owner string) (GenerationJob, error) {
	job, err := coordinator.repository.ReadGenerationJob(ctx, id)
	if err != nil {
		return job, err
	}
	if job.OwnerHash != owner {
		return GenerationJob{}, ErrJobNotFound
	}
	return job, nil
}

// RequestCancel records intent without claiming the provider acknowledged it.
// Polling/recovery performs the declared cancel operation with the pinned ID.
func (coordinator *JobCoordinator) RequestCancel(ctx context.Context, id, owner string) (GenerationJob, error) {
	for range JobMutationAttempts {
		job, err := coordinator.Read(ctx, id, owner)
		if err != nil {
			return job, err
		}
		if job.Phase == JobTerminal || job.IsCancelRequested {
			return job, nil
		}
		if !job.Task.CanCancel || job.Task.UpstreamID.IsZero() {
			return job, streamIssue(UnsupportedCapability, "/task/cancel", "job has no cancellable upstream identity")
		}
		job.IsCancelRequested = true
		job.UpdatedAt = time.Now().UTC()
		updated, err := coordinator.repository.UpdateGenerationJob(ctx, job, job.Revision)
		if !errors.Is(err, ErrJobConflict) {
			return updated, err
		}
	}
	return GenerationJob{}, ErrJobConflict
}

// Tick processes a bounded leased batch and then drains the settlement outbox.
// A submitting lease abandoned by a crash becomes uncertain, never resubmitted.
func (coordinator *JobCoordinator) Tick(ctx context.Context) error {
	var failures []error
	for range DefaultJobBatchSize {
		jobs, err := coordinator.repository.ClaimGenerationJobs(ctx, time.Now().UTC(), coordinator.lease, 1)
		if err != nil {
			return err
		}
		if len(jobs) == 0 {
			break
		}
		if err := coordinator.process(ctx, jobs[0]); err != nil {
			failures = append(failures, err)
		}
	}
	pending, err := coordinator.repository.PendingJobSettlements(ctx, DefaultJobBatchSize)
	if err != nil {
		failures = append(failures, err)
	}
	for _, job := range pending {
		if err := coordinator.settlements.SettleJob(ctx, job); err != nil {
			failures = append(failures, err)
			continue
		}
		if err := coordinator.repository.AcknowledgeJobSettlement(ctx, job.Task.ID); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

// Run resumes pending polling, result retrieval and settlement after restart.
// onError reports infrastructure failures without changing provider task status.
func (coordinator *JobCoordinator) Run(ctx context.Context, onError func(error)) {
	ticker := time.NewTicker(coordinator.pollInterval)
	defer ticker.Stop()
	for {
		if err := coordinator.Tick(ctx); err != nil && ctx.Err() == nil {
			onError(err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (coordinator *JobCoordinator) process(ctx context.Context, job GenerationJob) error {
	if job.Phase == JobSubmitting {
		job.Phase, job.Task.Status = JobUncertain, TaskUncertain
		job.Issues = jobIssues(job, "recover", fmt.Errorf("submission lease expired before its outcome was persisted; no automatic resubmission"))
		_, err := coordinator.commit(ctx, job)
		return err
	}
	deadline, stop := context.WithTimeout(ctx, coordinator.operationTimeout)
	defer stop()
	stage := "poll"
	var operationErr error
	var update JobUpdate
	switch {
	case job.IsCancelRequested:
		stage = "cancel"
		update, operationErr = coordinator.executor.Cancel(deadline, job)
		if operationErr == nil {
			operationErr = applyJobUpdate(&job, update)
		}
		if operationErr == nil {
			job.IsCancelRequested = false
		}
	case job.Phase == JobFetchingResult:
		stage = "result"
		var response *Response
		response, operationErr = coordinator.executor.Result(deadline, job)
		if operationErr == nil && response == nil {
			operationErr = streamIssue(UpstreamContractViolation, "/task/result", "completed task returned no result")
		}
		if operationErr == nil {
			job.Task.Result = response
			job.Usage = MergeUsage(job.Usage, response.Usage)
			job.Phase = JobTerminal
		}
	case job.Phase == JobPolling:
		update, operationErr = coordinator.executor.Poll(deadline, job)
		if operationErr == nil {
			operationErr = applyJobUpdate(&job, update)
		}
	default:
		return fmt.Errorf("claimed job has unsupported phase %q", job.Phase)
	}
	job.Issues = nil
	if operationErr != nil {
		job.Issues = jobIssues(job, stage, operationErr)
	}
	_, err := coordinator.commit(ctx, job)
	return err
}

func (coordinator *JobCoordinator) commit(ctx context.Context, job GenerationJob) (GenerationJob, error) {
	lease := job.LeaseUntil
	job.UpdatedAt = time.Now().UTC()
	job.LeaseUntil = time.Time{}
	job.NextAttempt = job.UpdatedAt.Add(coordinator.pollInterval)
	deadline, stop := context.WithTimeout(context.WithoutCancel(ctx), DefaultJobCommitTimeout)
	defer stop()
	updated, err := coordinator.repository.UpdateGenerationJob(deadline, job, job.Revision)
	if !errors.Is(err, ErrJobConflict) {
		return updated, err
	}
	current, readErr := coordinator.repository.ReadGenerationJob(deadline, job.Task.ID)
	if readErr != nil {
		return job, readErr
	}
	// A cancellation may race a leased poll/result operation. Rebase only the
	// one cancellation mutation on that exact lease; never overwrite new work.
	if current.Revision != job.Revision+1 || !current.IsCancelRequested || !current.LeaseUntil.Equal(lease) {
		return job, err
	}
	job.IsCancelRequested = job.Phase != JobTerminal
	return coordinator.repository.UpdateGenerationJob(deadline, job, current.Revision)
}

func jobIssues(job GenerationJob, stage string, err error) []ConversionIssue {
	var conversion *ConversionError
	if errors.As(err, &conversion) {
		return append([]ConversionIssue(nil), conversion.Issues...)
	}
	code := TaskOperationFailed
	if stage == "submit" || stage == "recover" {
		code = TaskSubmissionUncertain
	}
	return []ConversionIssue{{Code: code, Severity: SeverityError, Protocol: job.Task.Protocol, Stage: "task_" + stage, Path: "/task", Reason: err.Error(), Suggestion: "Inspect the pinned job and upstream outcome; do not resubmit an uncertain generation."}}
}
