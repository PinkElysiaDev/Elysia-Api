package storage

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestSchemaVersionAndUnsupportedDatabaseIsolation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "current.sqlite")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	var version int
	if err := store.db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil || version != currentSchemaVersion {
		t.Fatal("current schema version was not recorded", version, err)
	}
	store.Close()
	store, err = Open(path)
	if err != nil {
		t.Fatal("current database cannot reopen", err)
	}
	store.Close()

	path = filepath.Join(t.TempDir(), "unsupported.sqlite")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE untouched(value TEXT); INSERT INTO untouched VALUES('original')`); err != nil {
		t.Fatal(err)
	}
	if opened, err := Open(path); err == nil {
		opened.Close()
		t.Fatal("unversioned database was silently adopted")
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_schema WHERE type='table'`).Scan(&count); err != nil || count != 1 {
		t.Fatal("unsupported database schema was modified", count, err)
	}
	var value string
	if err := db.QueryRow(`SELECT value FROM untouched`).Scan(&value); err != nil || value != "original" {
		t.Fatal("unsupported database contents were modified", value, err)
	}
}
