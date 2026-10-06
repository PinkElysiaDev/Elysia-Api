package protocol

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	ErrJobNotFound         = errors.New("generation job not found")
	ErrJobConflict         = errors.New("generation job changed")
	ErrIdempotencyConflict = errors.New("idempotency key was used for a different generation request")
)

const (
	TaskOperationFailed     IssueCode = "task_operation_failed"
	TaskSubmissionUncertain IssueCode = "task_submission_uncertain"
)

// JobPhase records gateway work independently from the provider's task status.
type JobPhase string

const (
	JobSubmitting     JobPhase = "submitting"
	JobUncertain      JobPhase = "uncertain"
	JobPolling        JobPhase = "polling"
	JobFetchingResult JobPhase = "fetching_result"
	JobTerminal       JobPhase = "terminal"
)

// GenerationJob persists identities and operational state, never credentials or
// submitted prompts. The result is operational data required by the result API;
// the repository applies the store's encryption policy to that payload.
type GenerationJob struct {
	Task              Task              `json:"task"`
	OwnerHash         string            `json:"ownerHash"`
	GroupID           string            `json:"groupId"`
	IngressID         string            `json:"ingressId"`
	IngressRevision   string            `json:"ingressRevision"`
	IngressOperation  string            `json:"ingressOperation"`
	GroupName         string            `json:"groupName"`
	UpstreamRevision  string            `json:"upstreamRevision"`
	SourceAccount     string            `json:"sourceAccount"`
	SourceBaseHash    string            `json:"sourceBaseHash"`
	ModelID           string            `json:"modelId"`
	Model             string            `json:"model"`
	Operation         string            `json:"operation"`
	Binding           Binding           `json:"binding"`
	RequestHash       string            `json:"requestHash"`
	DeduplicationHash string            `json:"deduplicationHash"`
	Phase             JobPhase          `json:"phase"`
	Revision          int64             `json:"revision"`
	CreatedAt         time.Time         `json:"createdAt"`
	UpdatedAt         time.Time         `json:"updatedAt"`
	NextAttempt       time.Time         `json:"nextAttempt"`
	LeaseUntil        time.Time         `json:"leaseUntil"`
	IsCancelRequested bool              `json:"cancelRequested"`
	Usage             *Usage            `json:"usage,omitempty"`
	Issues            []ConversionIssue `json:"issues,omitempty"`
	ProviderError     Value             `json:"providerError,omitzero"`
}

// JobUpdate is a decoded provider status. Completion can precede result fetch.
type JobUpdate struct {
	UpstreamID Value      `json:"id,omitzero"`
	Status     TaskStatus `json:"status"`
	Usage      *Usage     `json:"usage,omitempty"`
	Error      Value      `json:"error,omitzero"`
}

// JobRepository owns atomic reservation, leases, compare-and-swap transitions,
// and a durable settlement outbox. A terminal transition enqueues at most one
// settlement in the same transaction as the job update.
type JobRepository interface {
	CreateGenerationJob(context.Context, GenerationJob) (GenerationJob, bool, error)
	ReadGenerationJob(context.Context, string) (GenerationJob, error)
	UpdateGenerationJob(context.Context, GenerationJob, int64) (GenerationJob, error)
	ClaimGenerationJobs(context.Context, time.Time, time.Duration, int) ([]GenerationJob, error)
	PendingJobSettlements(context.Context, int) ([]GenerationJob, error)
	AcknowledgeJobSettlement(context.Context, string) error
}

// JobExecutor resolves the recorded account and immutable protocol revisions.
// Submit is called once for a newly reserved job; recovery only polls/fetches.
type JobExecutor interface {
	Submit(context.Context, GenerationJob, *Request) (JobUpdate, error)
	Poll(context.Context, GenerationJob) (JobUpdate, error)
	Result(context.Context, GenerationJob) (*Response, error)
	Cancel(context.Context, GenerationJob) (JobUpdate, error)
}

// JobSettlementWriter must be idempotent by Task.SettlementID. A crash between
// delivery and outbox acknowledgement may deliver the same snapshot again.
type JobSettlementWriter interface {
	SettleJob(context.Context, GenerationJob) error
}

// JobRequestDigest compares semantic submissions without persisting prompts.
func JobRequestDigest(request *Request) (string, error) {
	value, err := EncodeValue(request)
	if err != nil {
		return "", err
	}
	var object any
	if err := value.Decode(&object); err != nil {
		return "", err
	}
	canonical, err := EncodeValue(object)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canonical.Bytes())
	return hex.EncodeToString(sum[:]), nil
}

// jobHashSeparator joins hash components. NUL cannot appear in the owner,
// ingress or caller key, so ("ab","c") and ("a","bc") never collide.
const jobHashSeparator = "\x00"

// JobDeduplicationHash scopes caller idempotency keys to an owner and ingress.
// Without a caller key the generated job ID is unique for that submission.
func JobDeduplicationHash(owner, ingress, key, id string) string {
	if key == "" {
		key = id
	}
	sum := sha256.Sum256([]byte(strings.Join([]string{owner, ingress, key}, jobHashSeparator)))
	return hex.EncodeToString(sum[:])
}

// CheckNewJob validates the durable reservation before external submission.
func CheckNewJob(job GenerationJob) error {
	if job.Task.ID == "" || job.Task.SettlementID == "" || job.Task.ModelSourceID == "" || job.Task.Protocol.DefinitionID == "" || job.OwnerHash == "" || job.GroupID == "" || job.IngressID == "" || job.IngressRevision == "" || job.UpstreamRevision == "" || job.SourceAccount == "" || job.SourceBaseHash == "" || job.Model == "" || job.Operation == "" || job.RequestHash == "" || job.DeduplicationHash == "" {
		return fmt.Errorf("job requires pinned protocol, owner, account, model and request identities")
	}
	if job.Task.Status != TaskSubmitting || job.Phase != JobSubmitting || !job.Task.UpstreamID.IsZero() || job.Task.Result != nil || job.Revision != 1 || job.CreatedAt.IsZero() || !job.LeaseUntil.After(job.CreatedAt) {
		return fmt.Errorf("new job must reserve one submitting operation with a lease")
	}
	return nil
}

func applyJobUpdate(job *GenerationJob, update JobUpdate) error {
	next := *job
	if err := applyAcceptedJobUpdate(&next, update); err != nil {
		return err
	}
	*job = next
	return nil
}

func applyAcceptedJobUpdate(job *GenerationJob, update JobUpdate) error {
	if !update.UpstreamID.IsZero() {
		id, err := readString(update.UpstreamID)
		if err != nil || id == "" {
			return streamIssue(UpstreamContractViolation, "/task/upstreamId", "upstream job identity must be a nonempty string")
		}
		if !job.Task.UpstreamID.IsZero() && !equalValues(job.Task.UpstreamID, update.UpstreamID) {
			return streamIssue(InvalidAssociation, "/task/upstreamId", "upstream job identity changed")
		}
		job.Task.UpstreamID = update.UpstreamID
	}
	if job.Task.UpstreamID.IsZero() {
		return streamIssue(UpstreamContractViolation, "/task/upstreamId", "accepted job has no upstream identity")
	}
	if job.Task.Status == TaskRunning && update.Status == TaskQueued {
		return streamIssue(UpstreamContractViolation, "/task/status", "job moved backwards from running to queued")
	}
	switch update.Status {
	case TaskQueued, TaskRunning:
		job.Phase = JobPolling
	case TaskCompleted:
		job.Phase = JobFetchingResult
	case TaskFailed, TaskCancelled:
		job.Phase = JobTerminal
	default:
		return streamIssue(UpstreamContractViolation, "/task/status", "provider returned an unsupported task status")
	}
	job.Task.Status = update.Status
	if !update.Error.IsZero() {
		job.ProviderError = update.Error
	}
	job.Usage = MergeUsage(job.Usage, update.Usage)
	return nil
}
