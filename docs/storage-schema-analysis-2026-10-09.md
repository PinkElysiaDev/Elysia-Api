# Elysia-API 数据库表结构分析（2026-10-09）

> 一句话定位：**SQLite 单库 32 张表，分五组承载这座网关的全部持久化状态——「谁在调用」（核心业务）、「协议是什么」（协议体系）、「跨协议怎么转」（转换策略）、「账怎么算」（用量聚合）、「迁移到哪了」（元）。** 表结构本身编码了项目的核心不变量：内容寻址的不可变修订、CAS 乐观锁、密文载荷、触发器维护的单行计数器、以及「未上报 ≠ 上报为零」的统计口径。

基线：分支 `deploy`，基于 2026-10-09 工作树。真实表数 **32**（CREATE TABLE 语句 33 条，其中 `usage_asset_refs` 有初始建表与生命周期迁移重建两条；另有瞬态表 `log_migration_refs` 由 RENAME 产生、事务内 DROP，不计入）。分组：核心业务 11 + 协议体系 8 + 转换策略 8 + 用量聚合 4 + 元 1。全部 DDL 位于 `backend/storage`（下文 file:line 均相对该目录，跨包引用给全路径）。

## 0. 全局机制

**库与连接**：modernc.org/sqlite（纯 Go，无 CGO）；`SetMaxOpenConns(1)` 单连接（store.go:35）；PRAGMA 组合——WAL、busy_timeout=5000、**foreign_keys=ON**（所有 FK 实际生效，store.go:74）、synchronous=NORMAL、temp_store=MEMORY、cache_size=-65536（store.go:70-87）。

**加密（secretCodec）**：AES-256-GCM，密文格式 `enc:v1:` + base64(nonce‖ciphertext)，密钥由主密钥 SHA256 派生（crypto.go:37-105）。幂等：已有前缀不二次加密；历史明文行读到原样返回；主密钥为空退化明文模式。行级解密失败**清空留行并告警**（decryptOrClear，crypto.go:138-145），不让一行坏密文拖垮整表扫描。启动时 `SecretIntegrityProbe` 试解一行密文探测密钥匹配（crypto.go:112-133）。

**时间戳双轨制**：展示用 RFC3339Nano 字符串（`nowString`，store.go:119），排序/过滤用 Unix 毫秒整型列（`started_ms`/`created_ms`）。原因：RFC3339Nano 字符串字典序在整秒与毫秒精度混合时不可靠（migrate.go:158-162）。存量行有一次性分批回填（migrate.go:253-340，每事务 2000 行）。

**迁移三层**（store.go:88-97 执行顺序：`migrate()` → `migrateGenerationJobs()` → `migrateConversion()`（内含 evidence）→ `migrateLogLifecycle()`）：

| 层 | 机制 | 适用 |
| --- | --- | --- |
| 全量建表 | `CREATE TABLE IF NOT EXISTS` + 幂等 `ALTER TABLE ADD COLUMN`（addColumnIgnoreDup，store.go:100-108） | 结构补齐，每次启动重放 |
| 版本化标签 | `schema_migrations` 行门控，昂贵/一次性步骤 | 5 个版本行（见 §5） |
| 一次性回填 | 分批事务 + 进度记录 | token_hash、started_ms、rollup 建表 |

## 1. 核心业务（11 表）

### 1.1 settings — KV 杂项

```sql
CREATE TABLE IF NOT EXISTS settings (key TEXT PRIMARY KEY, value TEXT NOT NULL, updated_at TEXT NOT NULL)
```

| 字段 | 类型 | 约束 | 含义与读写 |
| --- | --- | --- | --- |
| key | TEXT | PK | 设置键；当前全库仅 `legacyConfigImported` 一键（旧 config 单次导入标记，server/store_bridge.go:11-70） |
| value | TEXT | NOT NULL | `json.Marshal` 后的任意 JSON 值；写 `SetSetting`（UPSERT，store.go:131）/ 读 `GetSetting`（store.go:140） |
| updated_at | TEXT | NOT NULL | RFC3339Nano |

无加密、无清理，永久保留。

### 1.2 api_tokens — 网关访问令牌

```sql
CREATE TABLE IF NOT EXISTS api_tokens (name TEXT PRIMARY KEY, token TEXT NOT NULL, enabled INTEGER NOT NULL DEFAULT 1, created_at TEXT NOT NULL, updated_at TEXT NOT NULL)
ALTER TABLE api_tokens ADD COLUMN allowed_groups_json TEXT NOT NULL DEFAULT '[]'
ALTER TABLE api_tokens ADD COLUMN token_hash TEXT NOT NULL DEFAULT ''
ALTER TABLE api_tokens ADD COLUMN scopes TEXT NOT NULL DEFAULT '[]'
CREATE UNIQUE INDEX IF NOT EXISTS idx_api_tokens_hash ON api_tokens(token_hash) WHERE token_hash != ''
```

| 字段 | 类型 | 约束 | 含义与读写 |
| --- | --- | --- | --- |
| name | TEXT | PK | 令牌名；改名 = 主键 UPDATE（store.go:249） |
| token | TEXT | NOT NULL | **enc:v1: 密文**；读 scanAPIToken（store.go:154-168） |
| token_hash | TEXT | NOT NULL DEFAULT ''，部分唯一索引 | 明文 SHA256 全量 hex，同值去重（store.go:205-208；存量回填 migrate.go:120-157） |
| enabled | INTEGER | DEFAULT 1 | 启停；组删除致授权清空时自动置 0 防扩权（models_groups.go:447-461） |
| allowed_groups_json | TEXT（JSON） | DEFAULT '[]' | 允许访问的模型组名数组；空=不限（store.go:229-238；组改名/删除同步 models_groups.go:274/447） |
| scopes | TEXT（JSON） | DEFAULT '[]' | 端点作用域；值域 ⊆ {`agent`}（types.go:166-182）；带 agent 时组强制 `["agent"]`（store.go:224-228）；与组授权正交——空 = 仅推理面 |
| created_at / updated_at | TEXT | NOT NULL | RFC3339Nano |

热路径读方：`loadTokensFromStore`（server/route_cache.go:197-212）构建鉴权缓存。无 TTL；物理删除仅 `DeleteAPIToken`（store.go:242）。

### 1.3 model_sources — 模型源（多 Key 所在地）

```sql
CREATE TABLE IF NOT EXISTS model_sources (id TEXT PRIMARY KEY, name TEXT NOT NULL, base_url TEXT NOT NULL,
  api_key TEXT NOT NULL DEFAULT '', platform TEXT NOT NULL, enabled INTEGER NOT NULL DEFAULT 1,
  auto_fetch_models INTEGER NOT NULL DEFAULT 1, manual_models_json TEXT NOT NULL DEFAULT '[]',
  fetch_base_url TEXT NOT NULL DEFAULT '', api_keys TEXT NOT NULL DEFAULT '',
  key_strategy TEXT NOT NULL DEFAULT 'single', created_at TEXT NOT NULL, updated_at TEXT NOT NULL)
ALTER TABLE model_sources ADD COLUMN cache_synthesis INTEGER NOT NULL DEFAULT 0
```

| 字段 | 类型 | 约束 | 含义与读写 |
| --- | --- | --- | --- |
| id | TEXT | PK | 源 ID；写 upsertSource（sources.go:74-112） |
| base_url | TEXT | NOT NULL | 请求端点 base；预置路径语义切换时批量重写（AppendSourceBaseURLSuffix，sources.go:534-580） |
| api_key | TEXT | NOT NULL DEFAULT '' | 单 key 模式密文（enc:v1:） |
| api_keys | TEXT | NOT NULL DEFAULT '' | **多 key 配置**：`[]SourceAPIKey{value/note/disabled/fetchedModels/allowedModels}`（types.go:73-81）JSON **整体加密**；空串 = 单 key 模式；刷新回写带 `updated_at` 乐观锁（CommitSourceRefresh，sources.go:413-461） |
| platform | TEXT | NOT NULL | `openai`/`gemini`/`anthropic`/…或 `custom:<protocol_id>` 协议绑定引用；协议改名/归档同步重写（custom_protocol.go:101、protocol_history.go:135/148） |
| auto_fetch_models | INTEGER | DEFAULT 1 | 自动拉取模型；决定 key 的 FetchedModels（自动发现集）与 AllowedModels（手动分配集）哪个生效，二者互斥清空（sources.go:78-85） |
| manual_models_json | TEXT（JSON） | DEFAULT '[]' | `[]Model` 手动模型权威集（明文 JSON） |
| fetch_base_url | TEXT | DEFAULT '' | 模型列表拉取专用地址；空 = 与 base_url 一致 |
| key_strategy | TEXT | DEFAULT 'single' | 枚举：`single`/`round-robin`/`random`/`priority`（types.go:92-99）；请求期展开候选（server/relay_retry.go:111-168） |
| cache_synthesis | INTEGER | DEFAULT 0 | 为声明 cache.breakpoints 的上游补结构断点的运维开关（migrate.go:223-225） |
| enabled / created_at / updated_at | — | — | 常规 |

删除 = 应用层手级联事务（删源 + models + 组引用 + 相关 protocol_bindings，sources.go:143-165；models 表无 FK）。

### 1.4 models — 模型行（合并语义）

```sql
CREATE TABLE IF NOT EXISTS models (id TEXT NOT NULL, source_id TEXT NOT NULL DEFAULT '', name TEXT NOT NULL,
  source_name TEXT NOT NULL DEFAULT '', base_url TEXT NOT NULL, api_key TEXT NOT NULL DEFAULT '', platform TEXT NOT NULL,
  type TEXT NOT NULL DEFAULT 'llm', max_tokens INTEGER NOT NULL DEFAULT 0, vision_capable INTEGER NOT NULL DEFAULT 0,
  tools_capable INTEGER NOT NULL DEFAULT 0, structured_output INTEGER NOT NULL DEFAULT 0,
  thinking_mode TEXT NOT NULL DEFAULT 'both', available INTEGER NOT NULL DEFAULT 1, enabled INTEGER NOT NULL DEFAULT 1,
  origin TEXT NOT NULL DEFAULT 'fetched', capability_source TEXT NOT NULL DEFAULT '', last_checked_at TEXT NOT NULL,
  PRIMARY KEY (id, source_id))
```

| 字段 | 类型 | 约束 | 含义与读写 |
| --- | --- | --- | --- |
| (id, source_id) | TEXT | 复合 PK | 模型身份；source_id='' 为 legacy 无源行 |
| api_key | TEXT | DEFAULT '' | 源首个有效 key 的密文**冗余快照**，仅供健康检测等遗留方回退；热路径用源级 key 集（sources.go:186-190 注释） |
| type | TEXT | DEFAULT 'llm' | 枚举：`llm`/`embedding` |
| vision_capable / tools_capable / structured_output | INTEGER | DEFAULT 0 | 能力位；tools_capable 可经 Agent nonce 探针实证置位（EnableModelFunctionTools，protocol_history.go:320-331） |
| thinking_mode | TEXT | DEFAULT 'both' | 已见值 `both`/`non-thinking-only`（目录回填 model_catalog.go:666-668） |
| available | INTEGER | DEFAULT 1 | **健康位**（健康检测自动翻转，models_groups.go:486-492）；可调度 = enabled && available（types.go:116-118） |
| enabled | INTEGER | DEFAULT 1 | 用户手动启停 |
| origin | TEXT | DEFAULT 'fetched' | 枚举：`fetched`（刷新合并替换）/ `manual`（刷新永不触碰、上游消失不删，sources.go:242-252） |
| capability_source | TEXT | DEFAULT '' | 枚举：`''` 未知 / `catalog` 目录回填可覆盖 / `manual` 用户手改刷新保留（migrate.go:226-229） |
| last_checked_at | TEXT | NOT NULL | 最近刷新/健康检查时刻 |

写入核心是**合并非替换**：`MergeSourceModels`（fetch 路径保留 manual 行）与 `SyncManualSourceModels`（manual 权威集）；上游消失的 fetched 行由 sweepMissing 删除并同步清组内引用（sources.go:375-392）。历史遗留：Gemini 模型 ID 的 `models/` 前缀一次性剥离迁移（models_groups.go:675-744）。

### 1.5 model_groups — 模型组（调度策略载体）

```sql
CREATE TABLE IF NOT EXISTS model_groups (id TEXT PRIMARY KEY, name TEXT NOT NULL UNIQUE, enabled INTEGER NOT NULL DEFAULT 1,
  strategy TEXT NOT NULL DEFAULT 'round-robin', max_retries INTEGER NOT NULL DEFAULT 3, retry_interval INTEGER NOT NULL DEFAULT 1000,
  max_concurrency INTEGER NOT NULL DEFAULT 0, daily_limit_max_requests INTEGER NOT NULL DEFAULT 0,
  daily_limit_max_tokens INTEGER NOT NULL DEFAULT 0, type TEXT NOT NULL DEFAULT 'llm', max_tokens INTEGER NOT NULL DEFAULT 0,
  vision_capable INTEGER NOT NULL DEFAULT 0, tools_capable INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL, updated_at TEXT NOT NULL)
```

| 字段 | 类型 | 约束 | 含义 |
| --- | --- | --- | --- |
| name | TEXT | **UNIQUE** | 组名（token 的 allowed_groups 按名引用；同名会令按名查找恒命中首个，故唯一） |
| strategy | TEXT | DEFAULT 'round-robin' | 枚举：`round-robin`（游标轮询）/ `random`（随机起点环绕）/ `sequential`（失败按序回退）（server/relay_retry.go:47-70） |
| max_retries / retry_interval | INTEGER | 3 / 1000 | 组内重试次数与间隔（ms） |
| max_concurrency | INTEGER | DEFAULT 0 | 并发上限；0=不限（执行 server.go:727-772） |
| daily_limit_max_requests / daily_limit_max_tokens | INTEGER | DEFAULT 0 | 每日请求/token 限额；0=不限 |
| type / max_tokens / vision_capable / tools_capable | — | — | 组级类型与能力默认值 |

删除组时级联清理 token 组授权，授权被清空的 token 自动禁用（models_groups.go:413-439 + 447-461）。

### 1.6 model_group_models — 组成员引用

```sql
CREATE TABLE IF NOT EXISTS model_group_models (group_id TEXT NOT NULL, model_id TEXT NOT NULL, source_id TEXT NOT NULL DEFAULT '',
  position INTEGER NOT NULL DEFAULT 0, PRIMARY KEY (group_id, model_id, source_id),
  FOREIGN KEY(group_id) REFERENCES model_groups(id) ON DELETE CASCADE)
```

| 字段 | 类型 | 约束 | 含义 |
| --- | --- | --- | --- |
| (group_id, model_id, source_id) | TEXT | 三列复合 PK | 成员身份；source_id 与模型复合键对齐（'' 为旧数据裸 id，装配端按 id 回退，models_groups.go:143-148） |
| position | INTEGER | DEFAULT 0 | 组内顺序 = 调度优先级；新成员排 MAX(position)+1 之后（models_groups.go:329-347） |

DB 级联仅 group_id；models 侧删除靠应用层事务同步（sources.go:158、models_groups.go:605）。UpsertGroup 成员写入为「先 DELETE 全组再按 position 重插」。

### 1.7 usage_records — 用量/请求日志主表（胖行）

最终形态 = 建表（migrate.go:26）+ 六次增量列（cache_hit_tokens / cache_creation_tokens / usage_report_mask / started_ms / source_id / content_bytes）：

```sql
CREATE TABLE IF NOT EXISTS usage_records (request_id TEXT PRIMARY KEY, started_at TEXT NOT NULL, ended_at TEXT NOT NULL,
  key_name TEXT NOT NULL DEFAULT '', key_hash TEXT NOT NULL DEFAULT '', requested_model_group TEXT NOT NULL DEFAULT '',
  group_id TEXT NOT NULL DEFAULT '', group_name TEXT NOT NULL DEFAULT '', model_id TEXT NOT NULL DEFAULT '',
  model_name TEXT NOT NULL DEFAULT '', platform TEXT NOT NULL DEFAULT '', source_format TEXT NOT NULL DEFAULT '',
  target_format TEXT NOT NULL DEFAULT '', relay_mode TEXT NOT NULL DEFAULT '', responses_mode TEXT NOT NULL DEFAULT '',
  usage_source TEXT NOT NULL DEFAULT '', stream INTEGER NOT NULL DEFAULT 0, status_code INTEGER NOT NULL DEFAULT 0,
  error TEXT NOT NULL DEFAULT '', first_byte_ms INTEGER NOT NULL DEFAULT 0, duration_ms INTEGER NOT NULL DEFAULT 0,
  input_tokens INTEGER NOT NULL DEFAULT 0, output_tokens INTEGER NOT NULL DEFAULT 0, total_tokens INTEGER NOT NULL DEFAULT 0,
  request_truncated INTEGER NOT NULL DEFAULT 0, response_truncated INTEGER NOT NULL DEFAULT 0, record_json TEXT NOT NULL, …增量列)
CREATE INDEX IF NOT EXISTS idx_usage_agg_cover ON usage_records(started_ms, model_name, group_name, key_name, status_code,
  stream, input_tokens, output_tokens, total_tokens, cache_hit_tokens, cache_creation_tokens, usage_report_mask, duration_ms, first_byte_ms)
```

关键列语义：

| 字段 | 含义 |
| --- | --- |
| request_id | 请求 ID；异步任务结算行为 SettlementID；`ON CONFLICT DO NOTHING` 禁止重复落库覆盖（rollup 一致性，usage_logs.go:34-37） |
| started_ms | Unix 毫秒排序/过滤专用（字符串序不可靠）；存量已回填 |
| key_hash | 令牌**短哈希**（SHA256 前 8 hex，server/usage.go:141-144）——与 api_tokens.token_hash 全量哈希是两个口径 |
| group_id / model_id | **遗留列**：当前 INSERT 列清单不含，恒为默认 '' |
| source_format / target_format | 协议转换入/出格式（ingress/definition ID） |
| relay_mode | 枚举：`protocol_v2` / `protocol_v2_websocket` / `protocol_v2_async` / `agent-assist` |
| usage_source | 枚举：`protocol_estimate`（估算）/ `protocol_observed`（观测）/ `maheshvara_estimate`（历史改写产物，migrate.go:411-449） |
| status_code | HTTP 状态码；**499 = 客户端提前断开哨兵**（server/constants.go:16）；2xx-3xx 为成功谓词（queries.go:15-16） |
| cache_creation_tokens | 仅当 usage_report_mask 置 bit2 时为上游上报值；历史行 0 |
| usage_report_mask | 位掩码 bit0=input / bit1=cache_hit / bit2=cache_creation 上报位；**缺位 = 上游未报告，不得当零**（types.go:277-281） |
| record_json | **胖列**：完整 usageRecord JSON（四段 body、usage 明细、warnings、conversionIssues）；BodyOnErrorOnly 时成功请求四段 body 清空（server/usage_sqlite.go:23-28） |
| content_bytes | 内容记账字节 = len(record_json)，驱动配额修剪与页面回收 |

覆盖索引 `idx_usage_agg_cover` 服务 KPI/按模型/按日聚合的 index-only 扫描；写入口 `SaveUsageRecordJSON`（与 rollup 同事务 UPSERT）。**保留策略**：后台 worker 按 retentionDays / maxRecords / maxContentMB 三策略分批修剪（每事务 ≤500 最旧行，server/usage_retention.go:151-268；PruneLogBatch log_maintenance.go:125-198），修剪后 incremental_vacuum 回收空闲页；**rollup 不随修剪删除**——logs 分页用 raw 口径、stats 聚合用 rollup 口径（usage_logs.go:46-50 注释）。

### 1.8 system_logs — 系统日志

```sql
CREATE TABLE IF NOT EXISTS system_logs (id INTEGER PRIMARY KEY AUTOINCREMENT, created_at TEXT NOT NULL, level TEXT NOT NULL,
  message TEXT NOT NULL, fields_json TEXT NOT NULL DEFAULT '{}', …增量列 content_bytes / created_ms)
CREATE INDEX idx_system_time ON system_logs(created_ms, id)
```

| 字段 | 含义 |
| --- | --- |
| level | 枚举：`info` / `warn`（写点仅这两种） |
| fields_json | 任意 map JSON（结构化字段） |
| content_bytes | = len(四文本列) 之和，配额记账 |
| created_ms | 分页排序（ORDER BY created_ms DESC, id DESC） |

写入方：`logSystemEvent`（运营/审计事件：启动/热重载/CRUD/明文 reveal 审计/预置升级丢弃本地修改的审计行）。独立三策略保留（systemLog.* 配置组）。

### 1.9 custom_protocols — 自定义协议原文

```sql
CREATE TABLE IF NOT EXISTS custom_protocols (id TEXT PRIMARY KEY, name TEXT NOT NULL DEFAULT '', version TEXT NOT NULL DEFAULT '',
  type TEXT NOT NULL DEFAULT 'llm', config TEXT NOT NULL, created_at TEXT NOT NULL, updated_at TEXT NOT NULL)
```

| 字段 | 含义 |
| --- | --- |
| id | 协议 ID（COLLATE NOCASE 处理改名/对账）；**必须与 config JSON 内部 "id" 一致**——启动对账 `ReconcileCustomProtocolConfigIDs` 以行 id 为准修历史库 |
| config | 用户提交的完整协议定义**原始 JSON**，未知字段不丢（custom_protocol.go:14-16 注释）；改名时内部 id 字段被 map 形态改写 |

与 platform 列的耦合：`custom:<protocol_id>` 被 model_sources/models.platform 引用；改名与归档都会同步重写引用及 v2 注册表七张表 + agent_sessions.protocol_id（protocol_history.go:138-223）。归档 = 拷入 protocol_history（reason='custom_deleted'）后物理删除本表行；预置行只读不可归档。

### 1.10 agent_sessions — AI 助手会话

```sql
CREATE TABLE IF NOT EXISTS agent_sessions (id TEXT PRIMARY KEY, title TEXT NOT NULL DEFAULT '', mode TEXT NOT NULL DEFAULT 'create',
  protocol_id TEXT NOT NULL DEFAULT '', seed_config TEXT NOT NULL DEFAULT '', draft_config TEXT NOT NULL DEFAULT '',
  draft_restore TEXT NOT NULL DEFAULT '', test_base_url TEXT NOT NULL DEFAULT '', test_api_key TEXT NOT NULL DEFAULT '',
  model_source_id TEXT NOT NULL DEFAULT '', model_name TEXT NOT NULL DEFAULT '', thinking_enabled INTEGER NOT NULL DEFAULT 0,
  thinking_effort TEXT NOT NULL DEFAULT '', plan_mode INTEGER NOT NULL DEFAULT 0, allow_live_test TEXT NOT NULL DEFAULT 'ask',
  allow_save TEXT NOT NULL DEFAULT 'ask', allow_delete TEXT NOT NULL DEFAULT 'ask', max_model_calls INTEGER NOT NULL DEFAULT 0,
  status TEXT NOT NULL DEFAULT 'idle', pending_action TEXT, plan_json TEXT NOT NULL DEFAULT '', plan_summary TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL, updated_at TEXT NOT NULL)
```

| 字段 | 含义 |
| --- | --- |
| id | `as_<UnixNano>_<12hex>` |
| mode | 枚举：`create` / `edit` |
| seed_config / draft_config / draft_restore | 三个 JSON：编辑模式初始配置 / 最新草稿 / **单槽还原点**（最近一轮修改前副本，SessionStateUpdate.DraftRestore 非 nil 即覆盖，agent/store.go:120） |
| test_api_key | **enc:v1: 密文**；管理面永不回传明文，只给 TestAPIKeySet 布尔（agent_session.go:16-19 文件头注释） |
| thinking_effort | 枚举：''/low/medium/high/xhigh/max/adaptive |
| allow_live_test / allow_save / allow_delete | 权限三列，枚举：`ask` / `always` / `never`（写入前归一化，agent/store.go:172-179） |
| max_model_calls | 单轮工具循环上限；0=默认，1..100 钳制 |
| status | 状态机：`idle` / `running` / `waiting_approval`；**启动对账 running→idle，waiting_approval 保留**（ResetRunningSessions，agent_session.go:499-504） |
| pending_action | **唯一可空列**（显式 NULL = 无待批）：PendingAction JSON{Kind: approval/question/plan, 剩余 Calls, Question, Plan…} |
| plan_json / plan_summary | 工作方案清单 `[]PlanStep{title,status}` 与分析摘要 |

JSON 列 5 个、加密列 1 个、枚举列 5 个。截断消息时复位 pending/status。

### 1.11 agent_messages — 助手消息轨迹

```sql
CREATE TABLE IF NOT EXISTS agent_messages (id INTEGER PRIMARY KEY AUTOINCREMENT, session_id TEXT NOT NULL, seq INTEGER NOT NULL,
  role TEXT NOT NULL, content TEXT NOT NULL, model TEXT NOT NULL DEFAULT '', usage_json TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL, UNIQUE(session_id, seq))
```

| 字段 | 含义 |
| --- | --- |
| (session_id, seq) | 逻辑身份；seq 事务内 MAX+1 分配、单调，按序回放完整轮次 |
| role | 枚举：`user` / `assistant` / `tool_result` / `approval` / `system` |
| content | **多态 JSON**：按 role 不同结构（UserContent{Text,Documents} / AssistantContent{Content,Text,Reasoning,ToolCalls} / ToolResultInfo / ApprovalContent{Decision,Names,Note} / SystemContent{Text,Kind:error\|info\|summary,BoundarySeq}） |
| usage_json | assistant 消息用量；**空串 = 非 assistant 落库值**——聚合必须先判空防 json_extract malformed（agent_session.go:96-103，见测试踩坑） |

上下文压缩不删历史行——摘要作为 SystemContent{Kind:"summary",BoundarySeq} 边界消息**追加**（agent/context.go:59）。截断 = 删除 seq > afterSeq 并复位会话状态。

## 2. 协议体系（8 表）

生命周期主线：**草稿（CAS）→ 验证（写不可变修订 + 报告）→ 激活（指针 CAS + generation+1）→ 归档（入 history）**。管理路由见 server/protocol_revision_admin.go:77-84。

### 2.1 protocol_revisions — 内容寻址不可变修订库

```sql
CREATE TABLE IF NOT EXISTS protocol_revisions (protocol_id TEXT NOT NULL, content_hash TEXT NOT NULL, definition TEXT NOT NULL,
  created_at TEXT NOT NULL, PRIMARY KEY(protocol_id, content_hash))
```

| 字段 | 含义 |
| --- | --- |
| (protocol_id, content_hash) | 复合 PK 即**内容寻址**：编译产物规范化 JSON 的哈希；唯一写入路径 `INSERT … ON CONFLICT DO NOTHING`（protocol_revision.go:144-146）——既有哈希永不编辑，任何"修改"都产生新行 |
| definition | 完整协议定义 JSON |
| created_at | 列表按 created_at DESC, content_hash 排序 |

被 verification_reports 与 activations 作 FK 目标；删除仅发生在 DeleteProtocolHistory 级联（见 2.6）。

### 2.2 protocol_drafts — 草稿（乐观锁）

```sql
CREATE TABLE IF NOT EXISTS protocol_drafts (protocol_id TEXT PRIMARY KEY, content_hash TEXT NOT NULL,
  definition TEXT NOT NULL, updated_at TEXT NOT NULL)
```

每协议至多一份草稿；允许保存编译失败的不完整内容。**CAS 语义**：expected=="" 时 INSERT ON CONFLICT DO NOTHING（已存在即 ErrRevisionConflict）；否则 UPDATE WHERE protocol_id=? AND content_hash=?，影响行数≠1 即冲突（protocol_revision.go:81-97）。content_hash 为服务端规范化重算值，客户端不能自带。预置 ID 一律拒写（registry.go:73-75）。

### 2.3 protocol_activations — 激活指针

```sql
CREATE TABLE IF NOT EXISTS protocol_activations (protocol_id TEXT PRIMARY KEY, revision_hash TEXT NOT NULL,
  generation INTEGER NOT NULL, activated_at TEXT NOT NULL,
  FOREIGN KEY(protocol_id, revision_hash) REFERENCES protocol_revisions(protocol_id, content_hash))
```

| 字段 | 含义 |
| --- | --- |
| revision_hash | 当前激活的不可变修订；FK 保证指向存在的修订 |
| generation | 单调激活代数，每次激活 +1（SetProtocolActivation，protocol_revision.go:257-259）；激活前事务内 SELECT 比对 expected，不匹配即 ErrRevisionConflict |

激活门槛在 Service 层：`loadVerifiedRevision` 重编译、要求当前引擎下 kind=offline 的通过报告（registry.go:250-273）。回滚 = 同一函数（Rollback）。

### 2.4 protocol_verification_reports — 验证报告（append-only）

```sql
CREATE TABLE IF NOT EXISTS protocol_verification_reports (id INTEGER PRIMARY KEY AUTOINCREMENT, protocol_id TEXT NOT NULL,
  revision_hash TEXT NOT NULL, compiler_version TEXT NOT NULL, samples_hash TEXT NOT NULL, kind TEXT NOT NULL,
  report TEXT NOT NULL, verified_at TEXT NOT NULL,
  FOREIGN KEY(protocol_id, revision_hash) REFERENCES protocol_revisions(protocol_id, content_hash))
CREATE INDEX idx_protocol_reports_revision ON protocol_verification_reports(protocol_id, revision_hash, compiler_version, kind, id)
```

| 字段 | 含义 |
| --- | --- |
| compiler_version / samples_hash | 三重哈希之二、之三（definitionHash 在 report JSON 内）；`IsCurrent` 三元组比对，引擎或样例集变化即报告过期（diagnostic.go:151-155） |
| kind | `offline`（可授权激活）/ `upstream`（真实上游在线探针，只追加不回写） |
| report | VerificationReport 整体 JSON（checks、issues） |

索引恰好覆盖「取某修订某引擎某 kind 最新一份」与 upstream 历史 LIMIT 20 两类查询。写入前强制 hash == report.DefinitionHash（protocol_revision.go:192-194）。

### 2.5 protocol_bindings — 能力合同 JSON 载体

```sql
CREATE TABLE IF NOT EXISTS protocol_bindings (binding_key TEXT PRIMARY KEY, binding TEXT NOT NULL)
```

| 字段 | 含义 |
| --- | --- |
| binding_key | `json.Marshal([kind,sourceId,modelId,groupId])`；kind ∈ source/model/group，各自字段组合受校验（protocol_binding.go:25-46） |
| binding | ProtocolBinding JSON：`conversion`（钉住的转换策略修订）、`unbound`（显式解绑标志）、`binding`（protocolId/revisionHash/capabilities/transports）、`combinations`（组合验证证据） |

设计意图：与模型目录元数据分离，**目录刷新不覆盖操作员合同**（protocol_binding.go:11-12 注释）。写入口四类：手工保存、托管保存（同事务联动改 platform 列 + conversion_generation CAS）、转换策略激活派生写入、Agent 实证启用工具能力。源删除时按 json_extract(sourceId) 级联删（sources.go:161）。

### 2.6 protocol_history — 归档簿记

```sql
CREATE TABLE IF NOT EXISTS protocol_history (id TEXT PRIMARY KEY, protocol_id TEXT NOT NULL, content_hash TEXT NOT NULL,
  definition TEXT NOT NULL, reason TEXT NOT NULL, is_draft INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL, archived_at TEXT NOT NULL)
CREATE INDEX idx_protocol_history_time ON protocol_history(archived_at DESC,id)
```

| 字段 | 含义 |
| --- | --- |
| id | `protocol_id || '~' || content_hash` 拼接 |
| reason | `preset_replaced`（引擎升级换版归档）/ `custom_deleted`（用户删除自定义协议） |
| is_draft | 1 = 该行来自草稿归档 |

**三层只读守卫**：删除历史时预置协议一律拒绝（「preset_replaced 历史行随引擎更新保留」）；五类活引用（激活/草稿/绑定/任务/Agent 会话）非空拒绝；否则删 reports → revisions → history 行（protocol_history.go:272-305）。**坏库降级**：表缺失（拷库丢 WAL）时归档降级跳过并告警，不阻塞预置升级（:34-39；回归测试 baseline_missing_table_test.go）。

### 2.7 generation_jobs — 异步生成任务（双密文载荷 + 租约）

```sql
CREATE TABLE IF NOT EXISTS generation_jobs (id TEXT PRIMARY KEY, owner_hash TEXT NOT NULL, deduplication_hash TEXT NOT NULL,
  phase TEXT NOT NULL, revision INTEGER NOT NULL, next_attempt INTEGER NOT NULL, lease_until INTEGER NOT NULL,
  job TEXT NOT NULL, result TEXT NOT NULL, UNIQUE(owner_hash, deduplication_hash))
CREATE INDEX idx_generation_jobs_due ON generation_jobs(phase,next_attempt,lease_until)
```

| 字段 | 含义 |
| --- | --- |
| (owner_hash, deduplication_hash) | 幂等预留键：owner = API key 哈希（Read 时属主鉴权）；dedup = sha256(owner‖ingress‖idempotencyKey)；`ON CONFLICT DO NOTHING` + RequestHash 不一致 → ErrIdempotencyConflict（job_coordinator.go:59-63） |
| phase | 状态机：`submitting`/`uncertain`/`polling`/`fetching_result`/`terminal`；迁移守卫 CheckJobTransition |
| revision | 乐观锁版本，每次写 +1（WHERE id=? AND revision=? CAS） |
| next_attempt / lease_until | Unix 毫秒；认领条件 `lease_until<=now`（提交期租约到期被认领 → 直接转 uncertain，**绝不自动重放提交**，job_coordinator.go:169-173）；认领后续约 40s |
| job / result | **两段独立加密**：result 单独 marshal+encrypt 后置 nil，再整体 marshal+encrypt（protocol_job.go:35-54）；任务从不持久化凭证或提示词（job.go:35-37 注释） |

**无删除路径**——终态任务（含结果）永久保留供结果 API 与审计。先预留后 I/O：Submit 先落库拿 ID 再发上游。

### 2.8 generation_job_settlements — 结算 outbox

```sql
CREATE TABLE IF NOT EXISTS generation_job_settlements (job_id TEXT PRIMARY KEY, settlement_id TEXT NOT NULL UNIQUE,
  is_delivered INTEGER NOT NULL DEFAULT 0, FOREIGN KEY(job_id) REFERENCES generation_jobs(id))
```

| 字段 | 含义 |
| --- | --- |
| settlement_id | 结算幂等键（= 请求 ID）；usage 写入按它幂等（重复投递兜底） |
| is_delivered | 投递确认位，usage 落库成功后置 1 |

动机（protocol_job.go:134-136 注释）：「Delivery uses an outbox because providers cannot join SQLite's tx」——终态记账与任务更新**同事务入队**，投递独立重试；崩溃可能重复投递但幂等键兜底。父行永不删除故无孤儿。

## 3. 转换策略（8 表）

迁移门控版本 `2026100801`（+ 完成标记与 6 表存在性校验，坏库重入防御，conversion.go:23-35）；evidence 两表独立版本 `2026100802`。

### 3.1 conversion_policy_drafts / 3.2 revisions / 3.3 activations — 策略三件套

```sql
CREATE TABLE conversion_policy_drafts (id TEXT PRIMARY KEY, hash TEXT NOT NULL, document TEXT NOT NULL, updated_at TEXT NOT NULL)
CREATE TABLE conversion_policy_revisions (id TEXT NOT NULL, hash TEXT NOT NULL, document TEXT NOT NULL, reports TEXT NOT NULL,
  compiler_version TEXT NOT NULL, created_at TEXT NOT NULL, PRIMARY KEY(id,hash))
CREATE TABLE conversion_policy_activations (id TEXT PRIMARY KEY, hash TEXT NOT NULL, selector TEXT NOT NULL,
  generation INTEGER NOT NULL DEFAULT 1)
```

| 表 | 关键语义 |
| --- | --- |
| drafts | 草稿 CAS（服务端对 document 重算 sha256，客户端不能自带）；允许不完整 |
| revisions | 内容寻址 (id,hash)；UPSERT 冲突时**只更新 reports/compiler_version，不改 document**（不可变部分）；激活时 compiler_version 必须等于当前引擎否则 "policy verification is stale" |
| activations | 策略自身激活代数（每次 +1，区别于全局 conversion_generation）；selector = ConversionMatch JSON，服务端白名单只放行协议对字段（sourceId/targetId/family/wire），model/operation/path 等被拒 |

激活是重事务：generation CAS + 激活 CAS + 绑定读集全量比对 + 派生绑定写入（conversion.go:208-261）。

### 3.4 conversion_provider_evidence — 上游签名探针证据（30 天时效）

```sql
CREATE TABLE conversion_provider_evidence (policy_hash TEXT NOT NULL, rule_id TEXT NOT NULL, scope_key TEXT NOT NULL,
  target_hash TEXT NOT NULL, compiler TEXT NOT NULL, expires_at INTEGER NOT NULL,
  PRIMARY KEY(policy_hash,rule_id,scope_key,target_hash,compiler))
```

| 字段 | 含义 |
| --- | --- |
| policy_hash + rule_id | 生效组合后的策略哈希 + `provider_signature` 规则 ID（仅此类规则需要证据） |
| scope_key | 账号/模型范围的 **HMAC 摘要**——绑定「一个策略+账号+模型」 |
| target_hash | 探针通过时的上游协议修订哈希 |
| compiler | 引擎版本；升级即全部失效 |
| expires_at | Unix 秒，写入 now+30 天，读取过滤 expires_at>now；UPSERT 续期 |

探针是显式管理动作：合成请求、**绝不执行 provider 返回的工具**（server/conversion_probe.go:40-42 注释）。

### 3.5 conversion_policy_evidence — 策略验证审计流水（append-only）

```sql
CREATE TABLE conversion_policy_evidence (sequence INTEGER PRIMARY KEY AUTOINCREMENT, policy_id TEXT NOT NULL,
  revision_hash TEXT NOT NULL, compiler TEXT NOT NULL, reports TEXT NOT NULL, created_at TEXT NOT NULL)
CREATE INDEX idx_conversion_policy_evidence ON conversion_policy_evidence(policy_id,revision_hash,sequence)
```

每次验证追加一行当次 CombinationReport 快照；**当前代码无读取路径**——纯审计流水，无清理（「30 天时效」只属于 3.4 的 expires_at 语义，本表不清理）。

### 3.6 protocol_continuations — 续传密文载体

```sql
CREATE TABLE protocol_continuations (id TEXT PRIMARY KEY, owner TEXT NOT NULL, session TEXT NOT NULL, scope_key TEXT NOT NULL,
  parent TEXT NOT NULL, ordinal INTEGER NOT NULL, digest TEXT NOT NULL, ciphertext TEXT NOT NULL, bytes INTEGER NOT NULL,
  created_at INTEGER NOT NULL, expires_at INTEGER NOT NULL)
CREATE INDEX idx_continuation_match    ON protocol_continuations(owner,session,scope_key,parent,ordinal,digest)
CREATE INDEX idx_continuation_expiry   ON protocol_continuations(expires_at)
CREATE INDEX idx_continuation_eviction ON protocol_continuations(created_at,id)
CREATE INDEX idx_continuation_session  ON protocol_continuations(owner,session,scope_key,parent,created_at)
```

| 字段 | 含义 |
| --- | --- |
| owner | 属主 HMAC 摘要 = Digest(sha256(accessToken))——**键控摘要防离线枚举** |
| session | 客户端会话 ID（header/body，≤512 字符）；空串 = 仅客户端载体不入库 |
| scope_key | 上游 scope（账号+模型+协议）的 HMAC 摘要 |
| parent | 本轮之前历史的键控摘要；轮次裁剪按 parent 分组 |
| ordinal + digest | 轮内节点序号 + 节点内容 HMAC 摘要；**tool_call 按内容摘要匹配、跳过 ordinal**（conversion.go:330-333） |
| ciphertext | `elysia-continuation.v1.` + base64(nonce‖AES-GCM(record))；写入强制前缀与长度上限且必须启用加密 |
| created_at | **UnixNano**——驱逐 LRU 键与轮次排序用 |
| expires_at | Unix 秒（来自密文内记录） |

**三级清理（均在 SaveContinuation 单事务内）**：① 过期清理（每次写入先 DELETE expires_at≤now LIMIT 256）；② 会话轮次裁剪（保留最近 N 轮 parent 组）；③ 全局字节上限 LRU 驱逐。读取 `LIMIT 2` 防歧义：>1 条命中判 ambiguous，恰 1 条计 hit。

### 3.7 conversion_generation — 全局转换代数（单行触发器表）

```sql
CREATE TABLE conversion_generation (id INTEGER PRIMARY KEY CHECK(id=1), generation INTEGER NOT NULL)
-- 15 个 AFTER INSERT/UPDATE/DELETE 触发器挂在 protocol_bindings、conversion_policy_activations、
-- models、model_sources、model_groups 五张表上，每次变更 bump generation+1
```

业务代码**从不直接 UPDATE**，只由触发器维护。用途：管理端读快照拿 generation → 编辑 → 提交时 `expectedGeneration != actual` → ErrRevisionConflict，拒绝基于过期派生证据的并发写（conversion_snapshot.go:102-114）。

### 3.8 continuation_totals — 续传统计（单行触发器表）

```sql
CREATE TABLE continuation_totals (id INTEGER PRIMARY KEY CHECK(id=1), bytes INTEGER NOT NULL, records INTEGER NOT NULL,
  hits INTEGER NOT NULL DEFAULT 0, misses INTEGER NOT NULL DEFAULT 0, evicted INTEGER NOT NULL DEFAULT 0)
-- INSERT/DELETE 触发器维护 bytes/records；hits/misses/evicted 由 FindContinuation/驱逐路径累加
```

bytes 是**密文字节口径**（len(ciphertext)）；hits/misses/evicted 为累计计数。管理端 `/admin/protocols/continuations` 展示。

## 4. 用量聚合（4 表）

### 4.1 usage_rollup_hour — 小时级预聚合

```sql
CREATE TABLE IF NOT EXISTS usage_rollup_hour (hour_ms INTEGER NOT NULL, model_name TEXT NOT NULL DEFAULT '',
  group_name TEXT NOT NULL DEFAULT '', key_name TEXT NOT NULL DEFAULT '', status_code INTEGER NOT NULL DEFAULT 0,
  cnt INTEGER NOT NULL DEFAULT 0, in_tok INTEGER NOT NULL DEFAULT 0, out_tok INTEGER NOT NULL DEFAULT 0,
  total_tok INTEGER NOT NULL DEFAULT 0, cache_tok INTEGER NOT NULL DEFAULT 0, …cc_tok/cc_rows 经 ALTER 补,
  dur_ms_sum INTEGER NOT NULL DEFAULT 0, fb_ms_sum INTEGER NOT NULL DEFAULT 0, fb_cnt INTEGER NOT NULL DEFAULT 0,
  min_started_ms INTEGER NOT NULL DEFAULT 0, max_started_ms INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (hour_ms, model_name, group_name, key_name, status_code)) WITHOUT ROWID
```

- **桶粒度**：hour × model × group × key × status 五维；不含 key_hash/source_id——带这些筛选的查询整体回退 raw 路径（rollup_query.go:24-27）。
- **UPSERT 累计**：与原始行写入同事务；cnt/in/out/total/cache/cc tokens 累加，min/max 用 SQL MIN/MAX 合并；fb_ms_sum 只收 first_byte_ms>0 的记录。
- **cc_rows**：桶内「真正上报了创建计数」的行数（usage_report_mask & 4 才 +1）——把「全桶未上报」与「上报为零」分开，聚合侧用 creationCoverage（usage_aggregates.go:505-507）。
- **WITHOUT ROWID**：PK 即表结构，范围扫描与 UPSERT 都走主键。
- **回填/重建/审计**：raw 是唯一事实来源——6 小时分块重建可重放；就绪后按小时对账，只修「rollup < raw」的漏计方向，反方向（retention 删过 raw）保留 rollup 计数。**retention 修剪不动本表**。

### 4.2 usage_rollup_state — 回填水位（3 键）

```sql
CREATE TABLE IF NOT EXISTS usage_rollup_state (key TEXT PRIMARY KEY, int_value INTEGER NOT NULL DEFAULT 0)
```

| 键 | 含义 |
| --- | --- |
| backfill_until_ms | 回填上界（首次初始化为当时刻）；**分界不变量**：之前的记录归回填，之后由写入侧增量维护，不重叠 |
| backfill_through_ms | 已回填水位（不含） |
| backfill_ready | 1 = 回填完成；未就绪时聚合查询全部走 raw（优雅降级） |

清空用量时 through=until=now、ready=1——无需重跑回填。

### 4.3 usage_assets / 4.4 usage_asset_refs — 外置媒体（内容寻址 + 引用计数）

```sql
CREATE TABLE IF NOT EXISTS usage_assets (asset_file TEXT PRIMARY KEY, size_bytes INTEGER NOT NULL CHECK(size_bytes>=0))
CREATE TABLE usage_asset_refs (asset_file TEXT NOT NULL REFERENCES usage_assets(asset_file),
  request_id TEXT NOT NULL REFERENCES usage_records(request_id) ON DELETE CASCADE, PRIMARY KEY(asset_file, request_id))
```

| 设计点 | 说明 |
| --- | --- |
| 内容寻址 | 文件名 = 内容哈希 + 扩展名（`^[0-9a-f]{16}\.[a-z0-9]{1,5}$`）；同图多请求只占一份磁盘 |
| externalizeMedia 数据流 | record_json 里媒体被抽成 `__ELYSIA_ASSET__:<mediatype>/<hash.ext>` 占位符 → 文件落盘（原子写）→ **同一事务** INSERT assets + INSERT OR IGNORE refs（「附件元数据与请求记录同事务」） |
| 引用计数 | 文件能否删由 refs 决定；usage_records 行删除时 FK 级联删引用 |
| GC | sweepAssets：os.Remove 未引用文件 → ForgetUnusedAsset 删元数据（DELETE 带 NOT EXISTS(refs) 防孤儿）；24h 宽限清中断写入 |
| 配额口径 | 修剪预算 = usage content_bytes + system content_bytes + **被引用媒体 size_bytes**；删行释放的引用只计入预算，物理文件与 assets 行此时不删 |

refs 表有一次全量重建（log lifecycle 迁移）：旧表改名后扫描 record_json 占位符重建引用，再迁移旧行、DROP 瞬态表。

## 5. 元（1 表）

### 5.1 schema_migrations — 版本化迁移标记

```sql
CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL)
```

| version | 内容 |
| --- | --- |
| 1 | 基础 schema 标记（幂等重放门） |
| 2 | maheshvara 标签一次性改写（usage_source 列 + record_json 内 REPLACE） |
| 2026092901 | 日志生命周期：VACUUM INTO 备份 → content_bytes/created_ms 列 → usage_assets 建表 + refs 重建 → auto_vacuum=INCREMENTAL + 一次性 VACUUM |
| 2026100801 | 转换存储 6 表 + 绑定/激活触发器 |
| 2026100802 | 证据两表 + models/sources/groups 上 9 个 bump conversion_generation 触发器 |

## 6. 横切视角

**加密列全集（8 处）**：api_tokens.token；model_sources.api_key、api_keys；models.api_key（冗余快照）；agent_sessions.test_api_key；generation_jobs.job、result（双段独立加密）；protocol_continuations.ciphertext（独立派生密钥）。

**哈希列**：api_tokens.token_hash（全量 SHA256 hex，去重）；usage_records.key_hash（前 8 hex 短哈希，展示关联）；generation_jobs.owner_hash/deduplication_hash；protocol_continuations 的 owner/scope_key/parent/digest（HMAC 键控摘要，防离线枚举）。

**枚举列值域总表**（节选）：api_tokens.scopes ⊆ {agent}；model_sources.key_strategy、model_groups.strategy；models.type/origin/capability_source/thinking_mode；usage_records.relay_mode/usage_source；system_logs.level；agent_sessions.mode/status/thinking_effort/allow_*；agent_messages.role；protocol_verification_reports.kind；generation_jobs.phase；protocol_history.reason。

**状态机列**：models（available×enabled）、agent_sessions.status（3 态 + 启动对账）、generation_jobs.phase（5 相 + 迁移守卫）、conversion_generation/continuation_totals（单行计数器）。

**触发器全集（17 个）**：conversion_generation 的 15 个 bump 触发器（挂 protocol_bindings / conversion_policy_activations / models / model_sources / model_groups 五表 × INSERT/UPDATE/DELETE）；continuation_totals 的 2 个（protocol_continuations INSERT/DELETE）。

**跨表流转**：

```
协议：drafts ──verify──► revisions(不可变) + verification_reports ──activate(CAS)──► activations(generation+1)
      │归档                                                                  │
      ▼                                                                     ▼
   history(reason, 只读守卫)                          bindings(能力合同) ◄─ 派生写入 ─ conversion_policy_activations

任务：generation_jobs(预留,幂等键) ──租约轮询──► phase: terminal ──同事务──► settlements(outbox) ──投递──► usage_records

用量：usage_records(唯一事实) ──同事务 UPSERT──► rollup_hour ──保留策略修剪(raw only)──► 页面回收
      └─媒体─► asset_refs(级联) ─► assets ◄─ GC sweep(未引用)
```

## 7. 已知边界与设计取舍

- **单连接 SQLite**：写吞吐上限；rollup + 覆盖索引 + index-only 扫描缓解读侧。
- **models 无 FK**：组引用与模型删除全靠应用层事务同步——历史设计，改动需连动三处删除路径。
- **usage_records 的 group_id/model_id 为遗留死列**：恒空，新代码不应读写。
- **conversion_policy_evidence 只写不读**：纯审计流水；若需回溯展示需补读取路径。
- **generation_jobs 永不删除**：审计与结果 API 依赖；长期运行库需关注体积（无 TTL 设计）。
- **rollup 与 raw 双口径**：retention 删 raw 不删 rollup——logs 分页与 stats 聚合数字可能不一致，属有意设计（聚合保全历史）。
- **坏库容忍集中在启动路径**：历史/簿记类可选表的读写都容忍 no such table（真实案例：拷库丢 WAL）。
