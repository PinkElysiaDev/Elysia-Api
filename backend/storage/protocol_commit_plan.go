package storage

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/elysia-api/backend/protocol"
)

// ProtocolUpgradeRevision is a verified runtime revision prepared without writes.
type ProtocolUpgradeRevision struct {
	Revision protocol.Revision           `json:"revision"`
	Report   protocol.VerificationReport `json:"report"`
	Draft    protocol.Draft              `json:"draft"`
	// Refresh limits writes to defects found by the runtime recovery check.
	// Refresh selects the exact writes required by runtime recovery.
	Refresh *ProtocolRefreshWrite `json:"refresh,omitempty"`
}

type ProtocolRefreshWrite struct {
	Revision         bool `json:"revision,omitempty"`
	Report           bool `json:"report,omitempty"`
	Draft            bool `json:"draft,omitempty"`
	Activation       bool `json:"activation,omitempty"`
	RepairPreset     bool `json:"repairPreset,omitempty"`
	insertDraft      bool
	insertActivation bool
}

func (w ProtocolRefreshWrite) Changed() bool {
	return w.Revision || w.Report || w.Draft || w.Activation || w.RepairPreset
}

// ProtocolUpgrade describes a verified runtime refresh, never individual writes.
// Baseline binds the refresh to configuration, drafts, activations and bindings.
type ProtocolUpgrade struct {
	Baseline  string                    `json:"baseline"`
	Revisions []ProtocolUpgradeRevision `json:"revisions"`
	Bindings  []ProtocolBinding         `json:"bindings"`
	// EvidenceBaseline guards reports and conversion configuration, which were
	// deliberately not part of the original backup fingerprint format.
	EvidenceBaseline string `json:"evidenceBaseline,omitempty"`
	// Rejections refresh evidence for an unchanged custom revision without
	// activating it or rewriting its definition/draft.
	Rejections []ProtocolUpgradeRejection `json:"rejections,omitempty"`
}

type ProtocolUpgradeRejection struct {
	ProtocolID string                      `json:"protocolId"`
	Report     protocol.VerificationReport `json:"report"`
}

type protocolUpgradeReader interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

// The fingerprint includes credential ciphertext, never plaintext or a public
// snapshot. Usage/log writes deliberately do not invalidate a configuration plan.
var protocolUpgradeQueries = []string{
	`SELECT * FROM protocol_history ORDER BY id`,
	`SELECT * FROM protocol_revisions ORDER BY protocol_id,content_hash`,
	`SELECT * FROM model_sources ORDER BY id`,
	`SELECT * FROM models ORDER BY source_id,id`,
	`SELECT * FROM model_groups ORDER BY id`,
	`SELECT * FROM model_group_models ORDER BY group_id,position,source_id,model_id`,
	`SELECT * FROM protocol_drafts ORDER BY protocol_id`,
	`SELECT * FROM protocol_activations ORDER BY protocol_id`,
	`SELECT * FROM protocol_bindings ORDER BY binding_key`,
}

// ProtocolUpgradeBaseline returns an opaque configuration fingerprint for atomic edits.
func (store *Store) ProtocolUpgradeBaseline(ctx context.Context) (string, error) {
	tx, err := store.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	return readProtocolUpgradeBaseline(ctx, tx)
}

func readProtocolUpgradeBaseline(ctx context.Context, reader protocolUpgradeReader) (string, error) {
	digest := sha256.New()
	encoder := json.NewEncoder(digest)
	for _, query := range protocolUpgradeQueries {
		rows, err := reader.QueryContext(ctx, query)
		if err != nil {
			return "", fmt.Errorf("protocol configuration storage: %w", err)
		}
		err = hashProtocolUpgradeRows(rows, encoder, query)
		closeErr := rows.Close()
		if err != nil {
			return "", err
		}
		if closeErr != nil {
			return "", closeErr
		}
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

func hashProtocolUpgradeRows(rows *sql.Rows, encoder *json.Encoder, query string) error {
	columns, err := rows.Columns()
	if err != nil {
		return err
	}
	if err := encoder.Encode([]any{query, columns}); err != nil {
		return err
	}
	for rows.Next() {
		values := make([]any, len(columns))
		targets := make([]any, len(columns))
		for index := range values {
			targets[index] = &values[index]
		}
		if err := rows.Scan(targets...); err != nil {
			return err
		}
		if err := encoder.Encode(values); err != nil {
			return err
		}
	}
	return rows.Err()
}

func validateProtocolUpgrade(plan ProtocolUpgrade) error {
	revisions := map[string]string{}
	for _, entry := range plan.Revisions {
		revision, report := entry.Revision, entry.Report
		if revision.ProtocolID == "" || revision.Hash == "" || revisions[revision.ProtocolID] != "" || entry.Draft.ProtocolID != revision.ProtocolID || entry.Draft.Hash == "" {
			return fmt.Errorf("invalid or duplicated protocol upgrade identity")
		}
		if !report.Passed || report.Kind != protocol.OfflineVerification || report.CompilerVersion != protocol.CompilerVersion || report.DefinitionHash != revision.Hash || report.SamplesHash == "" || len(report.Checks) == 0 || protocol.IssuesError(report.Issues) != nil {
			return fmt.Errorf("protocol %s lacks current passing offline evidence", revision.ProtocolID)
		}
		revisions[revision.ProtocolID] = revision.Hash
	}
	for _, entry := range plan.Bindings {
		if entry.Unbound {
			continue
		}
		if revisions[entry.Binding.ProtocolID] != entry.Binding.RevisionHash || entry.Binding.RevisionHash == "" {
			return fmt.Errorf("binding references a protocol outside the verified upgrade")
		}
	}
	return nil
}

func writeProtocolUpgradeRevision(ctx context.Context, tx *sql.Tx, entry ProtocolUpgradeRevision) error {
	revision, draft := entry.Revision, entry.Draft
	if entry.Refresh == nil {
		return fmt.Errorf("protocol writes must be normalized before commit")
	}
	writes := *entry.Refresh
	if !writes.Changed() {
		return nil
	}
	if protocol.IsPresetProtocolID(revision.ProtocolID) && writes.Activation {
		if err := archiveReplacedPreset(ctx, tx, revision.ProtocolID, revision.Hash); err != nil {
			return err
		}
	}
	if writes.RepairPreset {
		if err := repairPresetRevision(ctx, tx, entry); err != nil {
			return err
		}
	}
	if writes.Draft {
		if protocol.IsPresetProtocolID(draft.ProtocolID) {
			if err := archivePresetDraft(ctx, tx, draft); err != nil {
				return err
			}
		}
		query := `UPDATE protocol_drafts SET content_hash=?,definition=?,updated_at=? WHERE protocol_id=?`
		args := []any{draft.Hash, string(draft.Definition.Bytes()), draft.UpdatedAt.UTC().Format(time.RFC3339Nano), draft.ProtocolID}
		if writes.insertDraft {
			query = `INSERT INTO protocol_drafts(content_hash,definition,updated_at,protocol_id) VALUES(?,?,?,?)`
		}
		if _, err := tx.ExecContext(ctx, query, args...); err != nil {
			return err
		}
	}
	if writes.Revision {
		if _, err := tx.ExecContext(ctx, `INSERT INTO protocol_revisions(protocol_id,content_hash,definition,created_at) VALUES(?,?,?,?)`, revision.ProtocolID, revision.Hash, string(revision.Definition.Bytes()), revision.CreatedAt.UTC().Format(time.RFC3339Nano)); err != nil {
			return err
		}
	}
	if writes.Report {
		if err := saveProtocolReport(ctx, tx, revision.ProtocolID, revision.Hash, entry.Report); err != nil {
			return err
		}
	}
	if !writes.Activation {
		return nil
	}
	query := `UPDATE protocol_activations SET revision_hash=?,generation=MAX(generation,0)+1,activated_at=? WHERE protocol_id=?`
	if writes.insertActivation {
		query = `INSERT INTO protocol_activations(revision_hash,activated_at,protocol_id,generation) VALUES(?,?,?,1)`
	}
	_, err := tx.ExecContext(ctx, query, revision.Hash, nowString(), revision.ProtocolID)
	return err
}
