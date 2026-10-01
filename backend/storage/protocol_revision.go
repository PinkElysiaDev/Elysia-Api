package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/elysia-api/backend/protocol"
)

var _ protocol.Repository = (*Store)(nil)

func (s *Store) migrateProtocolRevisions(ctx context.Context) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS protocol_drafts (
			protocol_id TEXT PRIMARY KEY, content_hash TEXT NOT NULL,
			definition TEXT NOT NULL, updated_at TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS protocol_revisions (
			protocol_id TEXT NOT NULL, content_hash TEXT NOT NULL, definition TEXT NOT NULL,
			created_at TEXT NOT NULL, PRIMARY KEY(protocol_id, content_hash))`,
		`CREATE TABLE IF NOT EXISTS protocol_verification_reports (
			id INTEGER PRIMARY KEY AUTOINCREMENT, protocol_id TEXT NOT NULL, revision_hash TEXT NOT NULL,
			compiler_version TEXT NOT NULL, samples_hash TEXT NOT NULL, kind TEXT NOT NULL,
			report TEXT NOT NULL, verified_at TEXT NOT NULL,
			FOREIGN KEY(protocol_id, revision_hash) REFERENCES protocol_revisions(protocol_id, content_hash))`,
		`CREATE INDEX IF NOT EXISTS idx_protocol_reports_revision
			ON protocol_verification_reports(protocol_id, revision_hash, compiler_version, kind, id)`,
		`CREATE TABLE IF NOT EXISTS protocol_activations (
			protocol_id TEXT PRIMARY KEY, revision_hash TEXT NOT NULL, generation INTEGER NOT NULL,
			activated_at TEXT NOT NULL,
			FOREIGN KEY(protocol_id, revision_hash) REFERENCES protocol_revisions(protocol_id, content_hash))`,
	}
	for _, statement := range statements {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}

// SaveProtocolDraft performs an optimistic edit independently of activation.
func (s *Store) SaveProtocolDraft(ctx context.Context, draft protocol.Draft, expected string) (protocol.Draft, error) {
	var result sql.Result
	var err error
	if expected == "" {
		result, err = s.db.ExecContext(ctx, `INSERT INTO protocol_drafts(protocol_id, content_hash, definition, updated_at)
			VALUES(?,?,?,?) ON CONFLICT(protocol_id) DO NOTHING`, draft.ProtocolID, draft.Hash, string(draft.Definition.Bytes()), draft.UpdatedAt.UTC().Format(time.RFC3339Nano))
	} else {
		result, err = s.db.ExecContext(ctx, `UPDATE protocol_drafts SET content_hash=?, definition=?, updated_at=?
			WHERE protocol_id=? AND content_hash=?`, draft.Hash, string(draft.Definition.Bytes()), draft.UpdatedAt.UTC().Format(time.RFC3339Nano), draft.ProtocolID, expected)
	}
	if err != nil {
		return protocol.Draft{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return protocol.Draft{}, err
	}
	if affected != 1 {
		return protocol.Draft{}, protocol.ErrRevisionConflict
	}
	return draft, nil
}

func scanProtocolDraft(row interface{ Scan(...any) error }) (protocol.Draft, error) {
	var draft protocol.Draft
	var definition, updated string
	if err := row.Scan(&draft.ProtocolID, &draft.Hash, &definition, &updated); err != nil {
		return draft, protocolRowError(err)
	}
	value, err := protocol.ParseValue([]byte(definition))
	if err != nil {
		return draft, err
	}
	draft.Definition, draft.UpdatedAt = value, parseTime(updated)
	return draft, nil
}

// ReadProtocolDraft reads the editable revision without falling back to active.
func (s *Store) ReadProtocolDraft(ctx context.Context, id string) (protocol.Draft, error) {
	return scanProtocolDraft(s.db.QueryRowContext(ctx, `SELECT protocol_id, content_hash, definition, updated_at FROM protocol_drafts WHERE protocol_id=?`, id))
}

// ListProtocolDrafts returns deterministic protocol IDs for management clients.
func (s *Store) ListProtocolDrafts(ctx context.Context) ([]protocol.Draft, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT protocol_id, content_hash, definition, updated_at FROM protocol_drafts ORDER BY protocol_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	drafts := []protocol.Draft{}
	for rows.Next() {
		draft, err := scanProtocolDraft(rows)
		if err != nil {
			return nil, err
		}
		drafts = append(drafts, draft)
	}
	return drafts, rows.Err()
}

// SaveProtocolRevision inserts immutable content; an existing hash is never edited.
func (s *Store) SaveProtocolRevision(ctx context.Context, revision protocol.Revision) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO protocol_revisions(protocol_id, content_hash, definition, created_at)
		VALUES(?,?,?,?) ON CONFLICT(protocol_id, content_hash) DO NOTHING`, revision.ProtocolID, revision.Hash, string(revision.Definition.Bytes()), revision.CreatedAt.UTC().Format(time.RFC3339Nano))
	return err
}

func scanProtocolRevision(row interface{ Scan(...any) error }) (protocol.Revision, error) {
	var revision protocol.Revision
	var definition, created string
	if err := row.Scan(&revision.ProtocolID, &revision.Hash, &definition, &created); err != nil {
		return revision, protocolRowError(err)
	}
	value, err := protocol.ParseValue([]byte(definition))
	if err != nil {
		return revision, err
	}
	revision.Definition, revision.CreatedAt = value, parseTime(created)
	return revision, nil
}

// ReadProtocolRevision retrieves one content-addressed rollback candidate.
func (s *Store) ReadProtocolRevision(ctx context.Context, id, hash string) (protocol.Revision, error) {
	return scanProtocolRevision(s.db.QueryRowContext(ctx, `SELECT protocol_id, content_hash, definition, created_at FROM protocol_revisions WHERE protocol_id=? AND content_hash=?`, id, hash))
}

// ListProtocolRevisions lists immutable history, newest first.
func (s *Store) ListProtocolRevisions(ctx context.Context, id string) ([]protocol.Revision, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT protocol_id, content_hash, definition, created_at FROM protocol_revisions WHERE protocol_id=? ORDER BY created_at DESC, content_hash`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	revisions := []protocol.Revision{}
	for rows.Next() {
		revision, err := scanProtocolRevision(rows)
		if err != nil {
			return nil, err
		}
		revisions = append(revisions, revision)
	}
	return revisions, rows.Err()
}

// SaveProtocolReport appends audit evidence without rewriting older reports.
func (s *Store) SaveProtocolReport(ctx context.Context, id, hash string, report protocol.VerificationReport) error {
	if hash != report.DefinitionHash {
		return errors.New("verification report does not match revision hash")
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO protocol_verification_reports(protocol_id, revision_hash, compiler_version, samples_hash, kind, report, verified_at)
		VALUES(?,?,?,?,?,?,?)`, id, hash, report.CompilerVersion, report.SamplesHash, report.Kind, string(encoded), report.VerifiedAt.UTC().Format(time.RFC3339Nano))
	return err
}

// ReadProtocolReport returns the newest offline evidence for this engine.
// Online evidence is stored separately by kind and cannot authorize activation.
func (s *Store) ReadProtocolReport(ctx context.Context, id, hash string) (protocol.VerificationReport, error) {
	var encoded string
	err := s.db.QueryRowContext(ctx, `SELECT report FROM protocol_verification_reports
		WHERE protocol_id=? AND revision_hash=? AND compiler_version=? AND kind=? ORDER BY id DESC LIMIT 1`, id, hash, protocol.CompilerVersion, protocol.OfflineVerification).Scan(&encoded)
	if err != nil {
		return protocol.VerificationReport{}, protocolRowError(err)
	}
	var report protocol.VerificationReport
	if err := json.Unmarshal([]byte(encoded), &report); err != nil {
		return report, err
	}
	return report, nil
}

// ListProtocolActivations reads one consistent active set for registry reload.
func (s *Store) ListProtocolActivations(ctx context.Context) ([]protocol.Activation, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT protocol_id, revision_hash, generation, activated_at FROM protocol_activations ORDER BY protocol_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	active := []protocol.Activation{}
	for rows.Next() {
		var activation protocol.Activation
		var timestamp string
		if err := rows.Scan(&activation.ProtocolID, &activation.RevisionHash, &activation.Generation, &timestamp); err != nil {
			return nil, err
		}
		activation.ActivatedAt = parseTime(timestamp)
		active = append(active, activation)
	}
	return active, rows.Err()
}

// SetProtocolActivation atomically compares and replaces one active pointer.
// Compilation and verification are the protocol service's responsibility.
func (s *Store) SetProtocolActivation(ctx context.Context, id, hash, expected string) (protocol.Activation, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return protocol.Activation{}, err
	}
	defer tx.Rollback()
	current := ""
	generation := int64(0)
	err = tx.QueryRowContext(ctx, `SELECT revision_hash, generation FROM protocol_activations WHERE protocol_id=?`, id).Scan(&current, &generation)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return protocol.Activation{}, err
	}
	if current != expected {
		return protocol.Activation{}, protocol.ErrRevisionConflict
	}
	activation := protocol.Activation{ProtocolID: id, RevisionHash: hash, Generation: generation + 1, ActivatedAt: time.Now().UTC()}
	_, err = tx.ExecContext(ctx, `INSERT INTO protocol_activations(protocol_id, revision_hash, generation, activated_at) VALUES(?,?,?,?)
		ON CONFLICT(protocol_id) DO UPDATE SET revision_hash=excluded.revision_hash, generation=excluded.generation, activated_at=excluded.activated_at`, id, hash, activation.Generation, activation.ActivatedAt.Format(time.RFC3339Nano))
	if err != nil {
		return protocol.Activation{}, err
	}
	if err := tx.Commit(); err != nil {
		return protocol.Activation{}, err
	}
	return activation, nil
}

func protocolRowError(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return protocol.ErrNotFound
	}
	return err
}
