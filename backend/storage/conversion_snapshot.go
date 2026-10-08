package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/elysia-api/backend/protocol"
)

// ConversionSnapshot reads policies and their derived binding evidence in one
// SQLite snapshot. Already running requests retain their own immutable values.
func (s *Store) ConversionSnapshot(ctx context.Context) ([]ConversionPolicyRecord, []ProtocolBinding, int64, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, nil, 0, err
	}
	defer tx.Rollback()
	var generation int64
	if err = tx.QueryRowContext(ctx, "SELECT generation FROM conversion_generation WHERE id=1").Scan(&generation); err != nil {
		return nil, nil, 0, err
	}
	rows, err := tx.QueryContext(ctx, "SELECT binding FROM protocol_bindings ORDER BY binding_key")
	if err != nil {
		return nil, nil, 0, err
	}
	bindings := []ProtocolBinding{}
	for rows.Next() {
		var raw string
		var b ProtocolBinding
		if err = rows.Scan(&raw); err == nil {
			err = json.Unmarshal([]byte(raw), &b)
		}
		if err != nil {
			rows.Close()
			return nil, nil, 0, err
		}
		bindings = append(bindings, b)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, nil, 0, err
	}
	rows, err = tx.QueryContext(ctx, "SELECT r.id,r.hash,r.document,a.hash,a.selector FROM conversion_policy_activations a JOIN conversion_policy_revisions r ON r.id=a.id AND r.hash=a.hash ORDER BY r.id")
	if err != nil {
		return nil, nil, 0, err
	}
	policies := []ConversionPolicyRecord{}
	for rows.Next() {
		var r ConversionPolicyRecord
		var raw, selector string
		if err = rows.Scan(&r.ID, &r.Hash, &raw, &r.ActiveHash, &selector); err == nil {
			err = json.Unmarshal([]byte(raw), &r.Policy)
		}
		if err == nil {
			err = json.Unmarshal([]byte(selector), &r.Selector)
		}
		if err != nil {
			rows.Close()
			return nil, nil, 0, err
		}
		policies = append(policies, r)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, nil, 0, err
	}
	// Explicit pins refer to immutable verified revisions, independently of which
	// revision is currently selected by the pair-wide activation.
	for _, b := range bindings {
		if b.Conversion == nil || b.Conversion.PolicyID == "" {
			continue
		}
		selection := b.Conversion
		found := false
		for _, p := range policies {
			found = found || p.ID == selection.PolicyID && p.Hash == selection.RevisionHash
		}
		if found {
			continue
		}
		var r ConversionPolicyRecord
		r.ID, r.Hash = selection.PolicyID, selection.RevisionHash
		var raw string
		if err = tx.QueryRowContext(ctx, "SELECT document,compiler_version FROM conversion_policy_revisions WHERE id=? AND hash=?", r.ID, r.Hash).Scan(&raw, &r.CompilerVersion); err != nil {
			return nil, nil, 0, fmt.Errorf("conversion revision %s unavailable: %w", r.ID, err)
		}
		if err = json.Unmarshal([]byte(raw), &r.Policy); err != nil {
			return nil, nil, 0, err
		}
		policies = append(policies, r)
	}
	if err = tx.Commit(); err != nil {
		return nil, nil, 0, err
	}
	return policies, bindings, generation, nil
}

func checkConversionGeneration(ctx context.Context, tx *sql.Tx, expected []int64) error {
	if len(expected) == 0 {
		return nil
	}
	var actual int64
	if err := tx.QueryRowContext(ctx, "SELECT generation FROM conversion_generation WHERE id=1").Scan(&actual); err != nil {
		return err
	}
	if actual != expected[0] {
		return protocol.ErrRevisionConflict
	}
	return nil
}
