package storage

import (
	"context"
	"path/filepath"
	"testing"
)

// After schema preparation, a missing live table is a storage failure. Old
// snapshots are never read by this live concurrency check.
func TestProtocolUpgradeBaselineRejectsMissingLiveTable(t *testing.T) {
	ctx := context.Background()
	store, err := Open(filepath.Join(t.TempDir(), "elysia.sqlite3"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer store.Close()
	if _, err := store.ProtocolUpgradeBaseline(ctx); err != nil {
		t.Fatalf("baseline before drop: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `DROP TABLE protocol_history`); err != nil {
		t.Fatalf("drop: %v", err)
	}
	if _, err := store.ProtocolUpgradeBaseline(ctx); err == nil {
		t.Fatal("missing live table accepted")
	}
}
