package storage

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/elysia-api/backend/protocol"
)

type outcomeSettlement struct {
	jobTestSettlement
	expected protocol.TaskStatus
}

func (writer *outcomeSettlement) SettleJob(ctx context.Context, job protocol.GenerationJob) error {
	if job.Task.Status != writer.expected {
		return fmt.Errorf("settlement used %s instead of %s", job.Task.Status, writer.expected)
	}
	return writer.jobTestSettlement.SettleJob(ctx, job)
}

func TestLegacyFailedJobSettlesActualOutcomeWithoutRewritingHistory(t *testing.T) {
	for _, isDelivered := range []bool{false, true} {
		t.Run(fmt.Sprintf("delivered=%v", isDelivered), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "legacy.sqlite")
			store := newJobTestStore(t, path)
			executor := &jobTestExecutor{}
			writer := &outcomeSettlement{jobTestSettlement: jobTestSettlement{store: store}, expected: protocol.TaskFailed}
			coordinator, err := protocol.NewJobCoordinator(store, executor, writer)
			if err != nil {
				t.Fatal(err)
			}
			job, _, err := coordinator.Submit(t.Context(), jobTestIdentity("legacy"), jobTestRequest())
			if err != nil {
				t.Fatal(err)
			}
			// Seed the old representation directly; current storage forbids creating it.
			job.Phase, job.Task.Status = protocol.JobTerminal, protocol.TaskCompleted
			job.Task.Result = &protocol.Response{Status: protocol.StringValue("failed"), Error: protocol.StringValue("synthetic provider error")}
			payload, result, err := store.encodeGenerationJob(job)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = store.db.Exec(`UPDATE generation_jobs SET phase=?,job=?,result=? WHERE id=?`, job.Phase, payload, result, job.Task.ID); err != nil {
				t.Fatal(err)
			}
			if _, err = store.db.Exec(`INSERT INTO generation_job_settlements(job_id,settlement_id,is_delivered) VALUES(?,?,?)`, job.Task.ID, job.Task.SettlementID, isDelivered); err != nil {
				t.Fatal(err)
			}
			if err = store.Close(); err != nil {
				t.Fatal(err)
			}
			store = newJobTestStore(t, path)
			writer.store = store
			coordinator, err = protocol.NewJobCoordinator(store, executor, writer)
			if err != nil {
				t.Fatal(err)
			}
			view, err := coordinator.Read(t.Context(), job.Task.ID, job.OwnerHash)
			if err != nil || view.Task.Status != protocol.TaskFailed {
				t.Fatalf("legacy view: %+v %v", view, err)
			}
			duplicate, isNew, err := coordinator.Submit(t.Context(), jobTestIdentity("legacy"), jobTestRequest())
			if err != nil || isNew || duplicate.Task.Status != protocol.TaskFailed {
				t.Fatalf("duplicate view: %+v %v", duplicate, err)
			}
			for range 3 {
				if err := coordinator.Tick(t.Context()); err != nil {
					t.Fatal(err)
				}
			}
			wantCalls := 1
			if isDelivered {
				wantCalls = 0
			}
			if writer.calls != wantCalls || executor.submits != 1 || executor.results != 0 || executor.polls != 0 {
				t.Fatal("legacy settlement repeated provider work or accounting")
			}
			raw, err := store.ReadGenerationJob(t.Context(), job.Task.ID)
			if err != nil || raw.Task.Status != protocol.TaskCompleted || raw.Revision != job.Revision {
				t.Fatal("legacy history was rewritten")
			}
		})
	}
}

type failedResultExecutor struct {
	jobTestExecutor
	status string
}

func (executor *failedResultExecutor) Result(context.Context, protocol.GenerationJob) (*protocol.Response, error) {
	executor.results++
	return &protocol.Response{Status: protocol.StringValue(executor.status), Usage: &protocol.Usage{Input: &protocol.Counter{Count: 3, Origin: protocol.ObservedCount}}, Error: protocol.StringValue("synthetic upstream failure")}, nil
}
func TestFailedJobResultIsTerminalFailure(t *testing.T) {
	for _, status := range []string{"failed", "cancelled", "canceled"} {
		t.Run(status, func(t *testing.T) {
			store := newJobTestStore(t, filepath.Join(t.TempDir(), "jobs.sqlite"))
			executor := &failedResultExecutor{status: status}
			settlements := &jobTestSettlement{store: store}
			coordinator, err := protocol.NewJobCoordinator(store, executor, settlements)
			if err != nil {
				t.Fatal(err)
			}
			job, _, err := coordinator.Submit(t.Context(), jobTestIdentity("review"), jobTestRequest())
			if err != nil {
				t.Fatal(err)
			}
			for range 2 {
				makeJobDue(t, store, job.Task.ID)
				if err := coordinator.Tick(t.Context()); err != nil {
					t.Fatal(err)
				}
			}
			expected := protocol.TaskFailed
			if status != "failed" {
				expected = protocol.TaskCancelled
			}
			got, err := store.ReadGenerationJob(t.Context(), job.Task.ID)
			if err != nil || got.Phase != protocol.JobTerminal || got.Task.Status != expected || !got.Task.Result.Status.IsString(status) || got.Usage.Input.Count != 3 {
				t.Fatalf("failure misclassified: %+v %v", got, err)
			}
			for range 3 {
				if err := coordinator.Tick(t.Context()); err != nil {
					t.Fatal(err)
				}
			}
			if executor.results != 1 || executor.submits != 1 || settlements.calls != 1 {
				t.Fatal("failed generation was repeated or settled twice")
			}
		})
	}
}
