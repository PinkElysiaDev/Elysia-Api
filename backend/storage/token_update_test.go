package storage

import (
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
)

func TestTokenUpdateRollsBackConflicts(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "tokens.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for _, name := range []string{"first", "second"} {
		if _, err := store.SaveAPIToken(t.Context(), name, APITokenUpdate{Token: name}, true); err != nil {
			t.Fatal(err)
		}
	}
	isEnabled := false
	for _, update := range []APITokenUpdate{{NewName: "second", Enabled: &isEnabled}, {NewName: "renamed", Token: "second", Enabled: &isEnabled}} {
		if _, err := store.SaveAPIToken(t.Context(), "first", update, false); err == nil {
			t.Fatal("expected conflict")
		}
		first, found, err := store.FindAPITokenByName(t.Context(), "first")
		if err != nil || !found || !first.Enabled || first.Token != "first" {
			t.Fatalf("partial mutation: %+v %v", first, err)
		}
		if _, found, err := store.FindAPITokenByName(t.Context(), "renamed"); err != nil || found {
			t.Fatal("rename committed")
		}
	}
}

func TestTokenConcurrentCreateIsInsertOnly(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "tokens.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	var workers sync.WaitGroup
	var successes atomic.Int32
	for range 8 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			_, err := store.SaveAPIToken(t.Context(), "same", APITokenUpdate{Token: "secret"}, true)
			if err == nil {
				successes.Add(1)
			} else if !errors.Is(err, ErrTokenExists) {
				t.Errorf("unexpected error: %v", err)
			}
		}()
	}
	workers.Wait()
	if successes.Load() != 1 {
		t.Fatalf("created %d times", successes.Load())
	}
}
