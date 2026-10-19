// Current SQLite schema. Historical schemas and data migrations are not supported.
package storage

import (
	"context"
	"errors"
	"strings"
)

func (s *Store) createSchema(ctx context.Context) error {
	if err := s.createProtocolTables(ctx); err != nil {
		return err
	}
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS settings (key TEXT PRIMARY KEY, value TEXT NOT NULL, updated_at TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS api_tokens (name TEXT PRIMARY KEY, token TEXT NOT NULL, enabled INTEGER NOT NULL DEFAULT 1, allowed_groups_json TEXT NOT NULL DEFAULT '[]', token_hash TEXT NOT NULL DEFAULT '', scopes TEXT NOT NULL DEFAULT '[]', created_at TEXT NOT NULL, updated_at TEXT NOT NULL)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_api_tokens_hash ON api_tokens(token_hash) WHERE token_hash != ''`,
		`CREATE TABLE IF NOT EXISTS model_sources (id TEXT PRIMARY KEY, name TEXT NOT NULL, base_url TEXT NOT NULL, api_key TEXT NOT NULL DEFAULT '', platform TEXT NOT NULL, enabled INTEGER NOT NULL DEFAULT 1, auto_fetch_models INTEGER NOT NULL DEFAULT 1, manual_models_json TEXT NOT NULL DEFAULT '[]', fetch_base_url TEXT NOT NULL DEFAULT '', api_keys TEXT NOT NULL DEFAULT '', key_strategy TEXT NOT NULL DEFAULT 'single', cache_synthesis INTEGER NOT NULL DEFAULT 0, created_at TEXT NOT NULL, updated_at TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS models (id TEXT NOT NULL, source_id TEXT NOT NULL DEFAULT '', name TEXT NOT NULL, source_name TEXT NOT NULL DEFAULT '', base_url TEXT NOT NULL, api_key TEXT NOT NULL DEFAULT '', platform TEXT NOT NULL, type TEXT NOT NULL DEFAULT 'llm', max_tokens INTEGER NOT NULL DEFAULT 0, vision_capable INTEGER NOT NULL DEFAULT 0, tools_capable INTEGER NOT NULL DEFAULT 0, structured_output INTEGER NOT NULL DEFAULT 0, thinking_mode TEXT NOT NULL DEFAULT 'both', available INTEGER NOT NULL DEFAULT 1, enabled INTEGER NOT NULL DEFAULT 1, origin TEXT NOT NULL DEFAULT 'fetched', capability_source TEXT NOT NULL DEFAULT '', last_checked_at TEXT NOT NULL, PRIMARY KEY (id, source_id))`,
		`CREATE TABLE IF NOT EXISTS model_groups (id TEXT PRIMARY KEY, name TEXT NOT NULL UNIQUE, enabled INTEGER NOT NULL DEFAULT 1, strategy TEXT NOT NULL DEFAULT 'round-robin', max_retries INTEGER NOT NULL DEFAULT 3, retry_interval INTEGER NOT NULL DEFAULT 1000, max_concurrency INTEGER NOT NULL DEFAULT 0, daily_limit_max_requests INTEGER NOT NULL DEFAULT 0, daily_limit_max_tokens INTEGER NOT NULL DEFAULT 0, type TEXT NOT NULL DEFAULT 'llm', max_tokens INTEGER NOT NULL DEFAULT 0, vision_capable INTEGER NOT NULL DEFAULT 0, tools_capable INTEGER NOT NULL DEFAULT 0, created_at TEXT NOT NULL, updated_at TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS model_group_models (group_id TEXT NOT NULL, model_id TEXT NOT NULL, source_id TEXT NOT NULL DEFAULT '', position INTEGER NOT NULL DEFAULT 0, PRIMARY KEY (group_id, model_id, source_id), FOREIGN KEY(group_id) REFERENCES model_groups(id) ON DELETE CASCADE)`,
		`CREATE TABLE IF NOT EXISTS usage_records (request_id TEXT PRIMARY KEY, started_at TEXT NOT NULL, started_ms INTEGER NOT NULL DEFAULT 0, ended_at TEXT NOT NULL, key_name TEXT NOT NULL DEFAULT '', key_hash TEXT NOT NULL DEFAULT '', requested_model_group TEXT NOT NULL DEFAULT '', group_id TEXT NOT NULL DEFAULT '', group_name TEXT NOT NULL DEFAULT '', model_id TEXT NOT NULL DEFAULT '', model_name TEXT NOT NULL DEFAULT '', source_id TEXT NOT NULL DEFAULT '', platform TEXT NOT NULL DEFAULT '', source_format TEXT NOT NULL DEFAULT '', target_format TEXT NOT NULL DEFAULT '', relay_mode TEXT NOT NULL DEFAULT '', responses_mode TEXT NOT NULL DEFAULT '', usage_source TEXT NOT NULL DEFAULT '', stream INTEGER NOT NULL DEFAULT 0, status_code INTEGER NOT NULL DEFAULT 0, error TEXT NOT NULL DEFAULT '', first_byte_ms INTEGER NOT NULL DEFAULT 0, duration_ms INTEGER NOT NULL DEFAULT 0, input_tokens INTEGER NOT NULL DEFAULT 0, output_tokens INTEGER NOT NULL DEFAULT 0, total_tokens INTEGER NOT NULL DEFAULT 0, cache_hit_tokens INTEGER NOT NULL DEFAULT 0, cache_creation_tokens INTEGER NOT NULL DEFAULT 0, usage_report_mask INTEGER NOT NULL DEFAULT 0, request_truncated INTEGER NOT NULL DEFAULT 0, response_truncated INTEGER NOT NULL DEFAULT 0, record_json TEXT NOT NULL, content_bytes INTEGER NOT NULL DEFAULT 0)`,
		`CREATE INDEX IF NOT EXISTS idx_usage_agg_cover ON usage_records(started_ms, model_name, group_name, key_name, status_code, stream, input_tokens, output_tokens, total_tokens, cache_hit_tokens, cache_creation_tokens, usage_report_mask, duration_ms, first_byte_ms)`,
		`CREATE INDEX IF NOT EXISTS idx_usage_content ON usage_records(content_bytes)`,
		`CREATE TABLE IF NOT EXISTS system_logs (id INTEGER PRIMARY KEY AUTOINCREMENT, created_at TEXT NOT NULL, created_ms INTEGER NOT NULL DEFAULT 0, level TEXT NOT NULL, message TEXT NOT NULL, fields_json TEXT NOT NULL DEFAULT '{}', content_bytes INTEGER NOT NULL DEFAULT 0)`,
		`CREATE INDEX IF NOT EXISTS idx_system_logs_created_at ON system_logs(created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_system_content ON system_logs(content_bytes)`,
		`CREATE INDEX IF NOT EXISTS idx_system_time ON system_logs(created_ms, id)`,
		`CREATE TABLE IF NOT EXISTS usage_assets (asset_file TEXT PRIMARY KEY, size_bytes INTEGER NOT NULL CHECK(size_bytes>=0))`,
		`CREATE TABLE IF NOT EXISTS usage_asset_refs (asset_file TEXT NOT NULL REFERENCES usage_assets(asset_file), request_id TEXT NOT NULL REFERENCES usage_records(request_id) ON DELETE CASCADE, PRIMARY KEY(asset_file, request_id))`,
		`CREATE INDEX IF NOT EXISTS idx_usage_asset_refs_request ON usage_asset_refs(request_id)`,
		`CREATE TABLE IF NOT EXISTS agent_sessions (
			id TEXT PRIMARY KEY, title TEXT NOT NULL DEFAULT '', mode TEXT NOT NULL DEFAULT 'create',
			protocol_id TEXT NOT NULL DEFAULT '', seed_config TEXT NOT NULL DEFAULT '',
			draft_config TEXT NOT NULL DEFAULT '', draft_restore TEXT NOT NULL DEFAULT '',
			test_base_url TEXT NOT NULL DEFAULT '', test_api_key TEXT NOT NULL DEFAULT '',
			model_source_id TEXT NOT NULL DEFAULT '', model_name TEXT NOT NULL DEFAULT '',
			thinking_enabled INTEGER NOT NULL DEFAULT 0, thinking_effort TEXT NOT NULL DEFAULT '',
			plan_mode INTEGER NOT NULL DEFAULT 0, allow_live_test TEXT NOT NULL DEFAULT 'ask',
			allow_save TEXT NOT NULL DEFAULT 'ask', allow_delete TEXT NOT NULL DEFAULT 'ask',
			max_model_calls INTEGER NOT NULL DEFAULT 0, status TEXT NOT NULL DEFAULT 'idle',
			pending_action TEXT, plan_json TEXT NOT NULL DEFAULT '', plan_summary TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL, updated_at TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS agent_messages (id INTEGER PRIMARY KEY AUTOINCREMENT, session_id TEXT NOT NULL, seq INTEGER NOT NULL, role TEXT NOT NULL, content TEXT NOT NULL, model TEXT NOT NULL DEFAULT '', usage_json TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL, UNIQUE(session_id, seq))`,
		`CREATE INDEX IF NOT EXISTS idx_agent_messages_session ON agent_messages(session_id, seq)`,
		`CREATE INDEX IF NOT EXISTS idx_agent_sessions_updated_at ON agent_sessions(updated_at)`,
		`CREATE TABLE IF NOT EXISTS usage_rollup_hour (
			hour_ms INTEGER NOT NULL, model_name TEXT NOT NULL DEFAULT '', group_name TEXT NOT NULL DEFAULT '',
			key_name TEXT NOT NULL DEFAULT '', status_code INTEGER NOT NULL DEFAULT 0,
			cnt INTEGER NOT NULL DEFAULT 0, in_tok INTEGER NOT NULL DEFAULT 0, out_tok INTEGER NOT NULL DEFAULT 0,
			total_tok INTEGER NOT NULL DEFAULT 0, cache_tok INTEGER NOT NULL DEFAULT 0,
			cc_tok INTEGER NOT NULL DEFAULT 0, cc_rows INTEGER NOT NULL DEFAULT 0,
			dur_ms_sum INTEGER NOT NULL DEFAULT 0, fb_ms_sum INTEGER NOT NULL DEFAULT 0, fb_cnt INTEGER NOT NULL DEFAULT 0,
			min_started_ms INTEGER NOT NULL DEFAULT 0, max_started_ms INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (hour_ms, model_name, group_name, key_name, status_code)) WITHOUT ROWID`,
		`CREATE TABLE IF NOT EXISTS usage_rollup_state (key TEXT PRIMARY KEY, int_value INTEGER NOT NULL DEFAULT 0)`,
	}
	for _, stmt := range stmts {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	return s.initRollupState(ctx)
}

func backfillProgressBar(done, total int) string {
	const width = 30
	if total <= 0 || done < 0 {
		return ""
	}
	filled := min(done*width/total, width)
	return "[" + strings.Repeat("#", filled) + strings.Repeat(".", width-filled) + "]"
}

// ErrRollupBackfillInProgress prevents log resets from blocking the usage writer.
var ErrRollupBackfillInProgress = errors.New("rollup backfill in progress")
