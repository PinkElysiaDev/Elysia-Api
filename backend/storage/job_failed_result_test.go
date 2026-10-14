package storage

import (
	"context"
	"github.com/elysia-api/backend/protocol"
	"path/filepath"
	"testing"
)

type reviewFailedResultExecutor struct{ jobTestExecutor }

func (executor *reviewFailedResultExecutor) Result(context.Context, protocol.GenerationJob) (*protocol.Response, error) {
	executor.results++
	return &protocol.Response{Status: protocol.StringValue("failed"), Error: protocol.StringValue("synthetic upstream failure")}, nil
}
func TestFailedJobResultIsTerminalFailure(t *testing.T) {
	store := newJobTestStore(t, filepath.Join(t.TempDir(), "jobs.sqlite"))
	executor := &reviewFailedResultExecutor{}
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
	got, err := store.ReadGenerationJob(t.Context(), job.Task.ID)
	if err != nil || got.Phase != protocol.JobTerminal || got.Task.Status != protocol.TaskFailed || got.Task.Result.Status != protocol.StringValue("failed") {
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
}
