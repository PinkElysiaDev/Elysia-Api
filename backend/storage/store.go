package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
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
	ctx, cancel := context.WithCancel(context.Background())
	store := &Store{db: db, codec: codec, rollupCtx: ctx, rollupCancel: cancel}
	if err := store.init(context.Background()); err != nil {
		cancel()
		db.Close()
		return nil, err
	}
	return store, nil
}

func (s *Store) Close() error {
	s.rollupCancel()
	s.rollupWG.Wait()
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
		// GROUP BY 的临时 B-tree 进内存而非临时文件；大窗口聚合（日×模型分组）
		// 依赖临时结构，落盘会让大库统计明显变慢。
		"PRAGMA temp_store=MEMORY",
		// 64MiB 页缓存：大索引（idx_usage_agg_cover）扫描时热页驻留内存，
		// 避免反复从磁盘重读索引页。
		"PRAGMA cache_size=-65536",
	}
	for _, stmt := range pragmas {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("%s: %w", stmt, err)
		}
	}
	return s.migrate(ctx)
}

// addColumnIgnoreDup 执行幂等 ALTER：列已存在（duplicate column）视为成功。
// migrate 里的容错性列补齐全部走这一条路径。
func (s *Store) addColumnIgnoreDup(ctx context.Context, stmt string) error {
	_, err := s.db.ExecContext(ctx, stmt)
	if err != nil && strings.Contains(err.Error(), "duplicate column") {
		return nil
	}
	return err
}

func sqlBoolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func sqlIntToBool(v int) bool { return v != 0 }

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

// scanAPIToken 是 api_tokens 行的统一映射（列表与按名查询共用）：
// 解密失败时明文清空（行级容错，见 decryptOrClear）。
func (s *Store) scanAPIToken(row interface{ Scan(dest ...any) error }) (APIToken, error) {
	var item APIToken
	var enabled int
	var allowedGroups, created, updated string
	if err := row.Scan(&item.Name, &item.Token, &enabled, &allowedGroups, &created, &updated); err != nil {
		return APIToken{}, err
	}
	item.Token = s.decryptOrClear("api token", item.Name, item.Token)
	item.Enabled = sqlIntToBool(enabled)
	item.AllowedGroups = decodeStringSlice(allowedGroups)
	item.CreatedAt = parseTime(created)
	item.UpdatedAt = parseTime(updated)
	return item, nil
}

func (s *Store) ListAPITokens(ctx context.Context) ([]APIToken, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT name, token, enabled, allowed_groups_json, created_at, updated_at FROM api_tokens ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []APIToken{}
	for rows.Next() {
		item, err := s.scanAPIToken(rows)
		if err != nil {
			return nil, err
		}
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
			return fmt.Errorf("token already used by API key %q, choose a different value", existingName)
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
	_, err = s.db.ExecContext(ctx, `INSERT INTO api_tokens(name, token, token_hash, enabled, allowed_groups_json, created_at, updated_at) VALUES(?, ?, ?, ?, ?, ?, ?) ON CONFLICT(name) DO UPDATE SET token=excluded.token, token_hash=excluded.token_hash, enabled=excluded.enabled, allowed_groups_json=excluded.allowed_groups_json, updated_at=excluded.updated_at`, item.Name, stored, tokenHash, sqlBoolToInt(item.Enabled), string(allowedGroups), now, now)
	return err
}

func (s *Store) DeleteAPIToken(ctx context.Context, name string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM api_tokens WHERE name = ?`, name)
	return err
}

// FindAPITokenByName 按名称查找单个 token（含解密后的明文），
// 供「留空即不变」编辑时保留原 token 使用。
func (s *Store) FindAPITokenByName(ctx context.Context, name string) (APIToken, bool, error) {
	item, err := s.scanAPIToken(s.db.QueryRowContext(ctx,
		`SELECT name, token, enabled, allowed_groups_json, created_at, updated_at FROM api_tokens WHERE name = ?`, name))
	if errors.Is(err, sql.ErrNoRows) {
		return APIToken{}, false, nil
	}
	if err != nil {
		return APIToken{}, false, err
	}
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
