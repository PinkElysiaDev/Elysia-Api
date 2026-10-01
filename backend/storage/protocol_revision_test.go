package storage

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/elysia-api/backend/protocol"
)

func newRevisionService(t *testing.T, store *Store) *protocol.Service {
	t.Helper()
	compiler, err := protocol.NewCompiler(protocol.DefaultLimits(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	service, err := protocol.NewService(compiler, store)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func revisionFixture(t *testing.T, version string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "protocol", "testdata", "text-alpha.json"))
	if err != nil {
		t.Fatal(err)
	}
	value, err := protocol.ParseValue(raw)
	if err != nil {
		t.Fatal(err)
	}
	edited, err := protocol.ApplyMutations(value, []protocol.Mutation{{Op: protocol.SetValue, Path: "/version", Value: protocol.StringValue(version)}}, protocol.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	return edited.Bytes()
}

func verifySavedDraft(t *testing.T, service *protocol.Service, raw []byte, previous string) (protocol.Draft, protocol.Revision) {
	t.Helper()
	draft, issues, err := service.SaveDraft(t.Context(), "text-alpha", raw, previous)
	if err != nil {
		t.Fatal(err)
	}
	if err := protocol.IssuesError(issues); err != nil {
		t.Fatal(err)
	}
	revision, report, err := service.VerifyDraft(t.Context(), draft.ProtocolID, draft.Hash)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Passed {
		t.Fatalf("fixture failed verification: %+v", report.Issues)
	}
	return draft, revision
}

func TestProtocolRevisionActivationPinningAndRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "protocol.sqlite")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	service := newRevisionService(t, store)
	firstDraft, first := verifySavedDraft(t, service, revisionFixture(t, "1"), "")
	if _, exists := service.Pin("text-alpha"); exists {
		t.Fatal("draft save/verification activated a revision")
	}
	if _, err := service.Activate(t.Context(), first.ProtocolID, first.Hash, ""); err != nil {
		t.Fatal(err)
	}
	pinned, exists := service.Pin("text-alpha")
	if !exists || pinned.Hash() != first.Hash {
		t.Fatal("first revision was not activated")
	}
	_, second := verifySavedDraft(t, service, revisionFixture(t, "2"), firstDraft.Hash)
	if current, _ := service.Pin("text-alpha"); current.Hash() != first.Hash {
		t.Fatal("editing draft changed active requests")
	}
	activation, err := service.Activate(t.Context(), second.ProtocolID, second.Hash, first.Hash)
	if err != nil {
		t.Fatal(err)
	}
	if activation.Generation != 2 || pinned.Hash() != first.Hash || pinned.Identity().Revision != "1" {
		t.Fatal("activation mutated an in-flight revision")
	}
	current, _ := service.Pin("text-alpha")
	if current.Hash() != second.Hash {
		t.Fatal("future requests did not observe new revision")
	}
	changes, err := service.Diff(t.Context(), "text-alpha", first.Hash, second.Hash)
	if err != nil || len(changes) != 1 || changes[0].Path != "/version" {
		t.Fatalf("revision diff: %+v %v", changes, err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { reopened.Close() })
	restarted := newRevisionService(t, reopened)
	if err := restarted.Reload(t.Context()); err != nil {
		t.Fatal(err)
	}
	if active, exists := restarted.Pin("text-alpha"); !exists || active.Hash() != second.Hash {
		t.Fatal("restart lost active revision")
	}
	if _, err := restarted.Rollback(t.Context(), "text-alpha", first.Hash, second.Hash); err != nil {
		t.Fatal(err)
	}
	if active, _ := restarted.Pin("text-alpha"); active.Hash() != first.Hash {
		t.Fatal("rollback did not pin old revision")
	}
	if _, err := restarted.Activate(t.Context(), "text-alpha", second.Hash, second.Hash); !errors.Is(err, protocol.ErrRevisionConflict) {
		t.Fatalf("stale activation was accepted: %v", err)
	}
}

func TestProtocolDraftConcurrentCompareAndSwap(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "protocol.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	service := newRevisionService(t, store)
	first, _ := verifySavedDraft(t, service, revisionFixture(t, "1"), "")
	var wait sync.WaitGroup
	results := make(chan error, 2)
	for _, raw := range [][]byte{revisionFixture(t, "2"), revisionFixture(t, "3")} {
		wait.Add(1)
		go func(body []byte) {
			defer wait.Done()
			_, _, err := service.SaveDraft(t.Context(), "text-alpha", body, first.Hash)
			results <- err
		}(raw)
	}
	wait.Wait()
	close(results)
	saved, conflicts := 0, 0
	for err := range results {
		if err == nil {
			saved++
		} else if errors.Is(err, protocol.ErrRevisionConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if saved != 1 || conflicts != 1 {
		t.Fatalf("concurrent edits saved=%d conflict=%d", saved, conflicts)
	}
	if _, _, err := service.VerifyDraft(t.Context(), "text-alpha", first.Hash); !errors.Is(err, protocol.ErrRevisionConflict) {
		t.Fatalf("stale draft was verified: %v", err)
	}
}

func TestProtocolFailedActivationAndReloadPreserveSnapshot(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "protocol.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	service := newRevisionService(t, store)
	draft, revision := verifySavedDraft(t, service, revisionFixture(t, "1"), "")
	if _, err := service.Activate(t.Context(), "text-alpha", revision.Hash, ""); err != nil {
		t.Fatal(err)
	}
	value, err := protocol.ParseValue(revisionFixture(t, "2"))
	if err != nil {
		t.Fatal(err)
	}
	flag, err := protocol.EncodeValue(true)
	if err != nil {
		t.Fatal(err)
	}
	invalid, err := protocol.ApplyMutations(value, []protocol.Mutation{{Op: protocol.SetValue, Path: "/capabilities/tools.function", Value: flag}}, protocol.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	badDraft, issues, err := service.SaveDraft(t.Context(), "text-alpha", invalid.Bytes(), draft.Hash)
	if err != nil || protocol.IssuesError(issues) != nil {
		t.Fatalf("valid but unverified draft: %v %+v", err, issues)
	}
	badRevision, report, err := service.VerifyDraft(t.Context(), "text-alpha", badDraft.Hash)
	if err != nil {
		t.Fatal(err)
	}
	if report.Passed {
		t.Fatal("unproven tools passed")
	}
	if _, err := service.Activate(t.Context(), "text-alpha", badRevision.Hash, revision.Hash); err == nil {
		t.Fatal("failed verification activated")
	}
	pinned, _ := service.Pin("text-alpha")
	if pinned.Hash() != revision.Hash {
		t.Fatal("failed activation disturbed current revision")
	}
	if _, err := store.db.ExecContext(t.Context(), "UPDATE protocol_revisions SET definition=? WHERE content_hash=?", string(revisionFixture(t, "corrupt")), revision.Hash); err != nil {
		t.Fatal(err)
	}
	if err := service.Reload(t.Context()); err == nil {
		t.Fatal("tampered revision loaded")
	}
	if current, _ := service.Pin("text-alpha"); current != pinned {
		t.Fatal("failed reload replaced the snapshot")
	}
	restarted := newRevisionService(t, store)
	if err := restarted.Reload(t.Context()); err == nil {
		t.Fatal("restart accepted corrupt revision")
	}
	if _, exists := restarted.Pin("text-alpha"); exists {
		t.Fatal("restart partially published invalid registry")
	}
}

func TestProtocolRollbackRequiresCurrentCompilerEvidence(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "protocol.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	service := newRevisionService(t, store)
	_, revision := verifySavedDraft(t, service, revisionFixture(t, "1"), "")
	if _, err := store.db.ExecContext(t.Context(), "UPDATE protocol_verification_reports SET compiler_version='obsolete'"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Rollback(t.Context(), "text-alpha", revision.Hash, ""); err == nil {
		t.Fatal("stale evidence authorized rollback")
	}
	report, err := service.VerifyRevision(t.Context(), "text-alpha", revision.Hash)
	if err != nil || !report.Passed {
		t.Fatalf("reverification failed: %v %+v", err, report.Issues)
	}
	if _, err := service.Activate(t.Context(), "text-alpha", revision.Hash, ""); err != nil {
		t.Fatal(err)
	}
}

func TestProtocolConcurrentReadersKeepPinnedRevisions(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "protocol.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	service := newRevisionService(t, store)
	draft, first := verifySavedDraft(t, service, revisionFixture(t, "1"), "")
	_, second := verifySavedDraft(t, service, revisionFixture(t, "2"), draft.Hash)
	if _, err := service.Activate(t.Context(), "text-alpha", first.Hash, ""); err != nil {
		t.Fatal(err)
	}
	var wait sync.WaitGroup
	failures := make(chan error, 8)
	for worker := 0; worker < 8; worker++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for iteration := 0; iteration < 60; iteration++ {
				pinned, exists := service.Pin("text-alpha")
				if !exists {
					failures <- fmt.Errorf("active entry disappeared")
					return
				}
				hash := pinned.Hash()
				if hash != first.Hash && hash != second.Hash {
					failures <- fmt.Errorf("unverified revision published")
					return
				}
				definition := pinned.Definition()
				request, err := pinned.DecodeRequest(t.Context(), definition.Samples[0].Input.Bytes(), protocol.EvaluationContext{})
				if err != nil {
					failures <- err
					return
				}
				if _, err := pinned.EncodeRequest(t.Context(), request, protocol.EvaluationContext{}); err != nil {
					failures <- err
					return
				}
				if pinned.Hash() != hash {
					failures <- fmt.Errorf("in-flight request changed revision")
					return
				}
			}
		}()
	}
	current := first.Hash
	for iteration := 0; iteration < 20; iteration++ {
		next := second.Hash
		if current == second.Hash {
			next = first.Hash
		}
		if _, err := service.Activate(t.Context(), "text-alpha", next, current); err != nil {
			t.Fatal(err)
		}
		current = next
		if err := service.Reload(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	wait.Wait()
	close(failures)
	for err := range failures {
		t.Error(err)
	}
}
