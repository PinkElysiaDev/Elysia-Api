package storage

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/elysia-api/backend/protocol"
	"github.com/elysia-api/backend/protocol/builtin"
)

// ProtocolRefreshEvidenceBaseline supplements (without changing) the legacy
// backup fingerprint. It is used only after a lightweight check finds changes.
func (s *Store) ProtocolRefreshEvidenceBaseline(ctx context.Context) (string, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	return readProtocolRefreshEvidenceBaseline(ctx, tx)
}

func readProtocolRefreshEvidenceBaseline(ctx context.Context, reader protocolUpgradeReader) (string, error) {
	digest := sha256.New()
	encoder := json.NewEncoder(digest)
	for _, query := range []string{
		`SELECT * FROM protocol_verification_reports ORDER BY id`,
		`SELECT * FROM conversion_generation ORDER BY id`,
		`SELECT * FROM conversion_policy_activations ORDER BY id`,
		`SELECT * FROM conversion_policy_revisions ORDER BY id,hash`,
		`SELECT * FROM conversion_provider_evidence ORDER BY policy_hash,rule_id,scope_key,target_hash,compiler`,
	} {
		rows, err := reader.QueryContext(ctx, query)
		if err != nil {
			return "", err
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

// EquivalentProtocolDefinition compares JSON without discarding number
// precision. It is also suitable for definitions with different indentation.
func EquivalentProtocolDefinition(a, b protocol.Value) bool {
	var aObject, bObject any
	aErr := a.Decode(&aObject)
	bErr := b.Decode(&bObject)
	if aErr != nil || bErr != nil {
		return false
	}
	aCanonical, aErr := protocol.EncodeValue(aObject)
	bCanonical, bErr := protocol.EncodeValue(bObject)
	return aErr == nil && bErr == nil && string(aCanonical.Bytes()) == string(bCanonical.Bytes())
}

// Repair is intentionally separate from ordinary immutable revision writes.
// Only exact embedded definitions under engine-owned IDs can replace a damaged
// row. Raw bytes and original identity are retained before the update.
func repairPresetRevision(ctx context.Context, tx *sql.Tx, entry ProtocolUpgradeRevision) error {
	revision := entry.Revision
	definitions, err := builtin.Definitions()
	if err != nil {
		return err
	}
	allowed := false
	for _, definition := range definitions {
		var meta protocol.Definition
		if err := definition.Decode(&meta); err != nil {
			return err
		}
		if meta.ID == revision.ProtocolID && protocol.IsPresetProtocolID(meta.ID) && EquivalentProtocolDefinition(definition, revision.Definition) {
			canonical, err := protocol.EncodeValue(meta)
			if err != nil {
				return err
			}
			digest := sha256.Sum256(canonical.Bytes())
			allowed = revision.Hash == hex.EncodeToString(digest[:])
		}
	}
	if !allowed {
		return fmt.Errorf("preset repair requires the exact embedded definition: %s", revision.ProtocolID)
	}
	var raw, created string
	err = tx.QueryRowContext(ctx, `SELECT definition,created_at FROM protocol_revisions WHERE protocol_id=? AND content_hash=?`, revision.ProtocolID, revision.Hash).Scan(&raw, &created)
	if err != nil {
		return err
	}
	if err := archivePresetRaw(ctx, tx, revision.ProtocolID, revision.Hash, raw, created, "preset_repaired", false); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE protocol_revisions SET definition=? WHERE protocol_id=? AND content_hash=?`, string(revision.Definition.Bytes()), revision.ProtocolID, revision.Hash)
	return err
}

func archivePresetDraft(ctx context.Context, tx *sql.Tx, draft protocol.Draft) error {
	var raw, hash, created string
	err := tx.QueryRowContext(ctx, `SELECT content_hash,definition,updated_at FROM protocol_drafts WHERE protocol_id=?`, draft.ProtocolID).Scan(&hash, &raw, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	value, err := protocol.ParseValue([]byte(raw))
	if err == nil && hash == draft.Hash && EquivalentProtocolDefinition(value, draft.Definition) {
		return nil
	}
	// A following draft often equals the just-archived active revision byte for
	// byte. Retain one original artifact rather than two identical history rows.
	var archived int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM protocol_history WHERE protocol_id=? AND content_hash=? AND definition=?`, draft.ProtocolID, hash, raw).Scan(&archived); err != nil {
		return err
	}
	if archived > 0 {
		return nil
	}
	return archivePresetRaw(ctx, tx, draft.ProtocolID, hash, raw, created, "preset_draft_replaced", true)
}

func archivePresetRaw(ctx context.Context, tx *sql.Tx, id, hash, raw, created, reason string, isDraft bool) error {
	digest := sha256.Sum256([]byte(raw))
	archiveID := id + "~" + hash + "~" + reason + "~" + hex.EncodeToString(digest[:])
	_, err := tx.ExecContext(ctx, `INSERT INTO protocol_history(id,protocol_id,content_hash,definition,reason,is_draft,created_at,archived_at) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(id) DO NOTHING`, archiveID, id, hash, raw, reason, isDraft, created, nowString())
	return err
}
