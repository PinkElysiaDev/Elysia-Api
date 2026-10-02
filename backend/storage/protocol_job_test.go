package storage

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/elysia-api/backend/protocol"
)

type jobTestExecutor struct {
	submits, polls, results, cancels int
	submitError                      error
	poll                             func(context.Context, protocol.GenerationJob) (protocol.JobUpdate, error)
}

func (executor *jobTestExecutor) Submit(context.Context, protocol.GenerationJob, *protocol.Request) (protocol.JobUpdate, error) {
	executor.submits++
	return protocol.JobUpdate{UpstreamID: protocol.StringValue("provider-id"), Status: protocol.TaskQueued}, executor.submitError
}
func (executor *jobTestExecutor) Poll(ctx context.Context, job protocol.GenerationJob) (protocol.JobUpdate, error) {
	executor.polls++
	if executor.poll != nil {
		return executor.poll(ctx, job)
	}
	return protocol.JobUpdate{Status: protocol.TaskCompleted}, nil
}
func (executor *jobTestExecutor) Cancel(context.Context, protocol.GenerationJob) (protocol.JobUpdate, error) {
	executor.cancels++
	return protocol.JobUpdate{Status: protocol.TaskCancelled}, nil
}
func (executor *jobTestExecutor) Result(context.Context, protocol.GenerationJob) (*protocol.Response, error) {
	executor.results++
	return &protocol.Response{SchemaVersion: 1, Content: []protocol.Node{{Kind: protocol.TextNode, Payload: protocol.StringValue("private result")}}, Usage: &protocol.Usage{Input: &protocol.Counter{Count: 100, Origin: protocol.ObservedCount}, CacheRead: &protocol.Counter{Count: 40, Origin: protocol.ObservedCount}, CacheCreation: &protocol.Counter{Count: 0, Origin: protocol.ObservedCount}}}, nil
}

type jobTestSettlement struct {
	store          *Store
	failAfterWrite bool
	calls          int
}

func (writer *jobTestSettlement) SettleJob(ctx context.Context, job protocol.GenerationJob) error {
	writer.calls++
	if err := writer.store.SaveUsageRecordJSON(ctx, []byte(`{"job":true}`), UsageLogItem{RequestID: job.Task.SettlementID, StartedAt: job.CreatedAt, InputTokens: 100, CacheHitTokens: 40}, job.UpdatedAt); err != nil {
		return err
	}
	if writer.failAfterWrite {
		writer.failAfterWrite = false
		return errors.New("simulated crash before outbox acknowledgement")
	}
	return nil
}

func newJobTestStore(t *testing.T, path string) *Store {
	t.Helper()
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}
func jobTestIdentity(id string) protocol.GenerationJob {
	return protocol.GenerationJob{Task: protocol.Task{ID: id, SettlementID: "settle-" + id, ModelSourceID: "source", Protocol: protocol.Identity{DefinitionID: "jobs", Family: "jobs", Revision: "1"}, CanCancel: true}, OwnerHash: "owner", GroupID: "group", IngressID: "jobs", IngressRevision: "ingress-hash", UpstreamRevision: "upstream-hash", SourceAccount: "account", SourceBaseHash: "host-hash", ModelID: "m", Model: "m", Operation: "submit", DeduplicationHash: "dedup-" + id}
}
func jobTestRequest() *protocol.Request {
	return &protocol.Request{SchemaVersion: 1, Model: protocol.StringValue("m"), Content: []protocol.Node{{Kind: protocol.TextNode, Payload: protocol.StringValue("prompt")}}}
}
func makeJobDue(t *testing.T, store *Store, id string) {
	t.Helper()
	job, err := store.ReadGenerationJob(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	job.NextAttempt = time.Now().Add(-time.Second)
	job.LeaseUntil = time.Time{}
	if _, err := store.UpdateGenerationJob(t.Context(), job, job.Revision); err != nil {
		t.Fatal(err)
	}
}
func newJobCoordinator(t *testing.T, store *Store, executor *jobTestExecutor, writer *jobTestSettlement) *protocol.JobCoordinator {
	t.Helper()
	coordinator, err := protocol.NewJobCoordinator(store, executor, writer)
	if err != nil {
		t.Fatal(err)
	}
	return coordinator
}

func TestGenerationJobRestartAndSettlementCrashWindow(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jobs.sqlite3")
	store := newJobTestStore(t, path)
	executor := &jobTestExecutor{}
	writer := &jobTestSettlement{store: store, failAfterWrite: true}
	coordinator := newJobCoordinator(t, store, executor, writer)
	job, isNew, err := coordinator.Submit(t.Context(), jobTestIdentity("one"), jobTestRequest())
	if err != nil || !isNew || job.Phase != protocol.JobPolling {
		t.Fatalf("submit: %+v %v", job, err)
	}
	if _, isNew, err := coordinator.Submit(t.Context(), jobTestIdentity("one"), jobTestRequest()); err != nil || isNew || executor.submits != 1 {
		t.Fatalf("duplicate submit: %v", err)
	}
	changed := jobTestRequest()
	changed.Model = protocol.StringValue("other")
	if _, _, err := coordinator.Submit(t.Context(), jobTestIdentity("one"), changed); !errors.Is(err, protocol.ErrIdempotencyConflict) {
		t.Fatalf("idempotency conflict: %v", err)
	}
	if _, err := coordinator.Read(t.Context(), "one", "someone-else"); !errors.Is(err, protocol.ErrJobNotFound) {
		t.Fatal("owner boundary failed")
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store = newJobTestStore(t, path)
	writer.store = store
	coordinator = newJobCoordinator(t, store, executor, writer)
	makeJobDue(t, store, "one")
	if err := coordinator.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	makeJobDue(t, store, "one")
	if err := coordinator.Tick(t.Context()); err == nil {
		t.Fatal("crash window was not exercised")
	}
	if err := coordinator.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	job, err = store.ReadGenerationJob(t.Context(), "one")
	if err != nil || job.Phase != protocol.JobTerminal || job.Task.Result == nil || job.Usage.CacheRead.Count != 40 || job.Usage.CacheCreation.Count != 0 {
		t.Fatalf("restored result: %+v %v", job, err)
	}
	_, logs, err := store.QueryUsageLogs(t.Context(), UsageQuery{Limit: 10})
	if err != nil || len(logs) != 1 || logs[0].CacheHitTokens != 40 || writer.calls != 2 {
		t.Fatalf("settlement duplicate: %+v %v calls=%d", logs, err, writer.calls)
	}
	if executor.submits != 1 || executor.results != 1 {
		t.Fatal("recovery replayed generation or result")
	}
	pending, err := store.PendingJobSettlements(t.Context(), 16)
	if err != nil || len(pending) != 0 {
		t.Fatal("outbox did not acknowledge")
	}
}

func TestGenerationJobUncertainNeverResubmits(t *testing.T) {
	store := newJobTestStore(t, filepath.Join(t.TempDir(), "uncertain.sqlite3"))
	executor := &jobTestExecutor{submitError: context.DeadlineExceeded}
	coordinator := newJobCoordinator(t, store, executor, &jobTestSettlement{store: store})
	job, _, err := coordinator.Submit(t.Context(), jobTestIdentity("timeout"), jobTestRequest())
	if !errors.Is(err, context.DeadlineExceeded) || job.Phase != protocol.JobUncertain {
		t.Fatalf("uncertain outcome lost: %+v %v", job, err)
	}
	if _, _, err := coordinator.Submit(t.Context(), jobTestIdentity("timeout"), jobTestRequest()); err != nil {
		t.Fatal(err)
	}
	if err := coordinator.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	if executor.submits != 1 || executor.polls != 0 {
		t.Fatal("uncertain task replayed")
	}
	reservation := jobTestIdentity("crashed")
	reservation.Task.Status = protocol.TaskSubmitting
	reservation.Phase = protocol.JobSubmitting
	reservation.RequestHash = "digest"
	reservation.Revision = 1
	reservation.CreatedAt = time.Now().Add(-time.Minute)
	reservation.UpdatedAt = reservation.CreatedAt
	reservation.LeaseUntil = reservation.CreatedAt.Add(time.Second)
	if _, _, err := store.CreateGenerationJob(t.Context(), reservation); err != nil {
		t.Fatal(err)
	}
	if err := coordinator.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	job, err = store.ReadGenerationJob(t.Context(), "crashed")
	if err != nil || job.Phase != protocol.JobUncertain || executor.submits != 1 {
		t.Fatal("crashed reservation was replayed")
	}
}

func TestGenerationJobCancellationRacesPollWithoutLosingOutcome(t *testing.T) {
	store := newJobTestStore(t, filepath.Join(t.TempDir(), "cancel.sqlite3"))
	executor := &jobTestExecutor{}
	coordinator := newJobCoordinator(t, store, executor, &jobTestSettlement{store: store})
	if _, _, err := coordinator.Submit(t.Context(), jobTestIdentity("cancel"), jobTestRequest()); err != nil {
		t.Fatal(err)
	}
	executor.poll = func(ctx context.Context, job protocol.GenerationJob) (protocol.JobUpdate, error) {
		_, err := coordinator.RequestCancel(ctx, job.Task.ID, job.OwnerHash)
		return protocol.JobUpdate{Status: protocol.TaskRunning}, err
	}
	makeJobDue(t, store, "cancel")
	if err := coordinator.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	job, err := store.ReadGenerationJob(t.Context(), "cancel")
	if err != nil || !job.IsCancelRequested || job.Task.Status != protocol.TaskRunning {
		t.Fatalf("cancel/poll update lost: %+v %v", job, err)
	}
	makeJobDue(t, store, "cancel")
	if err := coordinator.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	job, err = store.ReadGenerationJob(t.Context(), "cancel")
	if err != nil || job.Task.Status != protocol.TaskCancelled || executor.cancels != 1 {
		t.Fatalf("cancel not acknowledged: %+v %v", job, err)
	}
}

func TestGenerationJobRejectsIdentityAndTerminalMutations(t *testing.T) {
	store := newJobTestStore(t, filepath.Join(t.TempDir(), "immutable.sqlite3"))
	coordinator := newJobCoordinator(t, store, &jobTestExecutor{}, &jobTestSettlement{store: store})
	job, _, err := coordinator.Submit(t.Context(), jobTestIdentity("immutable"), jobTestRequest())
	if err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*protocol.GenerationJob){func(job *protocol.GenerationJob) { job.OwnerHash = "attacker" }, func(job *protocol.GenerationJob) { job.UpstreamRevision = "new" }, func(job *protocol.GenerationJob) { job.Task.UpstreamID = protocol.StringValue("another") }, func(job *protocol.GenerationJob) { job.Phase = protocol.JobSubmitting }} {
		copy := job
		mutate(&copy)
		if _, err := store.UpdateGenerationJob(t.Context(), copy, job.Revision); err == nil {
			t.Fatal("illegal job transition accepted")
		}
	}
}
