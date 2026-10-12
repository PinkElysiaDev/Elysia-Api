package storage

import (
	"errors"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
)

func TestCreateGroupNeverOverwritesAnExistingGroup(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "groups.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	original := ModelGroup{ID: "original", Name: "Group", Enabled: true, Strategy: "random", MaxConcurrency: 9, Models: []string{"s:m"}}
	if err := store.CreateGroup(t.Context(), original); err != nil {
		t.Fatal(err)
	}
	before, err := store.ListGroups(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, duplicate := range []ModelGroup{{ID: "original", Name: "Renamed"}, {ID: "other", Name: "Group"}} {
		if err := store.CreateGroup(t.Context(), duplicate); !errors.Is(err, ErrGroupExists) {
			t.Fatal("expected explicit conflict", err)
		}
	}
	after, err := store.ListGroups(t.Context())
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("duplicate creation changed stored settings or members", err)
	}
	var workers sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		workers.Go(func() { results <- store.CreateGroup(t.Context(), ModelGroup{ID: "concurrent", Name: "Concurrent"}) })
	}
	workers.Wait()
	close(results)
	successes, conflicts := 0, 0
	for err := range results {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrGroupExists):
			conflicts++
		default:
			t.Fatal(err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("creation race: %d successes, %d conflicts", successes, conflicts)
	}
}
