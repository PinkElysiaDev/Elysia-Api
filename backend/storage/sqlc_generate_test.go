package storage

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elysia-api/backend/internal/storagequery"
)

// Execute the exact schema consumed by sqlc against the runtime database.
// A drift between generated query expectations and actual column types must
// fail at least this focused check before changing production data paths.
func TestSQLCGroupAndSettingsSchemaMatchesRuntime(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "sqlc.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	schema, err := os.ReadFile("sql/schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range strings.Split(string(schema), ";") {
		if strings.TrimSpace(statement) == "" {
			continue
		}
		if _, err := store.db.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	q := storagequery.New(store.db)
	if err := q.SetSetting(ctx, storagequery.SetSettingParams{Key: "sqlc", Value: "first", UpdatedAt: "now"}); err != nil {
		t.Fatal(err)
	}
	if err := q.SetSetting(ctx, storagequery.SetSettingParams{Key: "sqlc", Value: "updated", UpdatedAt: "later"}); err != nil {
		t.Fatal(err)
	}
	got, err := q.GetSetting(ctx, "sqlc")
	if err != nil || got != "updated" {
		t.Fatalf("setting: %q %v", got, err)
	}
	_, err = q.GetSetting(ctx, "missing")
	if err != sql.ErrNoRows {
		t.Fatalf("not found: %v", err)
	}
	if _, err := q.ListGroups(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := q.ListGroupMembers(ctx); err != nil {
		t.Fatal(err)
	}
}
