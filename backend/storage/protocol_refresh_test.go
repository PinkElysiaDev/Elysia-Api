package storage

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

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
				if err := store.UpsertSource(t.Context(), ModelSource{ID: "concurrent", Name: "operator edit", BaseURL: "https://example.test", Platform: "custom:text-alpha"}); err != nil {
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

func protocolUpgradeFixture(t *testing.T, store *Store) ProtocolUpgrade {
	t.Helper()
	compiler, err := protocol.NewCompiler(protocol.DefaultLimits(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	compiled, issues := compiler.Compile(revisionFixture(t, "current"))
	if err := protocol.IssuesError(issues); err != nil {
		t.Fatal(err)
	}
	value, err := protocol.EncodeValue(compiled.Definition())
	if err != nil {
		t.Fatal(err)
	}
	report := protocol.Verify(t.Context(), compiled)
	if !report.Passed {
		t.Fatal(report.Issues)
	}
	baseline, err := store.ProtocolUpgradeBaseline(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	return ProtocolUpgrade{Baseline: baseline, Revisions: []ProtocolUpgradeRevision{{Revision: protocol.Revision{ProtocolID: "text-alpha", Hash: compiled.Hash(), Definition: value, CreatedAt: now}, Draft: protocol.Draft{ProtocolID: "text-alpha", Hash: compiled.Hash(), Definition: value, UpdatedAt: now}, Report: report}}, Bindings: []ProtocolBinding{{Kind: "source", SourceID: "source", Binding: protocol.Binding{ProtocolID: "text-alpha", RevisionHash: compiled.Hash(), Capabilities: protocol.CapabilitySet{protocol.TextCapability: true}, Transports: []protocol.Transport{protocol.HTTPJSON}}}}}
}
