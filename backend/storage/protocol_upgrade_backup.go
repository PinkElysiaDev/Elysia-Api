package storage

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/elysia-api/backend/protocol"
)

// Historical protocol_engine_v2_backup records are audit artifacts. Startup
// never opens them or interprets their unversioned fingerprints.
const protocolSnapshotSetting = "protocol_current_snapshot_v2"

type ProtocolSnapshot struct {
	FormatVersion    int       `json:"formatVersion"`
	Path             string    `json:"path"`
	SHA256           string    `json:"sha256"`
	Baseline         string    `json:"baseline"`
	EvidenceBaseline string    `json:"evidenceBaseline"`
	CreatedAt        time.Time `json:"createdAt"`
}

type ProtocolSnapshotError struct {
	Stage string
	Err   error
}

func (e *ProtocolSnapshotError) Error() string {
	return fmt.Sprintf("protocol snapshot %s: %v", e.Stage, e.Err)
}
func (e *ProtocolSnapshotError) Unwrap() error { return e.Err }

// EnsureProtocolSnapshot protects pending changes to existing data. It does not
// test whether any protocol stored in that data is valid or executable.
func (s *Store) EnsureProtocolSnapshot(ctx context.Context) (*ProtocolSnapshot, error) {
	baseline, err := s.ProtocolUpgradeBaseline(ctx)
	if err != nil {
		return nil, err
	}
	evidence, err := s.ProtocolRefreshEvidenceBaseline(ctx)
	if err != nil {
		return nil, err
	}
	return s.ensureProtocolSnapshot(ctx, baseline, evidence)
}

func (s *Store) ensureProtocolSnapshot(ctx context.Context, baseline, evidence string) (snapshot *ProtocolSnapshot, err error) {
	defer func() {
		if err != nil && !errors.Is(err, protocol.ErrRevisionConflict) {
			var typed *ProtocolSnapshotError
			if !errors.As(err, &typed) {
				err = &ProtocolSnapshotError{Stage: "create", Err: err}
			}
		}
	}()
	var raw string
	readErr := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key=?`, protocolSnapshotSetting).Scan(&raw)
	if readErr != nil && !errors.Is(readErr, sql.ErrNoRows) {
		return nil, readErr
	}
	var previous ProtocolSnapshot
	if readErr == nil && json.Unmarshal([]byte(raw), &previous) == nil && previous.FormatVersion == 2 && previous.Baseline == baseline && previous.EvidenceBaseline == evidence {
		if checkErr := checkProtocolSnapshot(ctx, &previous); checkErr == nil {
			return &previous, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		// Retain the unusable file; only a newly verified snapshot can protect writes.
	}
	file, err := os.CreateTemp(filepath.Dir(s.path), ".protocol-snapshot-*.sqlite")
	if err != nil {
		return nil, err
	}
	temporary := file.Name()
	if err = file.Close(); err != nil {
		return nil, err
	}
	defer os.Remove(temporary)
	if _, err = s.db.ExecContext(ctx, `VACUUM INTO ?`, temporary); err != nil {
		return nil, err
	}
	file, err = os.OpenFile(temporary, os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	syncErr := file.Sync()
	closeErr := file.Close()
	if syncErr != nil {
		return nil, syncErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	snapshot = &ProtocolSnapshot{FormatVersion: 2, Path: temporary, Baseline: baseline, EvidenceBaseline: evidence, CreatedAt: time.Now().UTC()}
	if snapshot.SHA256, err = protocolFileSHA256(temporary); err != nil {
		return nil, err
	}
	if err = checkProtocolSnapshot(ctx, snapshot); err != nil {
		return nil, err
	}
	db, err := openProtocolSnapshot(temporary)
	if err != nil {
		return nil, err
	}
	actual, e := readProtocolUpgradeBaseline(ctx, db)
	actualEvidence, ee := readProtocolRefreshEvidenceBaseline(ctx, db)
	db.Close()
	if e != nil {
		return nil, e
	}
	if ee != nil {
		return nil, ee
	}
	if actual != baseline || actualEvidence != evidence {
		return nil, protocol.ErrRevisionConflict
	}
	final := s.path + ".protocol-snapshot-v2-" + filepath.Base(temporary)
	if err = os.Rename(temporary, final); err != nil {
		return nil, err
	}
	snapshot.Path = final
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err = checkProtocolCommitBaseline(ctx, tx, ProtocolUpgrade{Baseline: baseline, EvidenceBaseline: evidence}); err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return nil, err
	}
	var result sql.Result
	if errors.Is(readErr, sql.ErrNoRows) {
		result, err = tx.ExecContext(ctx, `INSERT INTO settings(key,value,updated_at) VALUES(?,?,?) ON CONFLICT(key) DO NOTHING`, protocolSnapshotSetting, string(encoded), nowString())
	} else {
		result, err = tx.ExecContext(ctx, `UPDATE settings SET value=?,updated_at=? WHERE key=? AND value=?`, string(encoded), nowString(), protocolSnapshotSetting, raw)
	}
	if err != nil {
		return nil, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return nil, err
	}
	if count != 1 {
		return nil, protocol.ErrRevisionConflict
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return snapshot, nil
}

func openProtocolSnapshot(path string) (*sql.DB, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	normalized := filepath.ToSlash(absolute)
	if filepath.VolumeName(absolute) != "" && normalized[0] != '/' {
		normalized = "/" + normalized
	}
	uri := url.URL{Scheme: "file", Path: normalized, RawQuery: "mode=ro"}
	return sql.Open("sqlite", uri.String())
}

func protocolFileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	digest := sha256.New()
	if _, err = io.Copy(digest, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

func checkProtocolSnapshot(ctx context.Context, snapshot *ProtocolSnapshot) (err error) {
	defer func() {
		if err != nil {
			err = &ProtocolSnapshotError{Stage: "verify", Err: err}
		}
	}()
	if snapshot == nil || snapshot.FormatVersion != 2 || snapshot.Path == "" || len(snapshot.SHA256) != 64 {
		return fmt.Errorf("invalid snapshot metadata")
	}
	digest, err := protocolFileSHA256(snapshot.Path)
	if err != nil {
		return err
	}
	if digest != snapshot.SHA256 {
		return fmt.Errorf("snapshot checksum mismatch: %s", snapshot.Path)
	}
	db, err := openProtocolSnapshot(snapshot.Path)
	if err != nil {
		return err
	}
	defer db.Close()
	var integrity string
	if err = db.QueryRowContext(ctx, `PRAGMA quick_check`).Scan(&integrity); err != nil {
		return err
	}
	if integrity != "ok" {
		return fmt.Errorf("snapshot integrity: %s", integrity)
	}
	return nil
}
