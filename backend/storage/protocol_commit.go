package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/elysia-api/backend/protocol"
)

type protocolCommit struct {
	plan           ProtocolUpgrade
	overwrites     bool
	insertBindings map[string]bool
}

func checkProtocolCommitBaseline(ctx context.Context, tx *sql.Tx, plan ProtocolUpgrade) error {
	baseline, err := readProtocolUpgradeBaseline(ctx, tx)
	if err != nil {
		return err
	}
	if baseline != plan.Baseline {
		return protocol.ErrRevisionConflict
	}
	if plan.EvidenceBaseline != "" {
		evidence, err := readProtocolRefreshEvidenceBaseline(ctx, tx)
		if err != nil {
			return err
		}
		if evidence != plan.EvidenceBaseline {
			return protocol.ErrRevisionConflict
		}
	}
	return nil
}

func (s *Store) prepareProtocolCommit(ctx context.Context, plan ProtocolUpgrade) (*protocolCommit, *ProtocolSnapshot, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, nil, err
	}
	defer tx.Rollback()
	if err = checkProtocolCommitBaseline(ctx, tx, plan); err != nil {
		return nil, nil, err
	}
	if plan.EvidenceBaseline == "" {
		plan.EvidenceBaseline, err = readProtocolRefreshEvidenceBaseline(ctx, tx)
		if err != nil {
			return nil, nil, err
		}
	}
	prepared, err := normalizeProtocolCommit(ctx, tx, plan)
	if err != nil {
		return nil, nil, err
	}
	if err = tx.Rollback(); err != nil {
		return nil, nil, err
	}
	var snapshot *ProtocolSnapshot
	if prepared.overwrites {
		snapshot, err = s.ensureProtocolSnapshot(ctx, plan.Baseline, plan.EvidenceBaseline)
	}
	return prepared, snapshot, err
}

func (s *Store) beginProtocolCommit(ctx context.Context, prepared *protocolCommit) (*sql.Tx, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	if err = checkProtocolCommitBaseline(ctx, tx, prepared.plan); err != nil {
		tx.Rollback()
		return nil, err
	}
	return tx, nil
}

// Derive insert-only writes from persisted rows, including damaged JSON.
func normalizeProtocolCommit(ctx context.Context, tx *sql.Tx, plan ProtocolUpgrade) (*protocolCommit, error) {
	out := &protocolCommit{plan: plan, insertBindings: map[string]bool{}}
	out.plan.Revisions = nil
	out.plan.Bindings = nil
	read := func(query string, args ...any) (string, bool, error) {
		var raw string
		err := tx.QueryRowContext(ctx, query, args...).Scan(&raw)
		if errors.Is(err, sql.ErrNoRows) {
			return "", false, nil
		}
		return raw, err == nil, err
	}
	for _, entry := range plan.Revisions {
		w := ProtocolRefreshWrite{Revision: true, Report: true, Draft: true, Activation: true}
		if entry.Refresh != nil {
			w = *entry.Refresh
		}
		id, hash := entry.Revision.ProtocolID, entry.Revision.Hash
		if w.Revision || w.RepairPreset {
			raw, exists, err := read(`SELECT definition FROM protocol_revisions WHERE protocol_id=? AND content_hash=?`, id, hash)
			if err != nil {
				return nil, err
			}
			if exists {
				w.Revision = false
				if sameProtocolJSON(raw, string(entry.Revision.Definition.Bytes())) {
					w.RepairPreset = false
				} else if !w.RepairPreset {
					return nil, fmt.Errorf("%w: immutable revision %s/%s differs", ErrCorruptProtocolRecord, id, hash)
				} else {
					out.overwrites = true
				}
			} else {
				w.Revision = true
				w.RepairPreset = false
			}
		}
		if w.Draft {
			raw, exists, err := read(`SELECT definition FROM protocol_drafts WHERE protocol_id=?`, id)
			if err != nil {
				return nil, err
			}
			storedHash, _, err := read(`SELECT content_hash FROM protocol_drafts WHERE protocol_id=?`, id)
			if err != nil {
				return nil, err
			}
			w.Draft = !exists || storedHash != entry.Draft.Hash || !sameProtocolJSON(raw, string(entry.Draft.Definition.Bytes()))
			w.insertDraft = !exists
			out.overwrites = out.overwrites || (exists && w.Draft)
		}
		if w.Activation {
			var oldHash, at string
			var generation int64
			err := tx.QueryRowContext(ctx, `SELECT revision_hash,generation,activated_at FROM protocol_activations WHERE protocol_id=?`, id).Scan(&oldHash, &generation, &at)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return nil, err
			}
			w.insertActivation = errors.Is(err, sql.ErrNoRows)
			_, dateErr := time.Parse(time.RFC3339Nano, at)
			w.Activation = w.insertActivation || oldHash != hash || generation < 1 || dateErr != nil
			out.overwrites = out.overwrites || (!w.insertActivation && w.Activation)
		}
		if w.Report {
			raw, exists, err := read(`SELECT report FROM protocol_verification_reports WHERE protocol_id=? AND revision_hash=? AND compiler_version=? AND kind=? ORDER BY id DESC LIMIT 1`, id, hash, entry.Report.CompilerVersion, entry.Report.Kind)
			if err != nil {
				return nil, err
			}
			encoded, err := json.Marshal(entry.Report)
			if err != nil {
				return nil, err
			}
			w.Report = !exists || !sameProtocolJSON(raw, string(encoded))
		}
		entry.Refresh = &w
		out.plan.Revisions = append(out.plan.Revisions, entry)
	}
	for _, binding := range plan.Bindings {
		key, err := binding.key()
		if err != nil {
			return nil, err
		}
		raw, exists, err := read(`SELECT binding FROM protocol_bindings WHERE binding_key=?`, key)
		if err != nil {
			return nil, err
		}
		encoded, err := json.Marshal(binding)
		if err != nil {
			return nil, err
		}
		if exists && sameProtocolJSON(raw, string(encoded)) {
			continue
		}
		out.insertBindings[key] = !exists
		out.overwrites = out.overwrites || exists
		out.plan.Bindings = append(out.plan.Bindings, binding)
	}
	return out, nil
}

func sameProtocolJSON(a, b string) bool {
	av, ae := protocol.ParseValue([]byte(a))
	bv, be := protocol.ParseValue([]byte(b))
	return ae == nil && be == nil && EquivalentProtocolDefinition(av, bv)
}

func writeProtocolCommit(ctx context.Context, tx *sql.Tx, prepared *protocolCommit) error {
	for _, entry := range prepared.plan.Revisions {
		if err := writeProtocolUpgradeRevision(ctx, tx, entry); err != nil {
			return err
		}
	}
	for _, binding := range prepared.plan.Bindings {
		key, err := binding.key()
		if err != nil {
			return err
		}
		raw, err := json.Marshal(binding)
		if err != nil {
			return err
		}
		query := `UPDATE protocol_bindings SET binding=? WHERE binding_key=?`
		if prepared.insertBindings[key] {
			query = `INSERT INTO protocol_bindings(binding,binding_key) VALUES(?,?)`
		}
		if _, err := tx.ExecContext(ctx, query, string(raw), key); err != nil {
			return err
		}
	}
	return nil
}
