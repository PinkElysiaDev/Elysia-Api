package storage

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/elysia-api/backend/protocol"
)

func TestCurrentSnapshotPreservesDamagedProtocolWithoutVerification(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	plan := protocolUpgradeFixture(t, s)
	if err := s.RefreshProtocolRuntime(t.Context(), plan); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE protocol_drafts SET definition='{damaged original'`); err != nil {
		t.Fatal(err)
	}
	replacement := protocolUpgradeFixture(t, s)
	if err := s.RefreshProtocolRuntime(t.Context(), replacement); err != nil {
		t.Fatal(err)
	}
	var snapshot ProtocolSnapshot
	if ok, err := s.GetSetting(t.Context(), protocolSnapshotSetting, &snapshot); err != nil || !ok {
		t.Fatal("missing snapshot", err)
	}
	if err := checkProtocolSnapshot(t.Context(), &snapshot); err != nil {
		t.Fatal(err)
	}
	db, err := openProtocolSnapshot(snapshot.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var original string
	if err := db.QueryRow(`SELECT definition FROM protocol_drafts`).Scan(&original); err != nil || original != "{damaged original" {
		t.Fatal("backup did not preserve damaged data", original, err)
	}
	if _, err := db.Exec(`DELETE FROM protocol_drafts`); err == nil {
		t.Fatal("snapshot opened writable")
	}
	again, err := s.ensureProtocolSnapshot(t.Context(), snapshot.Baseline, snapshot.EvidenceBaseline)
	if err != nil || again.Path != snapshot.Path {
		t.Fatal("valid snapshot was not reused", err)
	}
}

func TestSnapshotMetadataFailureStopsOverwrite(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	plan := protocolUpgradeFixture(t, s)
	if err := s.RefreshProtocolRuntime(t.Context(), plan); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE protocol_drafts SET definition='{damaged original'`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`DELETE FROM settings WHERE key='protocol_runtime_refresh'`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`CREATE TRIGGER fail_snapshot BEFORE INSERT ON settings WHEN NEW.key='protocol_current_snapshot_v2' BEGIN SELECT RAISE(ABORT,'database or disk is full'); END`); err != nil {
		t.Fatal(err)
	}
	plan = protocolUpgradeFixture(t, s)
	err = s.RefreshProtocolRuntime(t.Context(), plan)
	var snapshotError *ProtocolSnapshotError
	if !errors.As(err, &snapshotError) || errors.Is(err, protocol.ErrRevisionConflict) {
		t.Fatal("wrong error category", err)
	}
	var raw string
	if err := s.db.QueryRow(`SELECT definition FROM protocol_drafts`).Scan(&raw); err != nil || raw != "{damaged original" {
		t.Fatal("overwrite escaped failure", raw, err)
	}
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM settings WHERE key IN ('protocol_current_snapshot_v2','protocol_runtime_refresh')`).Scan(&count); err != nil || count != 0 {
		t.Fatal("failed write marked complete", count, err)
	}
}

func TestInsertOnlyCommitRejectsConcurrentCreation(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	plan := protocolUpgradeFixture(t, s)
	prepared, snapshot, err := s.prepareProtocolCommit(t.Context(), plan)
	if err != nil || snapshot != nil || prepared.overwrites {
		t.Fatal("pure insertion needs no snapshot", err)
	}
	if _, err := s.db.Exec(`INSERT INTO protocol_drafts VALUES('text-alpha','other','{}','now')`); err != nil {
		t.Fatal(err)
	}
	tx, err := s.beginProtocolCommit(t.Context(), prepared)
	if tx != nil {
		tx.Rollback()
	}
	if !errors.Is(err, protocol.ErrRevisionConflict) {
		t.Fatal("concurrent creation was not detected", err)
	}
	var hash string
	if err := s.db.QueryRow(`SELECT content_hash FROM protocol_drafts`).Scan(&hash); err != nil || hash != "other" {
		t.Fatal("concurrent draft changed", err)
	}
}

func TestSnapshotIntegrityAndMissingFile(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	snapshot, err := s.EnsureProtocolSnapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(snapshot.Path, os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("tampered"); err != nil {
		t.Fatal(err)
	}
	file.Close()
	if err := checkProtocolSnapshot(t.Context(), snapshot); err == nil || errors.Is(err, protocol.ErrRevisionConflict) {
		t.Fatal("tampering not classified", err)
	}
	replacement, err := s.EnsureProtocolSnapshot(t.Context())
	if err != nil || replacement.Path == snapshot.Path {
		t.Fatal("did not replace unusable snapshot", err)
	}
	missing := *snapshot
	missing.Path = filepath.Join(t.TempDir(), "missing.sqlite")
	if err := checkProtocolSnapshot(t.Context(), &missing); err == nil {
		t.Fatal("missing snapshot accepted")
	}
	if _, err := os.Stat(missing.Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("verification created a missing backup", err)
	}
}
