package storage

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/elysia-api/backend/protocol"
)

func protocolUpgradeFixture(t *testing.T, store *Store) ProtocolUpgrade {
	t.Helper()
	compiler, err := protocol.NewCompiler(protocol.DefaultLimits(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	compiled, issues := compiler.Compile(revisionFixture(t, "migrated"))
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

func TestProtocolUpgradeAtomicBackupAndRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway.sqlite")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	legacy := CustomProtocol{ID: "text-alpha", Name: "Edited legacy protocol", Type: "llm", Config: `{"id":"text-alpha","userSetting":9007199254740993}`}
	if err := store.UpsertCustomProtocol(t.Context(), legacy); err != nil {
		t.Fatal(err)
	}
	plan := protocolUpgradeFixture(t, store)
	receipt, err := store.ApplyProtocolUpgrade(t.Context(), plan)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(receipt.Backup); err != nil {
		t.Fatal(err)
	}
	if err := checkProtocolUpgradeBackup(t.Context(), receipt.Backup, plan.Baseline); err != nil {
		t.Fatal(err)
	}
	second, err := store.ApplyProtocolUpgrade(t.Context(), plan)
	if err != nil || second.PlanHash != receipt.PlanHash {
		t.Fatalf("idempotent replay: %+v %v", second, err)
	}
	rows, err := store.ListCustomProtocols(t.Context())
	if err != nil || len(rows) != 1 || rows[0].Config != legacy.Config {
		t.Fatal("legacy source was overwritten", rows, err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	service := newRevisionService(t, store)
	if err := service.Reload(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, exists := service.Pin("text-alpha"); !exists {
		t.Fatal("upgraded activation did not survive restart")
	}
	bindings, err := store.ListProtocolBindings(t.Context())
	if err != nil || len(bindings) != 1 || bindings[0].Binding.RevisionHash != plan.Revisions[0].Revision.Hash {
		t.Fatal(bindings, err)
	}
}

func TestProtocolUpgradeFailureNeverPartiallyActivates(t *testing.T) {
	for _, failure := range []string{"stale", "unverified", "transaction"} {
		t.Run(failure, func(t *testing.T) {
			store, err := Open(filepath.Join(t.TempDir(), "gateway.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			plan := protocolUpgradeFixture(t, store)
			switch failure {
			case "stale":
				if err := store.UpsertCustomProtocol(t.Context(), CustomProtocol{ID: "concurrent", Config: `{"id":"concurrent"}`}); err != nil {
					t.Fatal(err)
				}
			case "unverified":
				plan.Revisions[0].Report.Passed = false
			case "transaction":
				if _, err := store.db.Exec(`CREATE TRIGGER reject_upgrade_binding BEFORE INSERT ON protocol_bindings BEGIN SELECT RAISE(ABORT,'injected failure after revision writes'); END`); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := store.ApplyProtocolUpgrade(t.Context(), plan); err == nil || (failure == "stale" && !errors.Is(err, protocol.ErrRevisionConflict)) {
				t.Fatalf("accepted %s: %v", failure, err)
			}
			for _, table := range []string{"protocol_drafts", "protocol_revisions", "protocol_verification_reports", "protocol_activations", "protocol_bindings"} {
				var count int
				if err := store.db.QueryRow("SELECT count(*) FROM " + table).Scan(&count); err != nil || count != 0 {
					t.Fatalf("partial migration in %s: count=%d err=%v", table, count, err)
				}
			}
			if receipt, err := store.ProtocolUpgradeStatus(t.Context()); err != nil || receipt != nil {
				t.Fatal("failed transaction marked complete", receipt, err)
			}
		})
	}
}
