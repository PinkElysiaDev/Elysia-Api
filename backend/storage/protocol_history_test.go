package storage

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/elysia-api/backend/protocol"
)

func TestProtocolHistoryArchiveConflictPurgeAndRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.sqlite")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	service := newRevisionService(t, store)
	draft, first := verifySavedDraft(t, service, revisionFixture(t, "1"), "")
	if _, err := service.Activate(t.Context(), first.ProtocolID, first.Hash, ""); err != nil {
		t.Fatal(err)
	}
	_, second := verifySavedDraft(t, service, revisionFixture(t, "2"), draft.Hash)
	binding := ProtocolBinding{Kind: "source", SourceID: "source", Binding: protocol.Binding{ProtocolID: first.ProtocolID, RevisionHash: first.Hash}}
	if err := store.SaveProtocolBinding(t.Context(), binding); err != nil {
		t.Fatal(err)
	}
	baseline, err := store.ProtocolUpgradeBaseline(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ArchiveProtocol(t.Context(), first.ProtocolID, "stale", "", nil); !errors.Is(err, protocol.ErrRevisionConflict) {
		t.Fatal(err)
	}
	if items, _ := store.ListProtocolHistory(t.Context()); len(items) != 0 {
		t.Fatal("stale archive wrote history")
	}
	binding.Unbound = true
	binding.Binding = protocol.Binding{}
	if err := service.RemoveActive(first.ProtocolID, func() error {
		return store.ArchiveProtocol(t.Context(), first.ProtocolID, baseline, "", []ProtocolBinding{binding})
	}); err != nil {
		t.Fatal(err)
	}
	if _, ok := service.Pin(first.ProtocolID); ok {
		t.Fatal("archived protocol remains loaded")
	}
	if _, err := store.ReadProtocolDraft(t.Context(), first.ProtocolID); !errors.Is(err, protocol.ErrNotFound) {
		t.Fatal(err)
	}
	items, err := store.ListProtocolHistory(t.Context())
	if err != nil || len(items) < 2 {
		t.Fatal(err, items)
	}
	// An independently retained revision and its reports can be physically removed.
	if err := store.DeleteProtocolHistory(t.Context(), first.ProtocolID+"~"+first.Hash); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReadProtocolRevision(t.Context(), first.ProtocolID, first.Hash); !errors.Is(err, protocol.ErrNotFound) {
		t.Fatal(err)
	}
	if _, err := store.ReadProtocolRevision(t.Context(), second.ProtocolID, second.Hash); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	bindings, err := store.ListProtocolBindings(t.Context())
	if err != nil || len(bindings) != 1 || !bindings[0].Unbound {
		t.Fatal(err, bindings)
	}
	if _, err := store.ReadProtocolHistory(t.Context(), first.ProtocolID+"~"+first.Hash); !errors.Is(err, protocol.ErrNotFound) {
		t.Fatal("deleted history reappeared", err)
	}
	if err := newRevisionService(t, store).Reload(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestProtocolHistoryPresetUpdatesBackfillAndRollback(t *testing.T) {
	store := newJobTestStore(t, filepath.Join(t.TempDir(), "presets.sqlite"))
	compiler, err := protocol.NewCompiler(protocol.DefaultLimits(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	var hashes []string
	for _, version := range []string{"1", "2", "3"} {
		value, err := protocol.ParseValue(revisionFixture(t, version))
		if err != nil {
			t.Fatal(err)
		}
		value, err = protocol.ApplyMutations(value, []protocol.Mutation{{Op: protocol.SetValue, Path: "/id", Value: protocol.StringValue("anthropic-messages")}}, protocol.DefaultLimits())
		if err != nil {
			t.Fatal(err)
		}
		compiled, issues := compiler.Compile(value.Bytes())
		if err := protocol.IssuesError(issues); err != nil {
			t.Fatal(err)
		}
		now := time.Now().UTC()
		entry := ProtocolUpgradeRevision{Revision: protocol.Revision{ProtocolID: "anthropic-messages", Hash: compiled.Hash(), Definition: value, CreatedAt: now}, Draft: protocol.Draft{ProtocolID: "anthropic-messages", Hash: compiled.Hash(), Definition: value, UpdatedAt: now}, Report: protocol.Verify(t.Context(), compiled)}
		tx, err := store.db.BeginTx(t.Context(), nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := writeProtocolUpgradeRevision(t.Context(), tx, entry); err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
		// Simulate a later failure in the same update: neither archive nor activation may leak.
		if version == "3" {
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			items, _ := store.ListProtocolHistory(t.Context())
			if len(items) != 1 {
				t.Fatalf("rollback leaked history: %+v", items)
			}
			tx, err = store.db.BeginTx(t.Context(), nil)
			if err != nil {
				t.Fatal(err)
			}
			if err := writeProtocolUpgradeRevision(t.Context(), tx, entry); err != nil {
				tx.Rollback()
				t.Fatal(err)
			}
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		hashes = append(hashes, compiled.Hash())
	}
	items, err := store.ListProtocolHistory(t.Context())
	if err != nil || len(items) != 2 {
		t.Fatal(items, err)
	}
	for _, item := range items {
		if item.Reason != "preset_replaced" || item.Hash == hashes[2] {
			t.Fatal(item)
		}
	}
	// Simulate a database predating the archive index, then run migration twice.
	if _, err := store.db.Exec(`DELETE FROM protocol_history`); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := store.migrateProtocolRevisions(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	items, err = store.ListProtocolHistory(t.Context())
	if err != nil || len(items) != 2 {
		t.Fatal(items, err)
	}
	// 预置只读不变量：preset_replaced 历史行不可物理删除（含存储层兜底）。
	for _, item := range items {
		if err := store.DeleteProtocolHistory(t.Context(), item.ID); !errors.Is(err, protocol.ErrRevisionConflict) {
			t.Fatalf("preset history row deletable: %v", err)
		}
	}
}

func TestProtocolHistoryDurableJobBlocksPurge(t *testing.T) {
	store := newJobTestStore(t, filepath.Join(t.TempDir(), "job-ref.sqlite"))
	service := newRevisionService(t, store)
	_, revision := verifySavedDraft(t, service, revisionFixture(t, "1"), "")
	baseline, _ := store.ProtocolUpgradeBaseline(t.Context())
	if err := store.ArchiveProtocol(t.Context(), revision.ProtocolID, baseline, "", nil); err != nil {
		t.Fatal(err)
	}
	job := jobTestIdentity("retained-task")
	job.IngressID, job.IngressRevision = revision.ProtocolID, revision.Hash
	job.RequestHash, job.Revision, job.Phase, job.Task.Status = "request-hash", 1, protocol.JobSubmitting, protocol.TaskSubmitting
	job.CreatedAt = time.Now().UTC()
	job.LeaseUntil = job.CreatedAt.Add(time.Minute)
	if _, _, err := store.CreateGenerationJob(t.Context(), job); err != nil {
		t.Fatal(err)
	}
	refs, err := store.ProtocolRevisionReferences(t.Context(), revision.ProtocolID, revision.Hash)
	if err != nil || len(refs) != 1 || refs[0].Kind != "job" {
		t.Fatal(refs, err)
	}
	if err := store.DeleteProtocolHistory(t.Context(), revision.ProtocolID+"~"+revision.Hash); !errors.Is(err, protocol.ErrRevisionConflict) {
		t.Fatal(err)
	}
}

func TestProtocolHistoryEditorSessionBlocksPurge(t *testing.T) {
	store := newAgentTestStore(t)
	service := newRevisionService(t, store)
	_, revision := verifySavedDraft(t, service, revisionFixture(t, "1"), "")
	session, err := store.CreateAgentSession(t.Context(), AgentSessionUpsert{Title: "编辑中的协议", Mode: "edit", ProtocolID: revision.ProtocolID})
	if err != nil {
		t.Fatal(err)
	}
	baseline, _ := store.ProtocolUpgradeBaseline(t.Context())
	if err := store.ArchiveProtocol(t.Context(), revision.ProtocolID, baseline, "", nil); err != nil {
		t.Fatal(err)
	}
	refs, err := store.ProtocolRevisionReferences(t.Context(), revision.ProtocolID, revision.Hash)
	if err != nil || len(refs) != 1 || refs[0].Kind != "session" || refs[0].ID != session.ID {
		t.Fatal(refs, err)
	}
	archiveID := revision.ProtocolID + "~" + revision.Hash
	if err := store.DeleteProtocolHistory(t.Context(), archiveID); !errors.Is(err, protocol.ErrRevisionConflict) {
		t.Fatal(err)
	}
	if _, err := store.DeleteAgentSession(t.Context(), session.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteProtocolHistory(t.Context(), archiveID); err != nil {
		t.Fatal(err)
	}
}

func TestProtocolHistoryRejectsDeletionOfReferencedRevision(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "refs.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	service := newRevisionService(t, store)
	_, revision := verifySavedDraft(t, service, revisionFixture(t, "1"), "")
	baseline, _ := store.ProtocolUpgradeBaseline(t.Context())
	if err := store.ArchiveProtocol(t.Context(), revision.ProtocolID, baseline, "", nil); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveProtocolBinding(t.Context(), ProtocolBinding{Kind: "model", ModelID: "m", Binding: protocol.Binding{ProtocolID: revision.ProtocolID, RevisionHash: revision.Hash}}); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteProtocolHistory(t.Context(), revision.ProtocolID+"~"+revision.Hash); !errors.Is(err, protocol.ErrRevisionConflict) {
		t.Fatal(err)
	}
	if _, err := store.ReadProtocolRevision(t.Context(), revision.ProtocolID, revision.Hash); err != nil {
		t.Fatal("blocked purge partially deleted revision", err)
	}
}
