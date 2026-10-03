package storage

import (
	"path/filepath"
	"testing"

	"github.com/elysia-api/backend/protocol"
)

func TestLegacyModelImportPreservesPerModelCredentials(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "legacy.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	models := []Model{{ID: "a", APIKey: "key-a"}, {ID: "b", APIKey: "key-b"}}
	if err := store.ImportLegacyConfig(t.Context(), nil, nil, models); err != nil {
		t.Fatal(err)
	}
	stored, err := store.ListModels(t.Context())
	if err != nil || len(stored) != len(models) {
		t.Fatal(stored, err)
	}
	for _, model := range stored {
		if model.APIKey != "key-"+model.ID {
			t.Fatal("import lost a per-model credential")
		}
	}
	// Ordinary source replacement remains authoritative even with no key;
	// cached model credentials must not revive after the operator clears them.
	if err := store.ReplaceSourceModels(t.Context(), ModelSource{ID: "legacy-config"}, models); err != nil {
		t.Fatal(err)
	}
	stored, err = store.ListModels(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, model := range stored {
		if model.APIKey != "" {
			t.Fatal("source key removal restored a stale model credential")
		}
	}
}

func TestBoundSourceTransactionAndDeletion(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "source.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	source := ModelSource{ID: "s", Name: "Original", Platform: "custom:catalog", BaseURL: "https://example.invalid"}
	binding := ProtocolBinding{Kind: "source", SourceID: "s", Binding: protocol.Binding{ProtocolID: "catalog", RevisionHash: "hash"}}
	if err := store.SaveBoundSource(t.Context(), source, binding); err != nil {
		t.Fatal(err)
	}
	source.Name = "Rejected update"
	invalid := binding
	invalid.ModelID = "not-allowed-for-source"
	if err := store.SaveBoundSource(t.Context(), source, invalid); err == nil {
		t.Fatal("invalid binding accepted")
	}
	sources, err := store.ListSources(t.Context())
	if err != nil || len(sources) != 1 || sources[0].Name != "Original" {
		t.Fatal("transaction did not roll back source", sources, err)
	}
	model := binding
	model.Kind, model.ModelID = "model", "m"
	if err := store.SaveProtocolBinding(t.Context(), model); err != nil {
		t.Fatal(err)
	}
	group := binding
	group.Kind, group.SourceID, group.GroupID = "group", "", "g"
	if err := store.SaveProtocolBinding(t.Context(), group); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteSource(t.Context(), "s"); err != nil {
		t.Fatal(err)
	}
	bindings, err := store.ListProtocolBindings(t.Context())
	if err != nil || len(bindings) != 1 || bindings[0].GroupID != "g" {
		t.Fatal("source bindings orphaned or unrelated group removed", bindings, err)
	}
}
