// SQLite schema 迁移：列补齐、rollup 表创建与历史数据回填（一次性）。
package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"
)

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
		// 自定义协议（协议设计器）：config 列保留原始 JSON，其余列用于列表。
		`CREATE TABLE IF NOT EXISTS custom_protocols (id TEXT PRIMARY KEY, name TEXT NOT NULL DEFAULT '', version TEXT NOT NULL DEFAULT '', type TEXT NOT NULL DEFAULT 'llm', config TEXT NOT NULL, created_at TEXT NOT NULL, updated_at TEXT NOT NULL)`,
		// 外置媒体引用计数：文件按内容哈希扁平存放（全局去重），本表追踪
		// 「哪个记录引用了哪个文件」，记录删除时据此判断文件是否还能删。
		`CREATE TABLE IF NOT EXISTS usage_asset_refs (
			asset_file TEXT NOT NULL,
			request_id TEXT NOT NULL,
			PRIMARY KEY (asset_file, request_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_usage_asset_refs_request ON usage_asset_refs(request_id)`,
		`CREATE INDEX IF NOT EXISTS idx_system_logs_created_at ON system_logs(created_at)`,
		// AI 助手（协议 Agent）会话与消息：会话含设置/草稿/审批状态，
		// 消息按 seq 单调排序完整保留轮次轨迹。test_api_key 走 secretCodec 加密。
		`CREATE TABLE IF NOT EXISTS agent_sessions (
				id TEXT PRIMARY KEY,
				title TEXT NOT NULL DEFAULT '',
				mode TEXT NOT NULL DEFAULT 'create',
				protocol_id TEXT NOT NULL DEFAULT '',
				seed_config TEXT NOT NULL DEFAULT '',
				draft_config TEXT NOT NULL DEFAULT '',
				draft_restore TEXT NOT NULL DEFAULT '',
				test_base_url TEXT NOT NULL DEFAULT '',
			test_api_key TEXT NOT NULL DEFAULT '',
			model_source_id TEXT NOT NULL DEFAULT '',
			model_name TEXT NOT NULL DEFAULT '',
			thinking_enabled INTEGER NOT NULL DEFAULT 0,
			thinking_effort TEXT NOT NULL DEFAULT '',
			plan_mode INTEGER NOT NULL DEFAULT 0,
			allow_live_test TEXT NOT NULL DEFAULT 'ask',
			allow_save TEXT NOT NULL DEFAULT 'ask',
			allow_delete TEXT NOT NULL DEFAULT 'ask',
			status TEXT NOT NULL DEFAULT 'idle',
			pending_action TEXT,
			plan_json TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS agent_messages (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			session_id TEXT NOT NULL,
			seq INTEGER NOT NULL,
			role TEXT NOT NULL,
			content TEXT NOT NULL,
			model TEXT NOT NULL DEFAULT '',
			usage_json TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			UNIQUE(session_id, seq)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_agent_messages_session ON agent_messages(session_id, seq)`,
		`CREATE INDEX IF NOT EXISTS idx_agent_sessions_updated_at ON agent_sessions(updated_at)`,
	}
	for _, stmt := range stmts {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	// 增量迁移（幂等 ALTER）：SQLite 无 ADD COLUMN IF NOT EXISTS，重复执行报
	// duplicate column，addColumnIgnoreDup 忽略该错误。新列一律追加到这里；
	// 唯一索引等非 ALTER 步骤跟在清单之后。
	incrementalColumns := []string{
		// api_tokens：组级访问权限 + token 去重哈希（空 hash 不参与唯一约束）+ 端点作用域。
		`ALTER TABLE api_tokens ADD COLUMN allowed_groups_json TEXT NOT NULL DEFAULT '[]'`,
		`ALTER TABLE api_tokens ADD COLUMN token_hash TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE api_tokens ADD COLUMN scopes TEXT NOT NULL DEFAULT '[]'`,
		// agent_sessions：方案清单 / 计划模式 / 草稿还原点（单槽覆盖）/ 删除类权限。
		`ALTER TABLE agent_sessions ADD COLUMN plan_json TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE agent_sessions ADD COLUMN plan_mode INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE agent_sessions ADD COLUMN draft_restore TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE agent_sessions ADD COLUMN allow_delete TEXT NOT NULL DEFAULT 'ask'`,
		// usage_records：缓存命中 token 数——统计接口直接 SUM，免逐条解析
		// record_json；历史行为 0（旧记录不回填）。
		`ALTER TABLE usage_records ADD COLUMN cache_hit_tokens INTEGER NOT NULL DEFAULT 0`,
	}
	for _, stmt := range incrementalColumns {
		if err := s.addColumnIgnoreDup(ctx, stmt); err != nil {
			return err
		}
	}
	// 为 token_hash 建唯一索引（WHERE token_hash != '' 保证空值不参与约束）。
	if _, err := s.db.ExecContext(ctx, `CREATE UNIQUE INDEX IF NOT EXISTS idx_api_tokens_hash ON api_tokens(token_hash) WHERE token_hash != ''`); err != nil {
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
	if err := s.addColumnIgnoreDup(ctx, `ALTER TABLE usage_records ADD COLUMN started_ms INTEGER NOT NULL DEFAULT 0`); err != nil {
		return err
	}
	// 聚合覆盖索引：统计 KPI（UsageTotals）、按模型分组（UsageByModel）与按日趋势
	// （UsageDaily）的全部过滤与聚合列都在索引内——index-only 扫描，不回表读取
	// 含 record_json（完整请求/响应体，单行可达几十 KB）的胖行。月级数据的聚合
	// 从 GB 级行读取降为几十 MB 索引扫描。列全为整数/短字符串，空间开销可控；
	// 幂等建索引，大表首次执行为一次性启动成本。
	// 增量迁移：usage 记录持久化模型源 ID，来源筛选按 source_id 精确匹配，
	// 不再把源名展开成模型名（同名跨源会串数据）。存量行默认为空，不会命中源筛选。
	if err := s.addColumnIgnoreDup(ctx, `ALTER TABLE usage_records ADD COLUMN source_id TEXT NOT NULL DEFAULT ''`); err != nil {
		return err
	}
	// 大库上首次建索引要扫全表胖行，可达分钟级且期间无任何输出——升级后首启
	// 会停在本步。先探存在性，确实要建时说一声，避免看起来像"卡住"。
	var hasAggIndex int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = 'idx_usage_agg_cover'`).Scan(&hasAggIndex); err != nil {
		return err
	}
	if hasAggIndex == 0 {
		log.Printf("[migration] building usage aggregate index — one-time on first start after upgrade, duration scales with usage history")
	}
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
			parsed := parseTime(r.startedAt)
			if parsed.IsZero() {
				// 无法解析的旧行保持 started_ms=0，与 rollup / 时间窗查询隔离；
				// 全时段 totals/by-model 另走 raw fallback 计入。
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

// maheshvaraLabelsMigrationVersion 标记"canonical → maheshvara 展示标签改写"
// 已完成的 schema_migrations 版本号。改写本身幂等，但 WHERE LIKE 需要全表扫
// 描胖行——大库上每次启动都重跑会明显拖慢启动，故用版本门控只跑一次。
const maheshvaraLabelsMigrationVersion = 2

// migrateMaheshvaraLabels 把历史 usage 记录里的 canonical_* 展示标签改写为
// maheshvara_*（命名统一的一次性数据迁移；写入侧已改用新值）。以
// schema_migrations 版本门控：已改写的库直接返回，不再全表扫描。record_json
// 的 REPLACE 用带引号的完整成员上下文（"usageSource":"..."）与数组元素
// （"canonical_request"），误碰撞仅影响展示字段，无功能语义。
func (s *Store) migrateMaheshvaraLabels(ctx context.Context) error {
	var applied int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM schema_migrations WHERE version = ?`, maheshvaraLabelsMigrationVersion).Scan(&applied); err != nil {
		return err
	}
	if applied > 0 {
		return nil
	}
	// 与覆盖索引同理：全表扫描在数据量大的库上可达分钟级，先说一声。
	started := time.Now()
	log.Printf("[migration] rewriting legacy usage labels — one-time on first start after upgrade, duration scales with usage history")
	// usage_source 列自建表即在 CREATE TABLE 内，本库恒存在；探测仅为防御
	// 外来/前代工程的库（缺列则不可能存有 canonical_estimate，跳过列改写
	// 是正确行为）——record_json 的改写不受探测门控。
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
	if _, err := s.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO schema_migrations(version, applied_at) VALUES(?, ?)`,
		maheshvaraLabelsMigrationVersion, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		return err
	}
	log.Printf("[migration] legacy usage labels rewritten in %s", time.Since(started).Round(time.Millisecond))
	return nil
}

// ErrRollupBackfillInProgress 表示小时聚合后台回填正在运行，ClearUsage 抢
// 互斥锁失败。调用方（resetUsage）持有 usage writer/persist 锁期间不能排队
// 等待——大库首次回填可能持锁数分钟，排队会把所有请求的 usage 落库一并卡住。
var ErrRollupBackfillInProgress = errors.New("rollup backfill in progress")
