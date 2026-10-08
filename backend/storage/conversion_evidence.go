package storage

import (
	"context"
	"github.com/elysia-api/backend/protocol"
)

// Evidence has an independent migration so older conversion storage can be
// upgraded without recreating policies, continuations or their completion mark.
func (s *Store) migrateConversionEvidence(ctx context.Context) error {
	const version = 2026100802
	var found int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM schema_migrations WHERE version=?", version).Scan(&found); err != nil {
		return err
	}
	if found > 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, statement := range []string{
		`CREATE TABLE conversion_provider_evidence (policy_hash TEXT NOT NULL, rule_id TEXT NOT NULL, scope_key TEXT NOT NULL, target_hash TEXT NOT NULL, compiler TEXT NOT NULL, expires_at INTEGER NOT NULL, PRIMARY KEY(policy_hash,rule_id,scope_key,target_hash,compiler))`,
		`CREATE TABLE conversion_policy_evidence (sequence INTEGER PRIMARY KEY AUTOINCREMENT, policy_id TEXT NOT NULL, revision_hash TEXT NOT NULL, compiler TEXT NOT NULL, reports TEXT NOT NULL, created_at TEXT NOT NULL)`,
		`CREATE INDEX idx_conversion_policy_evidence ON conversion_policy_evidence(policy_id,revision_hash,sequence)`,
		`CREATE TRIGGER conversion_models_insert AFTER INSERT ON models BEGIN UPDATE conversion_generation SET generation=generation+1 WHERE id=1; END`,
		`CREATE TRIGGER conversion_models_update AFTER UPDATE ON models BEGIN UPDATE conversion_generation SET generation=generation+1 WHERE id=1; END`,
		`CREATE TRIGGER conversion_models_delete AFTER DELETE ON models BEGIN UPDATE conversion_generation SET generation=generation+1 WHERE id=1; END`,
		`CREATE TRIGGER conversion_model_sources_insert AFTER INSERT ON model_sources BEGIN UPDATE conversion_generation SET generation=generation+1 WHERE id=1; END`,
		`CREATE TRIGGER conversion_model_sources_update AFTER UPDATE ON model_sources BEGIN UPDATE conversion_generation SET generation=generation+1 WHERE id=1; END`,
		`CREATE TRIGGER conversion_model_sources_delete AFTER DELETE ON model_sources BEGIN UPDATE conversion_generation SET generation=generation+1 WHERE id=1; END`,
		`CREATE TRIGGER conversion_model_groups_insert AFTER INSERT ON model_groups BEGIN UPDATE conversion_generation SET generation=generation+1 WHERE id=1; END`,
		`CREATE TRIGGER conversion_model_groups_update AFTER UPDATE ON model_groups BEGIN UPDATE conversion_generation SET generation=generation+1 WHERE id=1; END`,
		`CREATE TRIGGER conversion_model_groups_delete AFTER DELETE ON model_groups BEGIN UPDATE conversion_generation SET generation=generation+1 WHERE id=1; END`,
	} {
		if _, err = tx.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO schema_migrations(version,applied_at) VALUES(?,?)", version, nowString()); err != nil {
		return err
	}
	return tx.Commit()
}

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
