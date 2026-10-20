package storage

import (
	"context"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"
)

func TestListGroupsOrderingAndCompositeMembers(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "groups.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := t.Context()
	for _, group := range []ModelGroup{
		{ID: "last", Name: "Zulu", Enabled: true, Models: []string{"disabled:same", "other:same", "missing:other"}, Strategy: "priority", MaxRetries: 5, RetryInterval: 12, MaxConcurrency: 3, DailyLimitMaxRequests: 8, DailyLimitMaxTokens: 9007199254740993, Type: "llm", MaxTokens: 123, VisionCapable: true, ToolsCapable: true},
		{ID: "empty", Name: "Alpha", Strategy: "round-robin", Type: "llm"},
	} {
		if err := store.UpsertGroup(ctx, group); err != nil {
			t.Fatal(err)
		}
	}
	groups, err := store.ListGroups(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 2 || groups[0].ID != "empty" || groups[0].Models == nil || len(groups[0].Models) != 0 {
		t.Fatalf("empty group/order: %+v", groups)
	}
	want := ModelGroup{ID: "last", Name: "Zulu", Enabled: true, Models: []string{"disabled:same", "other:same", "missing:other"}, Strategy: "priority", MaxRetries: 5, RetryInterval: 12, MaxConcurrency: 3, DailyLimitMaxRequests: 8, DailyLimitMaxTokens: 9007199254740993, Type: "llm", MaxTokens: 123, VisionCapable: true, ToolsCapable: true}
	if !reflect.DeepEqual(groups[1], want) {
		t.Fatalf("got %+v want %+v", groups[1], want)
	}
	ctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := store.ListGroups(ctx); err == nil {
		t.Fatal("canceled query succeeded")
	}
}

func BenchmarkListGroups(b *testing.B) {
	for _, count := range []int{1, 100, 1000} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			store, err := Open(filepath.Join(b.TempDir(), "groups.sqlite"))
			if err != nil {
				b.Fatal(err)
			}
			defer store.Close()
			ctx := context.Background()
			tx, err := store.db.BeginTx(ctx, nil)
			if err != nil {
				b.Fatal(err)
			}
			defer tx.Rollback()
			for i := range count {
				id := fmt.Sprintf("g%04d", i)
				if _, err := tx.ExecContext(ctx, `INSERT INTO model_groups(id,name,created_at,updated_at) VALUES(?,?,?,?)`, id, id, "now", "now"); err != nil {
					b.Fatal(err)
				}
				for position := range 4 {
					if _, err := tx.ExecContext(ctx, `INSERT INTO model_group_models(group_id,model_id,source_id,position) VALUES(?,?,?,?)`, id, fmt.Sprint(position), "source", position); err != nil {
						b.Fatal(err)
					}
				}
			}
			if err := tx.Commit(); err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			for b.Loop() {
				groups, err := store.ListGroups(ctx)
				if err != nil || len(groups) != count {
					b.Fatalf("groups=%d err=%v", len(groups), err)
				}
			}
		})
	}
}
