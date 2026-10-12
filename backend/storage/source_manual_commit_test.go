package storage

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestSourceManualCatalogCommitsAtomically(t *testing.T) {
	path := filepath.Join(t.TempDir(), "manual.db")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { store.Close() }()
	update := BoundSourceUpdate{
		Source:   ModelSource{ID: "source", Name: "original", Enabled: true, ManualModels: []Model{{ID: "one", Name: "one", Origin: "manual", Enabled: true}}},
		Bindings: []ProtocolBinding{{Kind: "source", SourceID: "source", Unbound: true}},
		IsCreate: true, SyncManualModels: true,
	}
	if err := store.SaveBoundSource(t.Context(), update); err != nil {
		t.Fatal(err)
	}
	update.Source.Name = "duplicate"
	if err := store.SaveBoundSource(t.Context(), update); !errors.Is(err, ErrSourceExists) {
		t.Fatal("duplicate source was not rejected", err)
	}
	update.IsCreate = false
	update.Source.ManualModels = []Model{{ID: "two", Origin: "manual"}}
	if _, err := store.db.ExecContext(t.Context(), `CREATE TRIGGER fail_model BEFORE INSERT ON models BEGIN SELECT RAISE(ABORT, 'model write failed'); END`); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveBoundSource(t.Context(), update); err == nil {
		t.Fatal("model failure did not abort source commit")
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	sources, err := store.ListSources(t.Context())
	if err != nil || len(sources) != 1 || sources[0].Name != "original" || len(sources[0].ManualModels) != 1 || sources[0].ManualModels[0].ID != "one" {
		t.Fatal("failed transaction changed authoritative source catalog", sources, err)
	}
	models, err := store.ListModels(t.Context())
	if err != nil || len(models) != 1 || models[0].ID != "one" {
		t.Fatal("failed transaction changed models", models, err)
	}
}
