package storage

import (
	"database/sql"
	"fmt"
	"os"
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

	for _, version := range []int{0, 1, 2} {
		t.Run(fmt.Sprintf("unsupported-%d", version), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "unsupported.sqlite")
			db, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if _, err := db.Exec(fmt.Sprintf(`CREATE TABLE untouched(value TEXT); INSERT INTO untouched VALUES('original'); PRAGMA user_version=%d`, version)); err != nil {
				t.Fatal(err)
			}
			logPath := filepath.Join(t.TempDir(), "untouched-log.sqlite")
			if opened, err := OpenWithLogPath(path, logPath, nil); err == nil {
				opened.Close()
				t.Fatal("unsupported database was silently adopted")
			}
			var count, actualVersion int
			if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_schema WHERE type='table'`).Scan(&count); err != nil || count != 1 {
				t.Fatal("unsupported database schema was modified", count, err)
			}
			if err := db.QueryRow(`PRAGMA user_version`).Scan(&actualVersion); err != nil || actualVersion != version {
				t.Fatal("unsupported database version was modified", actualVersion, err)
			}
			var value string
			if err := db.QueryRow(`SELECT value FROM untouched`).Scan(&value); err != nil || value != "original" {
				t.Fatal("unsupported database contents were modified", value, err)
			}
			if _, err := os.Stat(logPath); !os.IsNotExist(err) {
				t.Fatal("rejected database opened a log database", err)
			}
		})
	}
}
