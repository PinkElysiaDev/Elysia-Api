package storage

import (
	"errors"
	"reflect"
	"testing"
)

func TestSourceRefreshCommitRollsBackAllChanges(t *testing.T) {
	s := openMergeTestStore(t)
	ctx := t.Context()
	source := mergeTestSource()
	source.APIKeys = []SourceAPIKey{{Value: "key", Note: "kept", FetchedModels: []string{"old"}, AllowedModels: []string{"old"}}}
	seedSource(t, s, source)
	if err := s.UpdateSourceAPIKeys(ctx, source.ID, source.APIKeys); err != nil {
		t.Fatal(err)
	}
	if _, err := s.MergeSourceModels(ctx, source, []Model{{ID: "old"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertGroup(ctx, enabledGroup("g", "g", []string{source.ID + ":old"})); err != nil {
		t.Fatal(err)
	}
	beforeSources, _ := s.ListSources(ctx)
	beforeModels, _ := s.ListModels(ctx)
	beforeGroups, _ := s.ListGroups(ctx)
	// Fail after the key update, model insertion and model deletion have executed.
	if _, err := s.db.ExecContext(ctx, `CREATE TRIGGER fail_refresh BEFORE DELETE ON model_group_models BEGIN SELECT RAISE(ABORT, 'injected group deletion failure'); END`); err != nil {
		t.Fatal(err)
	}
	result, err := s.CommitSourceRefresh(ctx, beforeSources[0], []Model{{ID: "new"}}, map[int][]string{0: {"new"}})
	if err == nil || len(result.Added) != 0 || len(result.Removed) != 0 {
		t.Fatalf("failed commit reported changes: %+v %v", result, err)
	}
	afterSources, _ := s.ListSources(ctx)
	afterModels, _ := s.ListModels(ctx)
	afterGroups, _ := s.ListGroups(ctx)
	if !reflect.DeepEqual(beforeSources, afterSources) || !reflect.DeepEqual(beforeModels, afterModels) || !reflect.DeepEqual(beforeGroups, afterGroups) {
		t.Fatal("failed refresh changed source, model or group data")
	}
	if _, err := s.db.ExecContext(ctx, `DROP TRIGGER fail_refresh`); err != nil {
		t.Fatal(err)
	}
	result, err = s.CommitSourceRefresh(ctx, beforeSources[0], []Model{{ID: "new"}}, map[int][]string{0: {"new"}})
	if err != nil || len(result.Added) != 1 || len(result.Removed) != 1 {
		t.Fatalf("complete refresh did not merge: %+v %v", result, err)
	}
	afterSources, _ = s.ListSources(ctx)
	key := afterSources[0].APIKeys[0]
	if key.Value != "key" || key.Note != "kept" || key.AllowedModels != nil || !reflect.DeepEqual(key.FetchedModels, []string{"new"}) || !key.KeyAllowsModel("new") || key.KeyAllowsModel("old") {
		t.Fatalf("committed key does not match the discovered catalog: %+v", key)
	}
}

func TestSourceRefreshCommitRejectsStaleAndDeletedSources(t *testing.T) {
	s := openMergeTestStore(t)
	source := mergeTestSource()
	seedSource(t, s, source)
	sources, _ := s.ListSources(t.Context())
	snapshot := sources[0]
	source.APIKey = "replacement"
	seedSource(t, s, source)
	for _, deleted := range []bool{false, true} {
		if deleted {
			if err := s.DeleteSource(t.Context(), source.ID); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := s.CommitSourceRefresh(t.Context(), snapshot, []Model{{ID: "stale"}}, nil); !errors.Is(err, ErrSourceChanged) {
			t.Fatalf("stale/deleted commit: %v", err)
		}
		models, _ := s.ListModels(t.Context())
		if len(models) != 0 {
			t.Fatal("stale commit created models")
		}
	}
}

func TestSourcePermissionsPreserveEmptySetsAndManualMode(t *testing.T) {
	s := openMergeTestStore(t)
	source := mergeTestSource()
	source.APIKeys = []SourceAPIKey{
		{Value: "empty", FetchedModels: []string{}},
		{Value: "unknown"},
		{Value: "selected", FetchedModels: []string{"old"}, AllowedModels: []string{"new"}},
	}
	seedSource(t, s, source)
	sources, _ := s.ListSources(t.Context())
	keys := sources[0].APIKeys
	if keys[0].FetchedModels == nil || keys[0].KeyAllowsModel("any") || !keys[1].KeyAllowsModel("any") || keys[2].KeyAllowsModel("new") || !keys[2].KeyAllowsModel("old") || keys[2].AllowedModels != nil {
		t.Fatal("discovery permission semantics changed after storage")
	}
	source.AutoFetchModels = false
	seedSource(t, s, source)
	sources, _ = s.ListSources(t.Context())
	for _, key := range sources[0].APIKeys {
		if key.FetchedModels != nil {
			t.Fatal("manual source retained discovery metadata")
		}
	}
	if !sources[0].APIKeys[2].KeyAllowsModel("new") || sources[0].APIKeys[2].KeyAllowsModel("old") {
		t.Fatal("old discovery set restricted manual configuration")
	}
}

func TestEffectiveKeysUsesSourceMode(t *testing.T) {
	source := ModelSource{APIKeys: []SourceAPIKey{{Value: "key", FetchedModels: []string{"discovered"}, AllowedModels: []string{"manual"}}}}
	for _, automatic := range []bool{true, false} {
		source.AutoFetchModels = automatic
		key := source.EffectiveKeys()[0]
		if key.KeyAllowsModel("discovered") != automatic || key.KeyAllowsModel("manual") == automatic || key.KeyAllowsModel("other") {
			t.Fatalf("wrong models for automatic=%v: %+v", automatic, key)
		}
	}
	if source.APIKeys[0].FetchedModels == nil || source.APIKeys[0].AllowedModels == nil {
		t.Fatal("effective keys mutated source configuration")
	}
}
