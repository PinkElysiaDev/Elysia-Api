package storage

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/elysia-api/backend/protocol"
)

func TestRuntimeRefreshRejectsStaleGraphAndRollsBackWrites(t *testing.T) {
	for _, failure := range []string{"stale", "transaction"} {
		t.Run(failure, func(t *testing.T) {
			store, err := Open(filepath.Join(t.TempDir(), "refresh.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			plan := protocolUpgradeFixture(t, store)
			if failure == "stale" {
				if err := store.UpsertCustomProtocol(t.Context(), CustomProtocol{ID: "concurrent", Name: "operator edit", Config: `{"edited":true}`}); err != nil {
					t.Fatal(err)
				}
			} else {
				if _, err := store.db.Exec(`CREATE TRIGGER reject_refresh BEFORE INSERT ON protocol_bindings BEGIN SELECT RAISE(ABORT,'injected refresh failure'); END`); err != nil {
					t.Fatal(err)
				}
			}
			err = store.RefreshProtocolRuntime(t.Context(), plan)
			if err == nil || (failure == "stale" && !errors.Is(err, protocol.ErrRevisionConflict)) {
				t.Fatal("invalid refresh accepted", err)
			}
			for _, table := range []string{"protocol_drafts", "protocol_revisions", "protocol_verification_reports", "protocol_activations", "protocol_bindings"} {
				var count int
				if err := store.db.QueryRow("SELECT count(*) FROM " + table).Scan(&count); err != nil || count != 0 {
					t.Fatalf("partial refresh in %s: %d %v", table, count, err)
				}
			}
			var receipts int
			if err := store.db.QueryRow(`SELECT count(*) FROM settings WHERE key='protocol_runtime_refresh'`).Scan(&receipts); err != nil || receipts != 0 {
				t.Fatal("failed refresh left a success receipt", err)
			}
		})
	}
}
