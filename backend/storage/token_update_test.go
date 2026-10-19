package storage

import (
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
)

func TestTokenReadsRejectCorruptAuthorization(t *testing.T) {
	for _, field := range []string{"allowed_groups_json", "scopes"} {
		t.Run(field, func(t *testing.T) {
			store, err := Open(filepath.Join(t.TempDir(), "tokens.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			if err := store.UpsertGroup(t.Context(), ModelGroup{ID: "group", Name: "private", Enabled: true}); err != nil {
				t.Fatal(err)
			}
			if _, err := store.SaveAPIToken(t.Context(), "restricted", APITokenUpdate{Token: "test-secret", AllowedGroups: []string{"private"}}, true); err != nil {
				t.Fatal(err)
			}
			if _, err := store.db.ExecContext(t.Context(), fmt.Sprintf("UPDATE api_tokens SET %s=? WHERE name=?", field), "broken-json", "restricted"); err != nil {
				t.Fatal(err)
			}
			if _, _, err := store.FindAPITokenByName(t.Context(), "restricted"); err == nil {
				t.Fatal("corrupt authorization was read as unrestricted")
			}
			if _, err := store.ListAPITokens(t.Context()); err == nil {
				t.Fatal("route cache could consume a corrupt authorization")
			}
			if _, err := store.SaveAPIToken(t.Context(), "restricted", APITokenUpdate{NewName: "renamed"}, false); err == nil {
				t.Fatal("rename silently repaired and changed authorization")
			}
			if field == "allowed_groups_json" {
				if err := store.UpsertGroup(t.Context(), ModelGroup{ID: "group", Name: "renamed-group", Enabled: true}); err == nil {
					t.Fatal("group rename ignored corrupt authorization")
				}
				var groupName string
				if err := store.db.QueryRowContext(t.Context(), "SELECT name FROM model_groups WHERE id='group'").Scan(&groupName); err != nil || groupName != "private" {
					t.Fatalf("failed group rename partially committed: %q, %v", groupName, err)
				}
			}
			var stored string
			if err := store.db.QueryRowContext(t.Context(), fmt.Sprintf("SELECT %s FROM api_tokens WHERE name=?", field), "restricted").Scan(&stored); err != nil || stored != "broken-json" {
				t.Fatalf("failed mutation changed stored authorization: %v", err)
			}
		})
	}
}

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
