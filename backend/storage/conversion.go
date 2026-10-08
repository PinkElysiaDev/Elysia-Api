package storage

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/elysia-api/backend/protocol"
)

const conversionStorageVersion = 2026100801

// Schema and completion marker share one transaction. Repeated starts only
// read the marker, with no DDL, history scan or backfill.
func (s *Store) migrateConversion(ctx context.Context) error {
	var present int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM schema_migrations WHERE version=?", conversionStorageVersion).Scan(&present); err != nil {
		return err
	}
	if present != 0 {
		var count int
		if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name IN ('conversion_policy_drafts','conversion_policy_revisions','conversion_policy_activations','protocol_continuations','conversion_generation','continuation_totals')").Scan(&count); err != nil {
			return err
		}
		if count != 6 {
			return fmt.Errorf("conversion migration recorded complete but required tables are missing")
		}
		return s.migrateConversionEvidence(ctx)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, stmt := range []string{
		`CREATE TABLE conversion_policy_drafts (id TEXT PRIMARY KEY, hash TEXT NOT NULL, document TEXT NOT NULL, updated_at TEXT NOT NULL)`,
		`CREATE TABLE conversion_policy_revisions (id TEXT NOT NULL, hash TEXT NOT NULL, document TEXT NOT NULL, reports TEXT NOT NULL, compiler_version TEXT NOT NULL, created_at TEXT NOT NULL, PRIMARY KEY(id,hash))`,
		`CREATE TABLE conversion_policy_activations (id TEXT PRIMARY KEY, hash TEXT NOT NULL, selector TEXT NOT NULL, generation INTEGER NOT NULL DEFAULT 1)`,
		`CREATE TABLE protocol_continuations (id TEXT PRIMARY KEY, owner TEXT NOT NULL, session TEXT NOT NULL, scope_key TEXT NOT NULL, parent TEXT NOT NULL, ordinal INTEGER NOT NULL, digest TEXT NOT NULL, ciphertext TEXT NOT NULL, bytes INTEGER NOT NULL, created_at INTEGER NOT NULL, expires_at INTEGER NOT NULL)`,
		`CREATE INDEX idx_continuation_match ON protocol_continuations(owner,session,scope_key,parent,ordinal,digest)`,
		`CREATE INDEX idx_continuation_expiry ON protocol_continuations(expires_at)`,
		`CREATE INDEX idx_continuation_eviction ON protocol_continuations(created_at,id)`,
		`CREATE TABLE conversion_generation (id INTEGER PRIMARY KEY CHECK(id=1), generation INTEGER NOT NULL)`,
		`INSERT INTO conversion_generation VALUES(1,0)`,
		`CREATE TRIGGER conversion_binding_insert AFTER INSERT ON protocol_bindings BEGIN UPDATE conversion_generation SET generation=generation+1 WHERE id=1; END`,
		`CREATE TRIGGER conversion_binding_update AFTER UPDATE ON protocol_bindings BEGIN UPDATE conversion_generation SET generation=generation+1 WHERE id=1; END`,
		`CREATE TRIGGER conversion_binding_delete AFTER DELETE ON protocol_bindings BEGIN UPDATE conversion_generation SET generation=generation+1 WHERE id=1; END`,
		`CREATE TRIGGER conversion_active_insert AFTER INSERT ON conversion_policy_activations BEGIN UPDATE conversion_generation SET generation=generation+1 WHERE id=1; END`,
		`CREATE TRIGGER conversion_active_update AFTER UPDATE ON conversion_policy_activations BEGIN UPDATE conversion_generation SET generation=generation+1 WHERE id=1; END`,
		`CREATE TRIGGER conversion_active_delete AFTER DELETE ON conversion_policy_activations BEGIN UPDATE conversion_generation SET generation=generation+1 WHERE id=1; END`,
		`CREATE TABLE continuation_totals (id INTEGER PRIMARY KEY CHECK(id=1), bytes INTEGER NOT NULL, records INTEGER NOT NULL, hits INTEGER NOT NULL DEFAULT 0, misses INTEGER NOT NULL DEFAULT 0, evicted INTEGER NOT NULL DEFAULT 0)`,
		`INSERT INTO continuation_totals(id,bytes,records) VALUES(1,0,0)`,
		`CREATE TRIGGER continuation_insert AFTER INSERT ON protocol_continuations BEGIN UPDATE continuation_totals SET bytes=bytes+NEW.bytes,records=records+1 WHERE id=1; END`,
		`CREATE TRIGGER continuation_delete AFTER DELETE ON protocol_continuations BEGIN UPDATE continuation_totals SET bytes=bytes-OLD.bytes,records=records-1 WHERE id=1; END`,
		`CREATE INDEX idx_continuation_session ON protocol_continuations(owner,session,scope_key,parent,created_at)`,
	} {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations(version,applied_at) VALUES(?,?)", conversionStorageVersion, nowString()); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return s.migrateConversionEvidence(ctx)
}

type ConversionPolicyRecord struct {
	ID              string                       `json:"id"`
	Hash            string                       `json:"hash"`
	Policy          protocol.ConversionPolicy    `json:"policy"`
	Reports         []protocol.CombinationReport `json:"reports,omitempty"`
	CompilerVersion string                       `json:"compilerVersion,omitempty"`
	Selector        protocol.ConversionMatch     `json:"selector"`
	ActiveHash      string                       `json:"activeHash"`
}

func (s *Store) SaveConversionDraft(ctx context.Context, p protocol.ConversionPolicy, expected string) (ConversionPolicyRecord, error) {
	// Drafts may be incomplete; only verification/activation compile them.
	raw, err := json.Marshal(p)
	if err != nil {
		return ConversionPolicyRecord{}, err
	}
	sum := sha256.Sum256(raw)
	draftHash := hex.EncodeToString(sum[:])
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ConversionPolicyRecord{}, err
	}
	defer tx.Rollback()
	var hash string
	err = tx.QueryRowContext(ctx, "SELECT hash FROM conversion_policy_drafts WHERE id=?", p.ID).Scan(&hash)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return ConversionPolicyRecord{}, err
	}
	if hash != expected {
		return ConversionPolicyRecord{}, protocol.ErrRevisionConflict
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO conversion_policy_drafts(id,hash,document,updated_at) VALUES(?,?,?,?) ON CONFLICT(id) DO UPDATE SET hash=excluded.hash,document=excluded.document,updated_at=excluded.updated_at", p.ID, draftHash, string(raw), nowString())
	if err != nil {
		return ConversionPolicyRecord{}, err
	}
	if err = tx.Commit(); err != nil {
		return ConversionPolicyRecord{}, err
	}
	return ConversionPolicyRecord{ID: p.ID, Hash: draftHash, Policy: p}, nil
}

func (s *Store) ListConversionPolicies(ctx context.Context, activeOnly bool) ([]ConversionPolicyRecord, error) {
	query := "SELECT d.id,d.hash,d.document,COALESCE(a.hash,''),COALESCE(a.selector,'{}') FROM conversion_policy_drafts d LEFT JOIN conversion_policy_activations a ON a.id=d.id ORDER BY d.id"
	if activeOnly {
		query = "SELECT r.id,r.hash,r.document,a.hash,a.selector FROM conversion_policy_activations a JOIN conversion_policy_revisions r ON r.id=a.id AND r.hash=a.hash ORDER BY r.id"
	}
	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ConversionPolicyRecord{}
	for rows.Next() {
		var item ConversionPolicyRecord
		var raw, selector string
		if err := rows.Scan(&item.ID, &item.Hash, &raw, &item.ActiveHash, &selector); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(raw), &item.Policy); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(selector), &item.Selector); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) SaveConversionRevision(ctx context.Context, record ConversionPolicyRecord) error {
	raw, err := json.Marshal(record.Policy)
	if err != nil {
		return err
	}
	reports, err := json.Marshal(record.Reports)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var hash string
	if err := tx.QueryRowContext(ctx, "SELECT hash FROM conversion_policy_drafts WHERE id=?", record.ID).Scan(&hash); err != nil {
		return err
	}
	if hash != record.Hash {
		return protocol.ErrRevisionConflict
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO conversion_policy_revisions(id,hash,document,reports,compiler_version,created_at) VALUES(?,?,?,?,?,?) ON CONFLICT(id,hash) DO UPDATE SET reports=excluded.reports,compiler_version=excluded.compiler_version", record.ID, record.Hash, string(raw), string(reports), protocol.CompilerVersion, nowString())
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO conversion_policy_evidence(policy_id,revision_hash,compiler,reports,created_at) VALUES(?,?,?,?,?)", record.ID, record.Hash, protocol.CompilerVersion, string(reports), nowString()); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ConversionRevisions(ctx context.Context, id string) ([]ConversionPolicyRecord, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT hash,document,reports,compiler_version FROM conversion_policy_revisions WHERE id=? ORDER BY created_at DESC", id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ConversionPolicyRecord{}
	for rows.Next() {
		r := ConversionPolicyRecord{ID: id}
		var raw, reports string
		if err := rows.Scan(&r.Hash, &raw, &reports, &r.CompilerVersion); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(raw), &r.Policy); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(reports), &r.Reports); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Activation and all derived binding evidence are committed together. expected
// bindings include the caller's full read set to reject concurrent edits.
func (s *Store) ActivateConversion(ctx context.Context, id, hash, expected string, selector protocol.ConversionMatch, before, after []ProtocolBinding, expectedGeneration ...int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := checkConversionGeneration(ctx, tx, expectedGeneration); err != nil {
		return err
	}
	var active string
	err = tx.QueryRowContext(ctx, "SELECT hash FROM conversion_policy_activations WHERE id=?", id).Scan(&active)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if active != expected {
		return protocol.ErrRevisionConflict
	}
	var compiler string
	if err := tx.QueryRowContext(ctx, "SELECT compiler_version FROM conversion_policy_revisions WHERE id=? AND hash=?", id, hash).Scan(&compiler); err != nil {
		return err
	}
	if compiler != protocol.CompilerVersion {
		return fmt.Errorf("policy verification is stale")
	}
	var count int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM protocol_bindings").Scan(&count); err != nil {
		return err
	}
	if count != len(before) {
		return protocol.ErrRevisionConflict
	}
	for _, b := range before {
		key, err := b.key()
		if err != nil {
			return err
		}
		var raw string
		if err := tx.QueryRowContext(ctx, "SELECT binding FROM protocol_bindings WHERE binding_key=?", key).Scan(&raw); err != nil {
			return err
		}
		current, _ := json.Marshal(b)
		var existing ProtocolBinding
		if err := json.Unmarshal([]byte(raw), &existing); err != nil {
			return err
		}
		normalized, _ := json.Marshal(existing)
		if string(current) != string(normalized) {
			return protocol.ErrRevisionConflict
		}
	}
	for _, b := range after {
		if err := saveProtocolBinding(ctx, tx, b); err != nil {
			return err
		}
	}
	raw, err := json.Marshal(selector)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO conversion_policy_activations(id,hash,selector) VALUES(?,?,?) ON CONFLICT(id) DO UPDATE SET hash=excluded.hash,selector=excluded.selector,generation=generation+1", id, hash, string(raw))
	if err != nil {
		return err
	}
	return tx.Commit()
}

type StoredContinuation struct {
	ID, Owner, Session, ScopeKey, Parent, Digest, Ciphertext string
	Ordinal                                                  int
	ExpiresAt                                                int64
	// Lookup-only: tools are matched by their content digest, which includes the
	// stable call ID and arguments. Positions remain mandatory for other nodes.
	MatchToolCall bool
}

func (s *Store) SaveContinuation(ctx context.Context, r StoredContinuation, limits protocol.ContinuationSettings) error {
	if !s.codec.enabled() {
		return fmt.Errorf("persistent continuation requires encryption")
	}
	if !strings.HasPrefix(r.Ciphertext, protocol.ContinuationPrefix) || len(r.Ciphertext) > limits.RecordBytes*2+256 || int64(len(r.Ciphertext)) > limits.MaxBytes {
		return fmt.Errorf("continuation record exceeds byte limit")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().Unix()
	if _, err := tx.ExecContext(ctx, "DELETE FROM protocol_continuations WHERE id IN (SELECT id FROM protocol_continuations WHERE expires_at<=? LIMIT 256)", now); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO protocol_continuations(id,owner,session,scope_key,parent,ordinal,digest,ciphertext,bytes,created_at,expires_at) VALUES(?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO NOTHING", r.ID, r.Owner, r.Session, r.ScopeKey, r.Parent, r.Ordinal, r.Digest, r.Ciphertext, len(r.Ciphertext), time.Now().UnixNano(), r.ExpiresAt)
	if err != nil {
		return err
	}
	if r.Session != "" {
		_, err = tx.ExecContext(ctx, "DELETE FROM protocol_continuations WHERE owner=? AND session=? AND scope_key=? AND parent NOT IN (SELECT parent FROM protocol_continuations WHERE owner=? AND session=? AND scope_key=? GROUP BY parent ORDER BY MAX(created_at) DESC,parent LIMIT ?)", r.Owner, r.Session, r.ScopeKey, r.Owner, r.Session, r.ScopeKey, limits.TurnsPerSession)
		if err != nil {
			return err
		}
	}
	var size int64
	if err := tx.QueryRowContext(ctx, "SELECT bytes FROM continuation_totals WHERE id=1").Scan(&size); err != nil {
		return err
	}
	for size > limits.MaxBytes {
		var id string
		var bytes int64
		if err := tx.QueryRowContext(ctx, "SELECT id,bytes FROM protocol_continuations ORDER BY created_at,id LIMIT 1").Scan(&id, &bytes); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM protocol_continuations WHERE id=?", id); err != nil {
			return err
		}
		size -= bytes
		if _, err := tx.ExecContext(ctx, "UPDATE continuation_totals SET evicted=evicted+1 WHERE id=1"); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) FindContinuation(ctx context.Context, r StoredContinuation) ([]string, error) {
	if r.Session == "" {
		return nil, nil
	}
	query := "SELECT ciphertext FROM protocol_continuations WHERE owner=? AND session=? AND scope_key=? AND parent=? AND digest=? AND expires_at>?"
	args := []any{r.Owner, r.Session, r.ScopeKey, r.Parent, r.Digest, time.Now().Unix()}
	if !r.MatchToolCall {
		query += " AND ordinal=?"
		args = append(args, r.Ordinal)
	}
	rows, err := s.db.QueryContext(ctx, query+" LIMIT 2", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			return nil, err
		}
		out = append(out, value)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	field := "misses"
	if len(out) == 1 {
		field = "hits"
	}
	_, err = s.db.ExecContext(ctx, "UPDATE continuation_totals SET "+field+"="+field+"+1 WHERE id=1")
	return out, err
}

func (s *Store) ContinuationStats(ctx context.Context) (map[string]int64, error) {
	out := map[string]int64{}
	var count, size, expired int64
	err := s.db.QueryRowContext(ctx, "SELECT COUNT(*),COALESCE(SUM(bytes),0),COALESCE(SUM(CASE WHEN expires_at<=? THEN 1 ELSE 0 END),0) FROM protocol_continuations", time.Now().Unix()).Scan(&count, &size, &expired)
	out["records"], out["bytes"], out["expired"] = count, size, expired
	if err != nil {
		return out, err
	}
	var hits, misses, evicted int64
	err = s.db.QueryRowContext(ctx, "SELECT hits,misses,evicted FROM continuation_totals WHERE id=1").Scan(&hits, &misses, &evicted)
	out["hits"], out["misses"], out["evicted"] = hits, misses, evicted
	return out, err
}

func (s *Store) ClearContinuations(ctx context.Context, session string) error {
	if session == "" {
		return fmt.Errorf("explicit session is required")
	}
	_, err := s.db.ExecContext(ctx, "DELETE FROM protocol_continuations WHERE session=?", session)
	return err
}
