package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

func Open(path string) (*Store, error) {
	return OpenWithKey(path, nil)
}

// OpenWithKey 打开 SQLite store，并用给定 key 对落库的敏感字段
// （api token、上游 api_key）做透明 AES-256-GCM 加解密。
// key 为空时退化为明文模式（向后兼容旧库）。
func OpenWithKey(path string, key []byte) (*Store, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("database path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	codec, err := newSecretCodec(key)
	if err != nil {
		db.Close()
		return nil, err
	}
	store := &Store{db: db, codec: codec}
	if err := store.init(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	return store, nil
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// Ping 验证底层 SQLite 连接是否可用，供 /health 依赖探测使用。
func (s *Store) Ping(ctx context.Context) error {
	if s == nil || s.db == nil {
		return errors.New("store is not initialized")
	}
	return s.db.PingContext(ctx)
}

func (s *Store) init(ctx context.Context) error {
	pragmas := []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA busy_timeout=5000",
		"PRAGMA foreign_keys=ON",
		"PRAGMA synchronous=NORMAL",
	}
	for _, stmt := range pragmas {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("%s: %w", stmt, err)
		}
	}
	return s.migrate(ctx)
}

func (s *Store) migrate(ctx context.Context) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS settings (key TEXT PRIMARY KEY, value TEXT NOT NULL, updated_at TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS api_tokens (name TEXT PRIMARY KEY, token TEXT NOT NULL, enabled INTEGER NOT NULL DEFAULT 1, created_at TEXT NOT NULL, updated_at TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS model_sources (id TEXT PRIMARY KEY, name TEXT NOT NULL, base_url TEXT NOT NULL, api_key TEXT NOT NULL DEFAULT '', platform TEXT NOT NULL, enabled INTEGER NOT NULL DEFAULT 1, auto_fetch_models INTEGER NOT NULL DEFAULT 1, manual_models_json TEXT NOT NULL DEFAULT '[]', created_at TEXT NOT NULL, updated_at TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS models (id TEXT NOT NULL, source_id TEXT NOT NULL DEFAULT '', name TEXT NOT NULL, source_name TEXT NOT NULL DEFAULT '', base_url TEXT NOT NULL, api_key TEXT NOT NULL DEFAULT '', platform TEXT NOT NULL, type TEXT NOT NULL DEFAULT 'llm', max_tokens INTEGER NOT NULL DEFAULT 0, vision_capable INTEGER NOT NULL DEFAULT 0, tools_capable INTEGER NOT NULL DEFAULT 0, structured_output INTEGER NOT NULL DEFAULT 0, thinking_mode TEXT NOT NULL DEFAULT 'both', available INTEGER NOT NULL DEFAULT 1, last_checked_at TEXT NOT NULL, PRIMARY KEY (id, source_id))`,
		`CREATE TABLE IF NOT EXISTS model_groups (id TEXT PRIMARY KEY, name TEXT NOT NULL UNIQUE, enabled INTEGER NOT NULL DEFAULT 1, strategy TEXT NOT NULL DEFAULT 'round-robin', max_retries INTEGER NOT NULL DEFAULT 3, retry_interval INTEGER NOT NULL DEFAULT 1000, max_concurrency INTEGER NOT NULL DEFAULT 0, daily_limit_max_requests INTEGER NOT NULL DEFAULT 0, daily_limit_max_tokens INTEGER NOT NULL DEFAULT 0, type TEXT NOT NULL DEFAULT 'llm', max_tokens INTEGER NOT NULL DEFAULT 0, vision_capable INTEGER NOT NULL DEFAULT 0, tools_capable INTEGER NOT NULL DEFAULT 0, created_at TEXT NOT NULL, updated_at TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS model_group_models (group_id TEXT NOT NULL, model_id TEXT NOT NULL, source_id TEXT NOT NULL DEFAULT '', position INTEGER NOT NULL DEFAULT 0, PRIMARY KEY (group_id, model_id, source_id), FOREIGN KEY(group_id) REFERENCES model_groups(id) ON DELETE CASCADE)`,
		`CREATE TABLE IF NOT EXISTS usage_records (request_id TEXT PRIMARY KEY, started_at TEXT NOT NULL, ended_at TEXT NOT NULL, key_name TEXT NOT NULL DEFAULT '', key_hash TEXT NOT NULL DEFAULT '', requested_model_group TEXT NOT NULL DEFAULT '', group_id TEXT NOT NULL DEFAULT '', group_name TEXT NOT NULL DEFAULT '', model_id TEXT NOT NULL DEFAULT '', model_name TEXT NOT NULL DEFAULT '', platform TEXT NOT NULL DEFAULT '', source_format TEXT NOT NULL DEFAULT '', target_format TEXT NOT NULL DEFAULT '', relay_mode TEXT NOT NULL DEFAULT '', responses_mode TEXT NOT NULL DEFAULT '', usage_source TEXT NOT NULL DEFAULT '', stream INTEGER NOT NULL DEFAULT 0, status_code INTEGER NOT NULL DEFAULT 0, error TEXT NOT NULL DEFAULT '', first_byte_ms INTEGER NOT NULL DEFAULT 0, duration_ms INTEGER NOT NULL DEFAULT 0, input_tokens INTEGER NOT NULL DEFAULT 0, output_tokens INTEGER NOT NULL DEFAULT 0, total_tokens INTEGER NOT NULL DEFAULT 0, request_truncated INTEGER NOT NULL DEFAULT 0, response_truncated INTEGER NOT NULL DEFAULT 0, record_json TEXT NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS idx_usage_started_at ON usage_records(started_at)`,
		`CREATE INDEX IF NOT EXISTS idx_usage_group ON usage_records(group_name)`,
		`CREATE INDEX IF NOT EXISTS idx_usage_model ON usage_records(model_name)`,
		`CREATE TABLE IF NOT EXISTS system_logs (id INTEGER PRIMARY KEY AUTOINCREMENT, created_at TEXT NOT NULL, level TEXT NOT NULL, message TEXT NOT NULL, fields_json TEXT NOT NULL DEFAULT '{}')`,
		`CREATE INDEX IF NOT EXISTS idx_system_logs_created_at ON system_logs(created_at)`,
	}
	for _, stmt := range stmts {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	// 增量迁移：为 api_tokens 增加 allowed_groups_json 列（模型组级访问权限）。
	// SQLite 无 ADD COLUMN IF NOT EXISTS，重复执行会报 duplicate column，忽略该错误即幂等。
	if _, err := s.db.ExecContext(ctx, `ALTER TABLE api_tokens ADD COLUMN allowed_groups_json TEXT NOT NULL DEFAULT '[]'`); err != nil &&
		!strings.Contains(err.Error(), "duplicate column") {
		return err
	}
	// 增量迁移：为 api_tokens 增加 token_hash 列（SHA256 哈希，用于去重检查）。
	// 空 hash 不参与唯一约束，兼容历史数据过渡期（旧数据 hash 为空，下次编辑时补齐）。
	if _, err := s.db.ExecContext(ctx, `ALTER TABLE api_tokens ADD COLUMN token_hash TEXT NOT NULL DEFAULT ''`); err != nil &&
		!strings.Contains(err.Error(), "duplicate column") {
		return err
	}
	// 为 token_hash 建唯一索引（WHERE token_hash != '' 保证空值不参与约束）。
	if _, err := s.db.ExecContext(ctx, `CREATE UNIQUE INDEX IF NOT EXISTS idx_api_tokens_hash ON api_tokens(token_hash) WHERE token_hash != ''`); err != nil {
		return err
	}
	// 增量迁移：为 usage_records 增加 cache_hit_tokens 列（缓存命中 token 数）。
	// 用于统计接口直接 SUM 出缓存命中量与命中率，免去逐条解析 record_json。
	// 历史数据该列为 0（可接受：旧记录缓存命中量不再回填）。
	if _, err := s.db.ExecContext(ctx, `ALTER TABLE usage_records ADD COLUMN cache_hit_tokens INTEGER NOT NULL DEFAULT 0`); err != nil &&
		!strings.Contains(err.Error(), "duplicate column") {
		return err
	}
	// 回填历史数据的 token_hash：查所有 hash 为空的行，解密 → 计算 SHA256 → UPDATE。
	// 解密失败（极端情况：master key 变了）跳过该行并记日志。
	//
	// 重要：store 用 SetMaxOpenConns(1)（单连接）。必须先把待回填的行全部读进内存
	// 并关闭游标，再做 UPDATE/后续 Exec——否则未关闭的 rows 一直占着唯一连接，
	// 循环内的 ExecContext 永远拿不到连接，导致死锁（即使 0 行，defer 的 Close
	// 也会拖到函数末尾，使后面的 Exec 死锁）。
	type tokenRow struct{ name, encryptedToken string }
	var pending []tokenRow
	rows, err := s.db.QueryContext(ctx, `SELECT name, token FROM api_tokens WHERE token_hash = ''`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var r tokenRow
		if err := rows.Scan(&r.name, &r.encryptedToken); err != nil {
			rows.Close()
			return err
		}
		pending = append(pending, r)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close() // 必须在任何后续 Exec 前释放连接

	for _, r := range pending {
		plaintext, derr := s.codec.decrypt(r.encryptedToken)
		if derr != nil {
			log.Printf("[token_hash backfill] failed to decrypt token %q: %v (skipped)", r.name, derr)
			continue
		}
		hash := hashToken(plaintext)
		if _, err := s.db.ExecContext(ctx, `UPDATE api_tokens SET token_hash = ? WHERE name = ?`, hash, r.name); err != nil {
			log.Printf("[token_hash backfill] failed to update hash for %q: %v", r.name, err)
		}
	}
	_, err = s.db.ExecContext(ctx, `INSERT OR IGNORE INTO schema_migrations(version, applied_at) VALUES(1, ?)`, time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func intBool(v int) bool { return v != 0 }

func nowString() string { return time.Now().UTC().Format(time.RFC3339Nano) }

func parseTime(raw string) time.Time {
	if t, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		return t
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t
	}
	return time.Time{}
}

func (s *Store) SetSetting(ctx context.Context, key string, value any) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO settings(key, value, updated_at) VALUES(?, ?, ?) ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at`, key, string(payload), nowString())
	return err
}

func (s *Store) GetSetting(ctx context.Context, key string, target any) (bool, error) {
	var payload string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, json.Unmarshal([]byte(payload), target)
}

func (s *Store) ListAPITokens(ctx context.Context) ([]APIToken, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT name, token, enabled, allowed_groups_json, created_at, updated_at FROM api_tokens ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []APIToken{}
	for rows.Next() {
		var item APIToken
		var enabled int
		var allowedGroups, created, updated string
		if err := rows.Scan(&item.Name, &item.Token, &enabled, &allowedGroups, &created, &updated); err != nil {
			return nil, err
		}
		if plain, err := s.codec.decrypt(item.Token); err == nil {
			item.Token = plain
		} else {
			return nil, err
		}
		item.Enabled = intBool(enabled)
		item.AllowedGroups = decodeStringSlice(allowedGroups)
		item.CreatedAt = parseTime(created)
		item.UpdatedAt = parseTime(updated)
		items = append(items, item)
	}
	return items, rows.Err()
}

// decodeStringSlice 解析 allowed_groups_json 等 JSON 字符串数组列，
// 解析失败或为空时返回空切片（语义：不限制）。
func decodeStringSlice(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return []string{}
	}
	var out []string
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return []string{}
	}
	return out
}

func (s *Store) UpsertAPIToken(ctx context.Context, item APIToken) error {
	if strings.TrimSpace(item.Name) == "" {
		return errors.New("token name is required")
	}
	// 去重检查：同一 token 值不允许配置到两个不同 name 上。用 SHA256 hash 走唯一索引快速判重。
	tokenHash := hashToken(item.Token)
	if tokenHash != "" {
		var existingName string
		err := s.db.QueryRowContext(ctx, `SELECT name FROM api_tokens WHERE token_hash = ? AND name != ?`, tokenHash, item.Name).Scan(&existingName)
		if err == nil {
			return fmt.Errorf("该 token 已被 API Key %q 使用，请更换", existingName)
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
	}
	stored, err := s.codec.encrypt(item.Token)
	if err != nil {
		return err
	}
	if item.AllowedGroups == nil {
		item.AllowedGroups = []string{}
	}
	allowedGroups, err := json.Marshal(item.AllowedGroups)
	if err != nil {
		return err
	}
	now := nowString()
	_, err = s.db.ExecContext(ctx, `INSERT INTO api_tokens(name, token, token_hash, enabled, allowed_groups_json, created_at, updated_at) VALUES(?, ?, ?, ?, ?, ?, ?) ON CONFLICT(name) DO UPDATE SET token=excluded.token, token_hash=excluded.token_hash, enabled=excluded.enabled, allowed_groups_json=excluded.allowed_groups_json, updated_at=excluded.updated_at`, item.Name, stored, tokenHash, boolInt(item.Enabled), string(allowedGroups), now, now)
	return err
}

func (s *Store) DeleteAPIToken(ctx context.Context, name string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM api_tokens WHERE name = ?`, name)
	return err
}

// FindAPITokenByName 按名称查找单个 token（含解密后的明文），
// 供「留空即不变」编辑时保留原 token 使用。
func (s *Store) FindAPITokenByName(ctx context.Context, name string) (APIToken, bool, error) {
	var item APIToken
	var enabled int
	var allowedGroups, created, updated string
	err := s.db.QueryRowContext(ctx, `SELECT name, token, enabled, allowed_groups_json, created_at, updated_at FROM api_tokens WHERE name = ?`, name).
		Scan(&item.Name, &item.Token, &enabled, &allowedGroups, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return APIToken{}, false, nil
	}
	if err != nil {
		return APIToken{}, false, err
	}
	if plain, derr := s.codec.decrypt(item.Token); derr == nil {
		item.Token = plain
	} else {
		return APIToken{}, false, derr
	}
	item.Enabled = intBool(enabled)
	item.AllowedGroups = decodeStringSlice(allowedGroups)
	item.CreatedAt = parseTime(created)
	item.UpdatedAt = parseTime(updated)
	return item, true, nil
}

// FindAPIToken 按明文 token 查找。由于 token 以随机 nonce 加密存储，
// 无法用 SQL 等值查询，改为遍历解密后比对。注意：服务端热路径已由
// 内存缓存（持解密后的 token）承担，这里仅作回退/非热路径使用。
func (s *Store) FindAPIToken(ctx context.Context, token string) (APIToken, bool, error) {
	items, err := s.ListAPITokens(ctx)
	if err != nil {
		return APIToken{}, false, err
	}
	for _, item := range items {
		if item.Enabled && subtleConstantTimeEqual(item.Token, token) {
			return item, true, nil
		}
	}
	return APIToken{}, false, nil
}

func (s *Store) ListSources(ctx context.Context) ([]ModelSource, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, base_url, api_key, platform, enabled, auto_fetch_models, manual_models_json, created_at, updated_at FROM model_sources ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []ModelSource{}
	for rows.Next() {
		var item ModelSource
		var enabled, autoFetch int
		var manual, created, updated string
		if err := rows.Scan(&item.ID, &item.Name, &item.BaseURL, &item.APIKey, &item.Platform, &enabled, &autoFetch, &manual, &created, &updated); err != nil {
			return nil, err
		}
		item.Enabled = intBool(enabled)
		item.AutoFetchModels = intBool(autoFetch)
		item.CreatedAt = parseTime(created)
		item.UpdatedAt = parseTime(updated)
		if plain, err := s.codec.decrypt(item.APIKey); err == nil {
			item.APIKey = plain
		} else {
			return nil, err
		}
		_ = json.Unmarshal([]byte(manual), &item.ManualModels)
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) UpsertSource(ctx context.Context, item ModelSource) error {
	if strings.TrimSpace(item.ID) == "" {
		return errors.New("source id is required")
	}
	manual, err := json.Marshal(item.ManualModels)
	if err != nil {
		return err
	}
	storedKey, err := s.codec.encrypt(item.APIKey)
	if err != nil {
		return err
	}
	now := nowString()
	_, err = s.db.ExecContext(ctx, `INSERT INTO model_sources(id, name, base_url, api_key, platform, enabled, auto_fetch_models, manual_models_json, created_at, updated_at) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT(id) DO UPDATE SET name=excluded.name, base_url=excluded.base_url, api_key=excluded.api_key, platform=excluded.platform, enabled=excluded.enabled, auto_fetch_models=excluded.auto_fetch_models, manual_models_json=excluded.manual_models_json, updated_at=excluded.updated_at`, item.ID, item.Name, item.BaseURL, storedKey, item.Platform, boolInt(item.Enabled), boolInt(item.AutoFetchModels), string(manual), now, now)
	return err
}

func (s *Store) DeleteSource(ctx context.Context, id string) error {
	// 事务化：删除模型源、其下模型以及组内对该源模型的引用必须原子完成，
	// 避免第二步失败留下孤儿模型或组内残留旧模型引用。
	// （models / model_group_models 表对 model_sources 无 FK 级联）。
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM model_sources WHERE id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM models WHERE source_id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM model_group_models WHERE source_id = ?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ReplaceSourceModels(ctx context.Context, source ModelSource, models []Model) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM models WHERE source_id = ?`, source.ID); err != nil {
		return err
	}
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO models(id, source_id, name, source_name, base_url, api_key, platform, type, max_tokens, vision_capable, tools_capable, structured_output, thinking_mode, available, last_checked_at) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	checked := nowString()
	storedKey, err := s.codec.encrypt(source.APIKey)
	if err != nil {
		return err
	}
	for _, model := range models {
		if model.Type == "" {
			model.Type = "llm"
		}
		if model.ThinkingMode == "" {
			model.ThinkingMode = "both"
		}
		if model.Name == "" {
			model.Name = model.ID
		}
		if _, err := stmt.ExecContext(ctx, model.ID, source.ID, model.Name, source.Name, source.BaseURL, storedKey, normalizePlatform(source.Platform), model.Type, model.MaxTokens, boolInt(model.VisionCapable), boolInt(model.ToolsCapable), boolInt(model.StructuredOutput), model.ThinkingMode, boolInt(true), checked); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func normalizePlatform(platform string) string {
	if platform == "openai-compatible" {
		return "openai"
	}
	return platform
}
