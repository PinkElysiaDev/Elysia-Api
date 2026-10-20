package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bytedance/sonic"
	"github.com/elysia-api/backend/internal/dbpath"
	"github.com/elysia-api/backend/internal/storagequery"
)

const sqliteBusyTimeout = 5 * time.Second
const currentSchemaVersion = 2

func Open(path string) (*Store, error) {
	return OpenWithKey(path, nil)
}

// OpenWithKey 打开 SQLite store，并用给定 key 对落库的敏感字段
// （api token、上游 api_key）做透明 AES-256-GCM 加解密。
// key 为空时使用显式明文模式。
func OpenWithKey(path string, key []byte) (*Store, error) {
	return OpenWithLogPath(path, dbpath.DefaultLogPath(path), key)
}

func OpenWithLogPath(path, logPath string, key []byte) (*Store, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("database path is required")
	}
	if err := dbpath.Validate(path, logPath); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	dsn, err := sqliteDatabaseDSN(path, url.Values{
		"_synchronous": {"NORMAL"},
		"_pragma":      {"cache_size(-65536)"},
	})
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	codec, err := newSecretCodec(key)
	if err != nil {
		db.Close()
		return nil, err
	}
	store := &Store{db: db, codec: codec, path: path}
	if err := store.checkSchema(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	if err := store.init(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	store.LogStore, err = OpenLogStore(logPath)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("open log database: %w", err)
	}
	return store, nil
}

// Only a new database or this exact schema can be opened. No historical data is
// rewritten and unsupported databases are rejected before any schema writes.
func (s *Store) checkSchema(ctx context.Context) error {
	var version int
	if err := s.db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		return err
	}
	if version == currentSchemaVersion {
		return nil
	}
	if version == 0 {
		var tables int
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_schema WHERE type='table' AND name NOT LIKE 'sqlite_%'`).Scan(&tables); err != nil {
			return err
		}
		if tables == 0 {
			return nil
		}
	}
	return fmt.Errorf("unsupported database schema %d: use a new database with the current schema; automatic migrations are not supported", version)
}

func (s *Store) Close() error {
	s.closeOnce.Do(func() { s.closeErr = errors.Join(s.LogStore.Close(), s.db.Close()) })
	return s.closeErr
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
		"PRAGMA auto_vacuum=INCREMENTAL",
		"PRAGMA journal_mode=WAL",
	}
	for _, stmt := range pragmas {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("%s: %w", stmt, err)
		}
	}
	if err := s.createSchema(ctx); err != nil {
		return err
	}
	if err := s.createGenerationJobTables(ctx); err != nil {
		return err
	}
	if err := s.createConversionTables(ctx); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version=%d", currentSchemaVersion))
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
	return time.Time{}
}

func (s *Store) SetSetting(ctx context.Context, key string, value any) error {
	payload, err := sonic.ConfigStd.Marshal(value)
	if err != nil {
		return err
	}
	return storagequery.New(s.db).SetSetting(ctx, storagequery.SetSettingParams{Key: key, Value: string(payload), UpdatedAt: nowString()})
}

func (s *Store) GetSetting(ctx context.Context, key string, target any) (bool, error) {
	var payload string
	payload, err := storagequery.New(s.db).GetSetting(ctx, key)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, sonic.ConfigStd.Unmarshal([]byte(payload), target)
}

// scanAPIToken 是 api_tokens 行的统一映射（列表与按名查询共用）：
// 解密失败时明文清空（行级容错，见 decryptOrClear）。
func (s *Store) scanAPIToken(row interface{ Scan(dest ...any) error }) (APIToken, error) {
	var item APIToken
	var enabled int
	var allowedGroups, scopes, created, updated string
	if err := row.Scan(&item.Name, &item.Token, &enabled, &allowedGroups, &scopes, &created, &updated); err != nil {
		return APIToken{}, err
	}
	item.Token = s.decryptOrClear("api token", item.Name, item.Token)
	item.Enabled = sqlIntToBool(enabled)
	var err error
	if item.AllowedGroups, err = decodeStringSlice(allowedGroups); err != nil {
		return APIToken{}, fmt.Errorf("token %q allowed groups: %w", item.Name, err)
	}
	if item.Scopes, err = decodeStringSlice(scopes); err != nil {
		return APIToken{}, fmt.Errorf("token %q scopes: %w", item.Name, err)
	}
	item.CreatedAt = parseTime(created)
	item.UpdatedAt = parseTime(updated)
	return item, nil
}

func (s *Store) ListAPITokens(ctx context.Context) ([]APIToken, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT name, token, enabled, allowed_groups_json, scopes, created_at, updated_at FROM api_tokens ORDER BY name`)
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

// decodeStringSlice never turns malformed authorization data into unrestricted access.
func decodeStringSlice(raw string) ([]string, error) {
	var out []string
	if err := sonic.ConfigStd.Unmarshal([]byte(raw), &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Store) UpsertAPIToken(ctx context.Context, item APIToken) error {
	return s.writeAPIToken(ctx, s.db, item, true)
}

func (s *Store) writeAPIToken(ctx context.Context, executor interface {
	protocolSQLExecutor
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, item APIToken, isUpsert bool) error {
	if strings.TrimSpace(item.Name) == "" {
		return errors.New("token name is required")
	}
	// 去重检查：同一 token 值不允许配置到两个不同 name 上。用 SHA256 hash 走唯一索引快速判重。
	tokenHash := hashToken(item.Token)
	if tokenHash != "" {
		var existingName string
		err := executor.QueryRowContext(ctx, `SELECT name FROM api_tokens WHERE token_hash = ? AND name != ?`, tokenHash, item.Name).Scan(&existingName)
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
	item.Scopes = NormalizeScopes(item.Scopes)
	// 不变式：远程访问 Key（agent 作用域）的绑定组强制为 ["agent"]——
	// 纯展示值，判定与隔离都不依赖它（推理隔离在 /v1 鉴权层显式拒绝）。
	// 普通 Key 绑定真实存在的 "agent" 组完全合法，互不影响。
	if len(item.Scopes) > 0 {
		item.AllowedGroups = []string{TokenScopeAgent}
	}
	allowedGroups, err := sonic.ConfigStd.Marshal(item.AllowedGroups)
	if err != nil {
		return err
	}
	scopes, err := sonic.ConfigStd.Marshal(item.Scopes)
	if err != nil {
		return err
	}
	now := nowString()
	query := `INSERT INTO api_tokens(name, token, token_hash, enabled, allowed_groups_json, scopes, created_at, updated_at) VALUES(?, ?, ?, ?, ?, ?, ?, ?)`
	if isUpsert {
		query += ` ON CONFLICT(name) DO UPDATE SET token=excluded.token, token_hash=excluded.token_hash, enabled=excluded.enabled, allowed_groups_json=excluded.allowed_groups_json, scopes=excluded.scopes, updated_at=excluded.updated_at`
	}
	_, err = executor.ExecContext(ctx, query, item.Name, stored, tokenHash, sqlBoolToInt(item.Enabled), string(allowedGroups), string(scopes), now, now)
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
		`SELECT name, token, enabled, allowed_groups_json, scopes, created_at, updated_at FROM api_tokens WHERE name = ?`, name))
	if errors.Is(err, sql.ErrNoRows) {
		return APIToken{}, false, nil
	}
	if err != nil {
		return APIToken{}, false, err
	}
	return item, true, nil
}
