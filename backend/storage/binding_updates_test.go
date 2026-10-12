package storage

import (
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/elysia-api/backend/protocol"
)

func TestBindingUpdateTransaction(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "bindings.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	source := ModelSource{ID: "source", Name: "original"}
	parent := ProtocolBinding{Kind: "source", SourceID: source.ID, Unbound: true}
	child := ProtocolBinding{Kind: "model", SourceID: source.ID, ModelID: "child", Unbound: true}
	if err := store.SaveBoundSource(t.Context(), BoundSourceUpdate{Source: source, Bindings: []ProtocolBinding{parent, child}}); err != nil {
		t.Fatal(err)
	}
	_, before, generation, err := store.ConversionSnapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	assertUnchanged := func() {
		t.Helper()
		_, after, actual, err := store.ConversionSnapshot(t.Context())
		if err != nil || actual != generation || !reflect.DeepEqual(before, after) {
			t.Fatalf("partial evidence update: generation=%d err=%v", actual, err)
		}
		sources, err := store.ListSources(t.Context())
		if err != nil || len(sources) != 1 || sources[0].Name != "original" || sources[0].Platform != "" {
			t.Fatal("partial source update", err)
		}
	}
	parent.Unbound = false
	parent.Binding.ProtocolID = "new-protocol"
	if err := store.SaveManagedProtocolBindings(t.Context(), parent.Key(), []ProtocolBinding{parent, child}, generation-1); !errors.Is(err, protocol.ErrRevisionConflict) {
		t.Fatal("stale generation accepted", err)
	}
	assertUnchanged()
	// Force a failure after the parent has already been written in the transaction.
	if _, err := store.db.ExecContext(t.Context(), `CREATE TRIGGER fail_child BEFORE UPDATE ON protocol_bindings WHEN json_extract(new.binding, '$.kind')='model' BEGIN SELECT RAISE(ABORT, 'child write failed'); END`); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveManagedProtocolBindings(t.Context(), parent.Key(), []ProtocolBinding{parent, child}, generation); err == nil {
		t.Fatal("injected child failure was ignored")
	}
	assertUnchanged()
	source.Name = "rejected"
	if err := store.SaveBoundSource(t.Context(), BoundSourceUpdate{Source: source, Bindings: []ProtocolBinding{parent, child}}, generation); err == nil {
		t.Fatal("source commit ignored child failure")
	}
	assertUnchanged()
}
