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

func (s *Store) migrate(ctx context.Context) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS settings (key TEXT PRIMARY KEY, value TEXT NOT NULL, updated_at TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS api_tokens (name TEXT PRIMARY KEY, token TEXT NOT NULL, enabled INTEGER NOT NULL DEFAULT 1, created_at TEXT NOT NULL, updated_at TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS model_sources (id TEXT PRIMARY KEY, name TEXT NOT NULL, base_url TEXT NOT NULL, api_key TEXT NOT NULL DEFAULT '', platform TEXT NOT NULL, enabled INTEGER NOT NULL DEFAULT 1, auto_fetch_models INTEGER NOT NULL DEFAULT 1, manual_models_json TEXT NOT NULL DEFAULT '[]', fetch_base_url TEXT NOT NULL DEFAULT '', api_keys TEXT NOT NULL DEFAULT '', key_strategy TEXT NOT NULL DEFAULT 'single', created_at TEXT NOT NULL, updated_at TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS models (id TEXT NOT NULL, source_id TEXT NOT NULL DEFAULT '', name TEXT NOT NULL, source_name TEXT NOT NULL DEFAULT '', base_url TEXT NOT NULL, api_key TEXT NOT NULL DEFAULT '', platform TEXT NOT NULL, type TEXT NOT NULL DEFAULT 'llm', max_tokens INTEGER NOT NULL DEFAULT 0, vision_capable INTEGER NOT NULL DEFAULT 0, tools_capable INTEGER NOT NULL DEFAULT 0, structured_output INTEGER NOT NULL DEFAULT 0, thinking_mode TEXT NOT NULL DEFAULT 'both', available INTEGER NOT NULL DEFAULT 1, enabled INTEGER NOT NULL DEFAULT 1, origin TEXT NOT NULL DEFAULT 'fetched', capability_source TEXT NOT NULL DEFAULT '', last_checked_at TEXT NOT NULL, PRIMARY KEY (id, source_id))`,
		`CREATE TABLE IF NOT EXISTS model_groups (id TEXT PRIMARY KEY, name TEXT NOT NULL UNIQUE, enabled INTEGER NOT NULL DEFAULT 1, strategy TEXT NOT NULL DEFAULT 'round-robin', max_retries INTEGER NOT NULL DEFAULT 3, retry_interval INTEGER NOT NULL DEFAULT 1000, max_concurrency INTEGER NOT NULL DEFAULT 0, daily_limit_max_requests INTEGER NOT NULL DEFAULT 0, daily_limit_max_tokens INTEGER NOT NULL DEFAULT 0, type TEXT NOT NULL DEFAULT 'llm', max_tokens INTEGER NOT NULL DEFAULT 0, vision_capable INTEGER NOT NULL DEFAULT 0, tools_capable INTEGER NOT NULL DEFAULT 0, created_at TEXT NOT NULL, updated_at TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS model_group_models (group_id TEXT NOT NULL, model_id TEXT NOT NULL, source_id TEXT NOT NULL DEFAULT '', position INTEGER NOT NULL DEFAULT 0, PRIMARY KEY (group_id, model_id, source_id), FOREIGN KEY(group_id) REFERENCES model_groups(id) ON DELETE CASCADE)`,
		`CREATE TABLE IF NOT EXISTS usage_records (request_id TEXT PRIMARY KEY, started_at TEXT NOT NULL, ended_at TEXT NOT NULL, key_name TEXT NOT NULL DEFAULT '', key_hash TEXT NOT NULL DEFAULT '', requested_model_group TEXT NOT NULL DEFAULT '', group_id TEXT NOT NULL DEFAULT '', group_name TEXT NOT NULL DEFAULT '', model_id TEXT NOT NULL DEFAULT '', model_name TEXT NOT NULL DEFAULT '', platform TEXT NOT NULL DEFAULT '', source_format TEXT NOT NULL DEFAULT '', target_format TEXT NOT NULL DEFAULT '', relay_mode TEXT NOT NULL DEFAULT '', responses_mode TEXT NOT NULL DEFAULT '', usage_source TEXT NOT NULL DEFAULT '', stream INTEGER NOT NULL DEFAULT 0, status_code INTEGER NOT NULL DEFAULT 0, error TEXT NOT NULL DEFAULT '', first_byte_ms INTEGER NOT NULL DEFAULT 0, duration_ms INTEGER NOT NULL DEFAULT 0, input_tokens INTEGER NOT NULL DEFAULT 0, output_tokens INTEGER NOT NULL DEFAULT 0, total_tokens INTEGER NOT NULL DEFAULT 0, request_truncated INTEGER NOT NULL DEFAULT 0, response_truncated INTEGER NOT NULL DEFAULT 0, record_json TEXT NOT NULL)`,
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
	// 增量迁移：usage_records 增加 started_ms（Unix 毫秒整型）列。
	// started_at 是 RFC3339Nano 字符串，格式化会去掉小数尾零，整秒时间戳
	// （…T00:00:00Z）与带毫秒的时间戳（…T00:00:00.123Z）按字符串比较时
	// '.'(0x2E) < 'Z'(0x5A)，导致整秒边界的时间过滤漏记录、同秒内排序错乱。
	// 时间过滤与排序改用整型列；started_at 保留用于展示。
	if _, err := s.db.ExecContext(ctx, `ALTER TABLE usage_records ADD COLUMN started_ms INTEGER NOT NULL DEFAULT 0`); err != nil &&
		!strings.Contains(err.Error(), "duplicate column") {
		return err
	}
	// 聚合覆盖索引：统计 KPI（UsageTotals）、按模型分组（UsageByModel）与按日趋势
	// （UsageDaily）的全部过滤与聚合列都在索引内——index-only 扫描，不回表读取
	// 含 record_json（完整请求/响应体，单行可达几十 KB）的胖行。月级数据的聚合
	// 从 GB 级行读取降为几十 MB 索引扫描。列全为整数/短字符串，空间开销可控；
	// 幂等建索引，大表首次执行为一次性启动成本。
	if _, err := s.db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_usage_agg_cover ON usage_records(started_ms, model_name, group_name, key_name, status_code, stream, input_tokens, output_tokens, total_tokens, cache_hit_tokens, duration_ms, first_byte_ms)`); err != nil {
		return err
	}
	// 旧的时间索引成为覆盖索引前缀的冗余（写入双份维护），删除。
	if _, err := s.db.ExecContext(ctx, `DROP INDEX IF EXISTS idx_usage_started_ms`); err != nil {
		return err
	}
	// 遗留单列索引（started_at / group_name / model_name）是带筛选查询的性能
	// 陷阱：无 ANALYZE 统计时，规划器会把 model_name IN / group_name = 等筛选
	// 引到单列索引上，随后 ORDER BY started_ms 走临时 B-tree 排序、聚合逐行
	// 回表读取 record_json 胖行（大库上带筛选查询几十秒的根因）。覆盖索引的
	// 列已覆盖全部筛选/聚合/排序形态，删除后每次写入也少维护 3 个索引。
	for _, stmt := range []string{
		`DROP INDEX IF EXISTS idx_usage_started_at`,
		`DROP INDEX IF EXISTS idx_usage_group`,
		`DROP INDEX IF EXISTS idx_usage_model`,
	} {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	// 回填存量 started_ms（详见 backfillStartedMs）。
	if err := s.backfillStartedMs(ctx); err != nil {
		return err
	}
	// 小时级 rollup 预聚合表（详见 migrateRollupTables）。
	if err := s.migrateRollupTables(ctx); err != nil {
		return err
	}
	// 增量迁移（幂等，duplicate column 忽略）：
	//   model_sources.fetch_base_url —— 模型列表拉取专用地址（空=与 base_url 一致）；
	//   model_sources.api_keys / key_strategy —— 多 Key 配置与调度策略；
	//   models.enabled —— 用户手动启停（与 available 健康位分离）；
	//   models.origin —— 行来源（fetched 随刷新合并替换 / manual 刷新永不触碰）；
	//   models.capability_source —— 能力字段填充来源（''/catalog/manual，
	//     manual 的用户修改在刷新时保留，catalog 值随刷新更新）。
	for _, stmt := range []string{
		`ALTER TABLE model_sources ADD COLUMN fetch_base_url TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE model_sources ADD COLUMN api_keys TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE model_sources ADD COLUMN key_strategy TEXT NOT NULL DEFAULT 'single'`,
		`ALTER TABLE models ADD COLUMN enabled INTEGER NOT NULL DEFAULT 1`,
		`ALTER TABLE models ADD COLUMN origin TEXT NOT NULL DEFAULT 'fetched'`,
		`ALTER TABLE models ADD COLUMN capability_source TEXT NOT NULL DEFAULT ''`,
	} {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil &&
			!strings.Contains(err.Error(), "duplicate column") {
			return err
		}
	}
	if err := s.migrateMaheshvaraLabels(ctx); err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT OR IGNORE INTO schema_migrations(version, applied_at) VALUES(1, ?)`, time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

// backfillProgressBar 渲染等宽字符进度条。只用 ASCII 的 '#' 与 '.'，
// 避免宽字符进度条在部分 Windows 控制台代码页下乱码。
// backfillStartedMs 为存量 usage_records 行回填 started_ms（一次性迁移）。
func (s *Store) backfillStartedMs(ctx context.Context) error {
	// 回填存量行的 started_ms。单连接约束：先全部读进内存并关闭游标，再 UPDATE。
	type usageRow struct{ requestID, startedAt string }
	var pendingUsage []usageRow
	usageRows, err := s.db.QueryContext(ctx, `SELECT request_id, started_at FROM usage_records WHERE started_ms = 0`)
	if err != nil {
		return err
	}
	for usageRows.Next() {
		var r usageRow
		if err := usageRows.Scan(&r.requestID, &r.startedAt); err != nil {
			usageRows.Close()
			return err
		}
		pendingUsage = append(pendingUsage, r)
	}
	if err := usageRows.Err(); err != nil {
		usageRows.Close()
		return err
	}
	usageRows.Close()

	if len(pendingUsage) > 0 {
		// 大表回填可能耗时，先明确告知用户这是升级过程中的一次性迁移。
		log.Printf("[migration] usage_records: backfilling started_ms for %d rows — one-time upgrade, please wait", len(pendingUsage))
		backfillStartedAt := time.Now()
		// 逐行自动提交会在大表上造成每行一次 fsync（usage 记录含 record_json
		// 大字段，整行重写放大严重），改为分批事务 + 预编译语句：每批一次提交，
		// 速度提升数百倍；中途失败时已提交批次保留，下次启动仅补剩余行（幂等）。
		const backfillBatchSize = 2000
		var tx *sql.Tx
		var stmt *sql.Stmt
		closeBatch := func() error {
			if stmt != nil {
				_ = stmt.Close()
				stmt = nil
			}
			if tx != nil {
				if err := tx.Commit(); err != nil {
					tx = nil
					return err
				}
				tx = nil
			}
			return nil
		}
		for index, r := range pendingUsage {
			if tx == nil {
				tx, err = s.db.BeginTx(ctx, nil)
				if err != nil {
					return err
				}
				stmt, err = tx.PrepareContext(ctx, `UPDATE usage_records SET started_ms = ? WHERE request_id = ?`)
				if err != nil {
					rollbackErr := tx.Rollback()
					tx, stmt = nil, nil
					if err != nil {
						return err
					}
					return rollbackErr
				}
			}
			parsed, perr := time.Parse(time.RFC3339Nano, r.startedAt)
			if perr != nil {
				// 无法解析的旧行保持 0；写入侧全部走 RFC3339Nano，实际不应出现。
				log.Printf("[migration] usage_records: unparseable started_at %q for %q (skipped)", r.startedAt, r.requestID)
			} else if _, err = stmt.ExecContext(ctx, parsed.UnixMilli(), r.requestID); err != nil {
				_ = tx.Rollback()
				return fmt.Errorf("backfill started_ms for %q: %w", r.requestID, err)
			}
			if (index+1)%backfillBatchSize == 0 || index+1 == len(pendingUsage) {
				if err = closeBatch(); err != nil {
					return err
				}
				done := index + 1
				log.Printf("[migration] usage_records: %s %d%% (%d/%d rows, %.1fs elapsed)",
					backfillProgressBar(done, len(pendingUsage)), done*100/len(pendingUsage), done, len(pendingUsage),
					time.Since(backfillStartedAt).Seconds())
			}
		}
		if err = closeBatch(); err != nil {
			return err
		}
		log.Printf("[migration] usage_records: started_ms backfill complete in %s", time.Since(backfillStartedAt).Round(time.Millisecond))
	}
	return nil
}

// migrateRollupTables 创建小时级预聚合表并初始化状态（设计见 rollup.go）。
func (s *Store) migrateRollupTables(ctx context.Context) error {
	// 小时级 rollup 预聚合表 + 状态表（rollup.go）：仪表盘聚合与原始表大小
	// 解耦。只新增、不改任何现有表/列——usage_records 数据零风险，两张新表
	// 均为纯派生数据，可随时删除重建。WITHOUT ROWID 让 PK 即表结构，
	// hour_ms 范围扫描与 UPSERT 都走主键。
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS usage_rollup_hour (
		hour_ms INTEGER NOT NULL,
		model_name TEXT NOT NULL DEFAULT '',
		group_name TEXT NOT NULL DEFAULT '',
		key_name TEXT NOT NULL DEFAULT '',
		status_code INTEGER NOT NULL DEFAULT 0,
		cnt INTEGER NOT NULL DEFAULT 0,
		in_tok INTEGER NOT NULL DEFAULT 0,
		out_tok INTEGER NOT NULL DEFAULT 0,
		total_tok INTEGER NOT NULL DEFAULT 0,
		cache_tok INTEGER NOT NULL DEFAULT 0,
		dur_ms_sum INTEGER NOT NULL DEFAULT 0,
		fb_ms_sum INTEGER NOT NULL DEFAULT 0,
		fb_cnt INTEGER NOT NULL DEFAULT 0,
		min_started_ms INTEGER NOT NULL DEFAULT 0,
		max_started_ms INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY (hour_ms, model_name, group_name, key_name, status_code)
	) WITHOUT ROWID`); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS usage_rollup_state (key TEXT PRIMARY KEY, int_value INTEGER NOT NULL DEFAULT 0)`); err != nil {
		return err
	}
	if err := s.initRollupState(ctx); err != nil {
		return err
	}
	return nil
}

func backfillProgressBar(done, total int) string {
	const width = 30
	if total <= 0 || done < 0 {
		return ""
	}
	filled := done * width / total
	if filled > width {
		filled = width
	}
	return "[" + strings.Repeat("#", filled) + strings.Repeat(".", width-filled) + "]"
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
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, base_url, api_key, platform, enabled, auto_fetch_models, manual_models_json, fetch_base_url, api_keys, key_strategy, created_at, updated_at FROM model_sources ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []ModelSource{}
	for rows.Next() {
		var item ModelSource
		var enabled, autoFetch int
		var manual, fetchBase, storedKeys, strategy, created, updated string
		if err := rows.Scan(&item.ID, &item.Name, &item.BaseURL, &item.APIKey, &item.Platform, &enabled, &autoFetch, &manual, &fetchBase, &storedKeys, &strategy, &created, &updated); err != nil {
			return nil, err
		}
		item.Enabled = intBool(enabled)
		item.AutoFetchModels = intBool(autoFetch)
		item.FetchBaseURL = fetchBase
		item.KeyStrategy = SourceKeyStrategy(strategy)
		item.CreatedAt = parseTime(created)
		item.UpdatedAt = parseTime(updated)
		if plain, err := s.codec.decrypt(item.APIKey); err == nil {
			item.APIKey = plain
		} else {
			return nil, err
		}
		_ = json.Unmarshal([]byte(manual), &item.ManualModels)
		if storedKeys != "" {
			if plain, err := s.codec.decrypt(storedKeys); err == nil {
				_ = json.Unmarshal([]byte(plain), &item.APIKeys)
			} else {
				return nil, err
			}
		}
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
	// api_keys 为 JSON 数组整体加密存储；空列表落空串（单 key 模式）。
	storedKeys := ""
	if len(item.APIKeys) > 0 {
		payload, err := json.Marshal(item.APIKeys)
		if err != nil {
			return err
		}
		if storedKeys, err = s.codec.encrypt(string(payload)); err != nil {
			return err
		}
	}
	strategy := string(item.KeyStrategy)
	if strategy == "" {
		strategy = string(KeyStrategySingle)
	}
	now := nowString()
	_, err = s.db.ExecContext(ctx, `INSERT INTO model_sources(id, name, base_url, api_key, platform, enabled, auto_fetch_models, manual_models_json, fetch_base_url, api_keys, key_strategy, created_at, updated_at) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT(id) DO UPDATE SET name=excluded.name, base_url=excluded.base_url, api_key=excluded.api_key, platform=excluded.platform, enabled=excluded.enabled, auto_fetch_models=excluded.auto_fetch_models, manual_models_json=excluded.manual_models_json, fetch_base_url=excluded.fetch_base_url, api_keys=excluded.api_keys, key_strategy=excluded.key_strategy, updated_at=excluded.updated_at`, item.ID, item.Name, item.BaseURL, storedKey, item.Platform, boolInt(item.Enabled), boolInt(item.AutoFetchModels), string(manual), item.FetchBaseURL, storedKeys, strategy, now, now)
	return err
}

// UpdateSourceAPIKeys 定向更新某个源的 key 列表（加密整列重写，不碰其他字段）。
// 供逐 key 拉取后回写 fetchedModels/allowedModels 使用——避免为了改 key 元数据
// 而走整源 Upsert 与用户编辑产生竞争。
func (s *Store) UpdateSourceAPIKeys(ctx context.Context, sourceID string, keys []SourceAPIKey) error {
	payload, err := json.Marshal(keys)
	if err != nil {
		return err
	}
	stored, err := s.codec.encrypt(string(payload))
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `UPDATE model_sources SET api_keys = ?, updated_at = ? WHERE id = ?`, stored, nowString(), sourceID)
	return err
}

// UpdateSourceEnabled 仅更新源的启停开关（方向：源级轻量 PATCH）。
// 与整源 Upsert 不同：不触发「保存后自动同步模型」之类的副作用，也不触碰
// key/模型数据——启停与模型列表无关，重拉上游纯属多余（可能触发限流）。
func (s *Store) UpdateSourceEnabled(ctx context.Context, id string, enabled bool) (bool, error) {
	res, err := s.db.ExecContext(ctx, `UPDATE model_sources SET enabled = ?, updated_at = ? WHERE id = ?`, boolInt(enabled), nowString(), id)
	if err != nil {
		return false, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected > 0, nil
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
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO models(id, source_id, name, source_name, base_url, api_key, platform, type, max_tokens, vision_capable, tools_capable, structured_output, thinking_mode, available, enabled, origin, capability_source, last_checked_at) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	checked := nowString()
	// models.api_key 冗余列写入首个有效 key：热路径已改用源级 key 集合（方向6），
	// 该列仅供健康检测等遗留消费方回退，避免空 key 探测必败。
	storedKey, err := s.codec.encrypt(firstEffectiveKey(source))
	if err != nil {
		return err
	}
	for _, model := range models {
		model = normalizeModelDefaults(model)
		// platform 优先取模型自带值（legacy 配置导入的模型可逐模型声明平台，
		// 如 responses/gemini），为空才回落源级平台（自动拉取的模型两者一致）。
		platform := model.Platform
		if strings.TrimSpace(platform) == "" {
			platform = source.Platform
		}
		if _, err := stmt.ExecContext(ctx, model.ID, source.ID, model.Name, source.Name, model.BaseURL, storedKey, normalizePlatform(platform), model.Type, model.MaxTokens, boolInt(model.VisionCapable), boolInt(model.ToolsCapable), boolInt(model.StructuredOutput), model.ThinkingMode, boolInt(true), boolInt(model.Enabled), model.Origin, model.CapabilitySource, checked); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// firstEffectiveKey 返回源的首个有效 key（多 key 时取第一个启用项，单 key 即 APIKey），
// 供 models.api_key 冗余列的向后兼容写入。
func firstEffectiveKey(source ModelSource) string {
	for _, key := range source.EffectiveKeys() {
		return key.Value
	}
	return ""
}

func normalizePlatform(platform string) string {
	if platform == "openai-compatible" {
		return "openai"
	}
	return platform
}

// ModelMergeResult 汇总一次刷新合并的变更，供前端展示「新增 N / 移除 M」。
type ModelMergeResult struct {
	Added   []string `json:"added"`
	Removed []string `json:"removed"`
}

// MergeSourceModels 用新拉取（或手动同步）的模型列表合并进该源的模型表，
// 替代旧的全删全插（ReplaceSourceModels）：
//   - manual 行（origin='manual'）永不触碰：既不更新也不删除——手动模型与
//     用户显式保留的模型不随上游变动丢失（借鉴 axonhub manual∪fetched 合并策略）；
//   - fetched 行更新源身份（base_url/api_key/platform/name）与上游返回的元数据，
//     但保留用户手动启停（enabled）与健康位（available）；
//   - 能力字段（vision/tools/structured/thinking/maxTokens/type）：incoming 携带
//     capability_source='manual'（用户在 UI 编辑过）的行保留现有值，否则用
//     incoming 值（目录回填或上游解析）覆盖；
//   - 上游消失的 fetched 行删除并同步清理组内引用；manual 行即使上游消失也保留。
func (s *Store) MergeSourceModels(ctx context.Context, source ModelSource, incoming []Model) (ModelMergeResult, error) {
	result := ModelMergeResult{Added: []string{}, Removed: []string{}}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return result, err
	}
	defer tx.Rollback()

	// 读取现有行（单连接库：先全部读进内存再写，避免游标占用连接死锁）。
	type existingRow struct {
		id, name, thinking, origin, capabilitySource string
		maxTokens                                    int
		vision, tools, structured                    bool
	}
	existing := make(map[string]existingRow, len(incoming))
	rows, err := tx.QueryContext(ctx, `SELECT id, name, thinking_mode, origin, capability_source, max_tokens, vision_capable, tools_capable, structured_output FROM models WHERE source_id = ?`, source.ID)
	if err != nil {
		return result, err
	}
	for rows.Next() {
		var r existingRow
		var vision, tools, structured int
		if err := rows.Scan(&r.id, &r.name, &r.thinking, &r.origin, &r.capabilitySource, &r.maxTokens, &vision, &tools, &structured); err != nil {
			rows.Close()
			return result, err
		}
		r.vision, r.tools, r.structured = intBool(vision), intBool(tools), intBool(structured)
		existing[r.id] = r
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return result, err
	}
	rows.Close()

	checked := nowString()
	storedKey, err := s.codec.encrypt(firstEffectiveKey(source))
	if err != nil {
		return result, err
	}
	insertStmt, err := tx.PrepareContext(ctx, `INSERT INTO models(id, source_id, name, source_name, base_url, api_key, platform, type, max_tokens, vision_capable, tools_capable, structured_output, thinking_mode, available, enabled, origin, capability_source, last_checked_at) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return result, err
	}
	defer insertStmt.Close()
	updateStmt, err := tx.PrepareContext(ctx, `UPDATE models SET name = ?, source_name = ?, base_url = ?, api_key = ?, platform = ?, type = ?, max_tokens = ?, vision_capable = ?, tools_capable = ?, structured_output = ?, thinking_mode = ?, capability_source = ?, last_checked_at = ? WHERE id = ? AND source_id = ?`)
	if err != nil {
		return result, err
	}
	defer updateStmt.Close()

	incomingIDs := make(map[string]struct{}, len(incoming))
	for i := range incoming {
		model := normalizeModelDefaults(incoming[i])
		incomingIDs[model.ID] = struct{}{}
		prev, existed := existing[model.ID]
		if existed && prev.origin == "manual" {
			// manual 行完全保留（含能力与启停），只刷新检查时间。
			if _, err := tx.ExecContext(ctx, `UPDATE models SET last_checked_at = ? WHERE id = ? AND source_id = ?`, checked, model.ID, source.ID); err != nil {
				return result, err
			}
			continue
		}
		if existed {
			// 用户编辑过的能力字段（capability_source='manual'）在刷新时保留，
			// 其余能力值随 incoming（目录回填/上游解析）更新。
			vision, tools, structured, thinking, maxTokens, modelType, capabilitySource :=
				model.VisionCapable, model.ToolsCapable, model.StructuredOutput, model.ThinkingMode, model.MaxTokens, model.Type, model.CapabilitySource
			if prev.capabilitySource == "manual" {
				vision, tools, structured = prev.vision, prev.tools, prev.structured
				thinking, maxTokens = prev.thinking, prev.maxTokens
				capabilitySource = "manual"
			}
			if _, err := updateStmt.ExecContext(ctx, model.Name, source.Name, source.BaseURL, storedKey, normalizePlatform(source.Platform), modelType, maxTokens, boolInt(vision), boolInt(tools), boolInt(structured), thinking, capabilitySource, checked, model.ID, source.ID); err != nil {
				return result, err
			}
			continue
		}
		// 新模型默认启用（已确认的默认值），available 初始为 true 由健康检测接管。
		result.Added = append(result.Added, model.ID)
		if _, err := insertStmt.ExecContext(ctx, model.ID, source.ID, model.Name, source.Name, source.BaseURL, storedKey, normalizePlatform(source.Platform), model.Type, model.MaxTokens, boolInt(model.VisionCapable), boolInt(model.ToolsCapable), boolInt(model.StructuredOutput), model.ThinkingMode, boolInt(true), boolInt(model.Enabled), model.Origin, model.CapabilitySource, checked); err != nil {
			return result, err
		}
	}

	// 上游消失的 fetched 行删除（manual 行保留）；同步清理组内引用防悬空。
	for id, prev := range existing {
		if _, stillPresent := incomingIDs[id]; stillPresent || prev.origin == "manual" {
			continue
		}
		result.Removed = append(result.Removed, id)
		if _, err := tx.ExecContext(ctx, `DELETE FROM models WHERE id = ? AND source_id = ?`, id, source.ID); err != nil {
			return result, err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM model_group_models WHERE model_id = ? AND source_id = ?`, id, source.ID); err != nil {
			return result, err
		}
	}
	return result, tx.Commit()
}

// normalizeModelDefaults 补齐模型字段的落库默认值。
// Enabled 恒置 true：该函数只服务于「新插入行」（合并的新模型默认启用——已确认
// 的产品决策；已有行的启停由 UPDATE 语句不涉及该列而天然保留，manual 行整体跳过）。
func normalizeModelDefaults(model Model) Model {
	if model.Type == "" {
		model.Type = "llm"
	}
	if model.ThinkingMode == "" {
		model.ThinkingMode = "both"
	}
	if model.Name == "" {
		model.Name = model.ID
	}
	if model.Origin == "" {
		model.Origin = "fetched"
	}
	model.Enabled = true
	return model
}

// migrateMaheshvaraLabels 把历史 usage 记录里的 canonical_* 展示标签改写为
// maheshvara_*（命名统一的一次性数据迁移；写入侧已改用新值）。幂等：改写
// 后旧行拼写不再存在，重跑无操作。record_json 的 REPLACE 用带引号的完整
// 成员上下文（"usageSource":"..."）与数组元素（"canonical_request"），
// 误碰撞仅影响展示字段，无功能语义。
func (s *Store) migrateMaheshvaraLabels(ctx context.Context) error {
	// 极老的库可能还没有 usage_source 列（本迁移步骤之后的其他列迁移会补）：
	// 先探测，缺列则跳过列改写——record_json 的改写不受影响。
	var hasUsageSource int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_table_info('usage_records') WHERE name = 'usage_source'`).Scan(&hasUsageSource); err == nil && hasUsageSource > 0 {
		if _, err := s.db.ExecContext(ctx,
			`UPDATE usage_records SET usage_source = 'maheshvara_estimate' WHERE usage_source = 'canonical_estimate'`); err != nil {
			return err
		}
	}
	if _, err := s.db.ExecContext(ctx,
		`UPDATE usage_records
		SET record_json = REPLACE(REPLACE(REPLACE(record_json,
			'"usageSource":"canonical_estimate"', '"usageSource":"maheshvara_estimate"'),
			'"canonical_request"', '"maheshvara_request"'),
			'"canonical_response"', '"maheshvara_response"')
		WHERE record_json LIKE '%canonical_%'`); err != nil {
		return err
	}
	return nil
}
