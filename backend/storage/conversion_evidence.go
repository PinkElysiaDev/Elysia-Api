package storage

import (
	"context"

	"github.com/elysia-api/backend/protocol"
)

func (s *Store) SaveConversionProviderEvidence(ctx context.Context, policyHash, ruleID, scopeKey, targetHash string, expiresAt int64) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO conversion_provider_evidence(policy_hash,rule_id,scope_key,target_hash,compiler,expires_at) VALUES(?,?,?,?,?,?) ON CONFLICT(policy_hash,rule_id,scope_key,target_hash,compiler) DO UPDATE SET expires_at=excluded.expires_at`, policyHash, ruleID, scopeKey, targetHash, protocol.CompilerVersion, expiresAt)
	return err
}

func (s *Store) ConversionProviderEvidence(ctx context.Context, policyHash, scopeKey, targetHash string, now int64) (map[string]bool, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT rule_id FROM conversion_provider_evidence WHERE policy_hash=? AND scope_key=? AND target_hash=? AND compiler=? AND expires_at>?", policyHash, scopeKey, targetHash, protocol.CompilerVersion, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}
