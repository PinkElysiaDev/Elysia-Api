package storage

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/elysia-api/backend/internal/dbpath"
)

func TestLogDatabaseIsolationAndDurableBatch(t *testing.T) {
	dir := t.TempDir()
	path, logPath := filepath.Join(dir, "business.db"), filepath.Join(dir, "logs", "events.db")
	s, err := OpenWithLogPath(path, logPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	for _, tc := range []struct {
		db        *sql.DB
		forbidden string
	}{{s.db, "usage_records"}, {s.LogStore.db, "api_tokens"}} {
		var count int
		if err := tc.db.QueryRow("SELECT count(*) FROM sqlite_schema WHERE name=?", tc.forbidden).Scan(&count); err != nil || count != 0 {
			t.Fatalf("cross-database table %s: %d, %v", tc.forbidden, count, err)
		}
	}
	var synchronous int
	if err := s.LogStore.db.QueryRow("PRAGMA synchronous").Scan(&synchronous); err != nil || synchronous != 2 {
		t.Fatalf("FULL durability required: %d, %v", synchronous, err)
	}
	// Occupying the business database's only connection must not stall logs.
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	if err := s.LogStore.WriteSystemLogBatch(ctx, [][]byte{[]byte(`{"level":"warn","time":"` + stamp + `","message":"diagnostic","request_id":"r"}`)}); err != nil {
		t.Fatal(err)
	}
	if err := s.LogStore.SaveUsageBatch(ctx, []UsageRecordInput{{Payload: []byte(`{}`), Summary: UsageLogItem{RequestID: "one", TotalTokens: 7}}}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenLogStore(logPath)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if _, found, err := reopened.GetUsageRecordJSON(ctx, "one"); err != nil || !found {
		t.Fatalf("usage not durable: %t %v", found, err)
	}
	total, records, err := reopened.QuerySystemLogs(ctx, 10, 0, "")
	if err != nil || total != 1 || records[0].Message != "diagnostic" {
		t.Fatalf("system logs not durable: %d %v", total, err)
	}
	if reopened.UsageAssetsRoot() != logPath+"-assets" {
		t.Fatal("assets must follow log database")
	}
}

func TestLogDatabaseRejectsPathAliasesAndOldSchema(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "business.db")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(dir, "alias.db")
	if err := os.Link(path, alias); err != nil {
		t.Fatal(err)
	}
	for _, logs := range []string{path, alias} {
		if _, err := OpenWithLogPath(path, logs, nil); err == nil {
			t.Fatalf("accepted same file: %s", logs)
		}
	}
	parentAlias := filepath.Join(dir, "alias-dir")
	if err := os.Symlink(dir, parentAlias); err == nil {
		if err := dbpath.Validate(filepath.Join(dir, "new.db"), filepath.Join(parentAlias, "new.db")); err == nil {
			t.Fatal("accepted symlinked parent")
		}
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("CREATE TABLE settings(key TEXT); PRAGMA user_version=1"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if _, err := Open(path); err == nil {
		t.Fatal("old business schema must be rejected, never migrated")
	}
	if _, err := OpenLogStore(path); err == nil {
		t.Fatal("business database accepted as log database")
	}
}

func TestUsageBatchRollbackKeepsRollupsConsistent(t *testing.T) {
	s, err := OpenLogStore(filepath.Join(t.TempDir(), "log.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	err = s.SaveUsageBatch(t.Context(), []UsageRecordInput{
		{Payload: []byte(`{}`), Summary: UsageLogItem{RequestID: "good", TotalTokens: 10}},
		{Payload: []byte(`{}`), Summary: UsageLogItem{RequestID: "bad"}, Assets: []UsageAsset{{File: "../invalid"}}},
	})
	if err == nil {
		t.Fatal("invalid batch committed")
	}
	var rows, tokens int
	if err := s.db.QueryRow("SELECT count(*) FROM usage_records").Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow("SELECT coalesce(sum(total_tok),0) FROM usage_rollup_hour").Scan(&tokens); err != nil {
		t.Fatal(err)
	}
	if rows != 0 || tokens != 0 {
		t.Fatalf("partial batch: %d rows, %d tokens", rows, tokens)
	}
}

func TestLogReadersDoNotBlockWriterAndConfigureEveryConnection(t *testing.T) {
	s, err := OpenLogStore(filepath.Join(t.TempDir(), "events #1.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var readers []*sql.Conn
	defer func() {
		for _, reader := range readers {
			reader.Close()
		}
	}()
	for i := 0; i < 4; i++ {
		reader, err := s.readDB.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		readers = append(readers, reader)
		var queryOnly, timeout, foreignKeys, tempStore int
		if err := reader.QueryRowContext(ctx, "SELECT (SELECT query_only FROM pragma_query_only), (SELECT timeout FROM pragma_busy_timeout), (SELECT foreign_keys FROM pragma_foreign_keys), (SELECT temp_store FROM pragma_temp_store)").Scan(&queryOnly, &timeout, &foreignKeys, &tempStore); err != nil {
			t.Fatal(err)
		}
		if queryOnly != 1 || timeout != 5000 || foreignKeys != 1 || tempStore != 2 {
			t.Fatalf("reader configuration: %d %d %d %d", queryOnly, timeout, foreignKeys, tempStore)
		}
	}
	tx, err := readers[0].BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var before int
	if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM system_logs").Scan(&before); err != nil {
		t.Fatal(err)
	}
	if err := s.InsertSystemLog(ctx, "info", "concurrent writer", nil); err != nil {
		t.Fatal(err)
	}
	var snapshot int
	if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM system_logs").Scan(&snapshot); err != nil || snapshot != before {
		t.Fatalf("reader snapshot changed: %d, %v", snapshot, err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	// Force a replacement writer connection; FULL/foreign_keys/busy_timeout
	// must still hold after reconnect, including after interrupted SQL.
	s.db.SetMaxIdleConns(0)
	var full, foreignKeys, timeout int
	if err := s.db.QueryRowContext(ctx, "SELECT (SELECT synchronous FROM pragma_synchronous), (SELECT foreign_keys FROM pragma_foreign_keys), (SELECT timeout FROM pragma_busy_timeout)").Scan(&full, &foreignKeys, &timeout); err != nil {
		t.Fatal(err)
	}
	if full != 2 || foreignKeys != 1 || timeout != 5000 {
		t.Fatalf("writer reconnect configuration: %d %d %d", full, foreignKeys, timeout)
	}
}
