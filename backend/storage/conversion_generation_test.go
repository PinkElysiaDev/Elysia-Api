package storage

import (
	"path/filepath"
	"testing"

	"github.com/elysia-api/backend/protocol"
)

// expectCachedGeneration asserts the published cache equals the committed
// conversion_generation row without reconciling a missed publication.
func expectCachedGeneration(t *testing.T, s *Store) int64 {
	t.Helper()
	dbGeneration, err := s.ConversionGeneration(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if cached := s.CachedConversionGeneration(); cached != dbGeneration {
		t.Fatalf("cached generation %d does not match committed generation %d", cached, dbGeneration)
	}
	return dbGeneration
}

// The cache follows committed trigger bumps across single-row writes,
// bulk merges, no-ops and rollbacks.
func TestCachedConversionGeneration(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "generation.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	ctx := t.Context()
	if cached := s.CachedConversionGeneration(); cached != 0 {
		t.Fatalf("new store cached generation %d, want 0", cached)
	}
	expectCachedGeneration(t, s)

	// Single-row writes on covered tables.
	source := ModelSource{ID: "src", Name: "src", BaseURL: "https://example.test", Platform: "openai"}
	if err := s.UpsertSource(ctx, source); err != nil {
		t.Fatal(err)
	}
	before := expectCachedGeneration(t, s)
	if before == 0 {
		t.Fatal("source insert did not advance generation")
	}

	// Single-row update on a covered table.
	if _, err := s.UpdateSourceEnabled(ctx, source.ID, false); err != nil {
		t.Fatal(err)
	}
	if next := expectCachedGeneration(t, s); next != before+1 {
		t.Fatalf("update generation %d, want %d", next, before+1)
	}

	// No-op update (no matching row) changes nothing.
	if _, err := s.UpdateSourceEnabled(ctx, "missing", true); err != nil {
		t.Fatal(err)
	}
	expectCachedGeneration(t, s)

	// Explicit transaction writing several covered rows commits one fold of
	// every per-row trigger bump.
	group := ModelGroup{ID: "g", Name: "g", Models: []string{"src:m1", "src:m2"}}
	if err := s.CreateGroup(ctx, group); err != nil {
		t.Fatal(err)
	}
	models := []Model{
		{ID: "m1", SourceID: source.ID, Name: "m1"},
		{ID: "m2", SourceID: source.ID, Name: "m2"},
	}
	if _, err := s.MergeSourceModels(ctx, ModelSource{ID: source.ID, Name: "src"}, models); err != nil {
		t.Fatal(err)
	}
	expectCachedGeneration(t, s)

	// A rolled-back transaction never publishes its pending bumps. The cache
	// must be checked without touching the database while the tx holds the
	// single connection.
	committed := s.CachedConversionGeneration()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE model_sources SET enabled=1 WHERE id=?`, source.ID); err != nil {
		t.Fatal(err)
	}
	if cached := s.CachedConversionGeneration(); cached != committed {
		t.Fatalf("uncommitted write changed cached generation to %d, want %d", cached, committed)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if cached := s.CachedConversionGeneration(); cached != committed {
		t.Fatalf("rollback left cached generation %d, want %d", cached, committed)
	}
	expectCachedGeneration(t, s)

	// Binding writes go through the same triggers.
	binding := ProtocolBinding{Kind: "source", SourceID: source.ID, Unbound: true}
	if err := s.SaveProtocolBinding(ctx, binding); err != nil {
		t.Fatal(err)
	}
	expectCachedGeneration(t, s)

	// Activation commits the binding rewrite and the
	// conversion_policy_activations upsert in one transaction.
	policy := protocol.ConversionPolicy{SchemaVersion: protocol.ConversionPolicySchemaVersion, ID: "policy", Name: "policy", Rules: []protocol.ConversionRule{}}
	draft, err := s.SaveConversionDraft(ctx, policy, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SaveConversionRevision(ctx, draft); err != nil {
		t.Fatal(err)
	}
	_, bindings, generation, err := s.ConversionSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ActivateConversion(ctx, policy.ID, draft.Hash, "", protocol.ConversionMatch{}, bindings, bindings, generation); err != nil {
		t.Fatal(err)
	}
	expectCachedGeneration(t, s)

	// Delete of a covered row bumps; deleting it again is a no-op.
	if _, err := s.DeleteModel(ctx, "m1", source.ID); err != nil {
		t.Fatal(err)
	}
	expectCachedGeneration(t, s)

	// DeleteSource deletes model_sources, models and protocol_bindings rows in
	// one transaction; every removed row bumps once.
	if err := s.DeleteSource(ctx, source.ID); err != nil {
		t.Fatal(err)
	}
	expectCachedGeneration(t, s)
}

func TestConversionGenerationMutationPaths(t *testing.T) {
	s := newAgentTestStore(t)
	ctx := t.Context()
	check := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		expectCachedGeneration(t, s)
	}
	source := ModelSource{ID: "source", Name: "source", Enabled: true, AutoFetchModels: true}
	models := []Model{{ID: "model", Name: "model"}}
	check(s.UpsertSource(ctx, source))
	check(s.ReplaceSourceModels(ctx, source, models))
	check(s.UpdateSourceAPIKeys(ctx, source.ID, nil))
	name := "renamed"
	_, err := s.UpdateModel(ctx, "model", source.ID, ModelPatch{Name: &name})
	check(err)
	_, err = s.SetModelAvailability(ctx, "model", source.ID, true)
	check(err)
	check(s.UpsertGroup(ctx, ModelGroup{ID: "group", Name: "group"}))
	_, err = s.DeleteGroup(ctx, "group")
	check(err)
	binding := ProtocolBinding{Kind: "source", SourceID: source.ID, Unbound: true}
	check(s.SaveBoundSource(ctx, BoundSourceUpdate{Source: source, Bindings: []ProtocolBinding{binding}}))
	check(s.SaveManagedProtocolBindings(ctx, binding.Key(), []ProtocolBinding{binding}))
	baseline, err := s.ProtocolUpgradeBaseline(ctx)
	check(err)
	check(s.EnableModelFunctionTools(ctx, source.ID, "model", baseline, ProtocolBinding{Kind: "model", SourceID: source.ID, ModelID: "model", Unbound: true}))
	check(s.UpsertSource(ctx, source))
	sources, err := s.ListSources(ctx)
	check(err)
	_, err = s.CommitSourceRefresh(ctx, sources[0], models, nil)
	check(err)
	_, err = s.SyncManualSourceModels(ctx, source, models)
	check(err)
	check(s.ExecRaw(ctx, `UPDATE models SET enabled=0`))
	plan := protocolUpgradeFixture(t, s)
	check(s.RefreshProtocolRuntime(ctx, plan))
	baseline, err = s.ProtocolUpgradeBaseline(ctx)
	check(err)
	check(s.ArchiveProtocol(ctx, "text-alpha", baseline, "", []ProtocolBinding{binding}))
}

func TestConversionGenerationDiscardedWrites(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "generation.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := t.Context()
	if err := s.UpsertSource(ctx, ModelSource{ID: "src", Name: "source"}); err != nil {
		t.Fatal(err)
	}
	before := expectCachedGeneration(t, s)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for _, query := range []string{
		`SAVEPOINT discarded`,
		`UPDATE model_sources SET name='discarded' WHERE id='src'`,
		`ROLLBACK TO discarded`,
		`RELEASE discarded`,
	} {
		if _, err := tx.ExecContext(ctx, query); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.commitConversion(ctx, tx); err != nil {
		t.Fatal(err)
	}
	if got := expectCachedGeneration(t, s); got != before {
		t.Fatal("savepoint rollback advanced generation", got, before)
	}
	// Deferred constraints fail only at COMMIT, after the trigger has run.
	for _, query := range []string{
		`PRAGMA foreign_keys=ON`,
		`CREATE TABLE generation_parent(id INTEGER PRIMARY KEY)`,
		`CREATE TABLE generation_child(id INTEGER REFERENCES generation_parent(id) DEFERRABLE INITIALLY DEFERRED)`,
	} {
		if _, err := s.db.ExecContext(ctx, query); err != nil {
			t.Fatal(err)
		}
	}
	tx, err = s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		`UPDATE model_sources SET name='uncommitted' WHERE id='src'`,
		`INSERT INTO generation_child VALUES(1)`,
	} {
		if _, err := tx.ExecContext(ctx, query); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.commitConversion(ctx, tx); err == nil {
		t.Fatal("deferred constraint unexpectedly committed")
	}
	if got := expectCachedGeneration(t, s); got != before {
		t.Fatal("failed commit published uncommitted generation", got, before)
	}
}

func TestCachedConversionGenerationReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "generation.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	if err := s.CreateGroup(ctx, ModelGroup{ID: "g", Name: "g"}); err != nil {
		t.Fatal(err)
	}
	committed := expectCachedGeneration(t, s)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if cached := reopened.CachedConversionGeneration(); cached != committed {
		t.Fatalf("reopened cache %d, want committed %d", cached, committed)
	}
}

// TestConversionSnapshotPublishesGeneration keeps the cache compatible with a
// snapshot pinned by a concurrent reader: after ConversionSnapshot returns,
// the cache holds at least that snapshot's generation.
func TestConversionSnapshotPublishesGeneration(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "generation.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_, _, generation, err := s.ConversionSnapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if cached := s.CachedConversionGeneration(); cached < generation {
		t.Fatalf("cache %d lags snapshot generation %d", cached, generation)
	}
}
