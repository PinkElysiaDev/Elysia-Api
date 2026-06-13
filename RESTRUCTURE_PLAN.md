# Elysia-API 独立项目重构方案

## 一、现状分析

### 1.1 当前架构

当前项目是一个三合一的 Koishi 插件体系：

```
Koishi 实例
├── aggregator 插件 (TypeScript) — 负责从各 API 平台拉取模型列表
├── orchestrator 插件 (TypeScript) — 负责组合模型组、加密写入 config.json、管理 Go 后端进程
└── Go 后端 (独立二进制) — 实际的 API 网关，负责请求转发、格式转换、用量统计
    ├── config.json — 所有配置（模型组、API Key 加密、token、dashboard token 等）
    ├── usage-records.jsonl — 用量记录持久化
    └── 内嵌 HTML dashboard — 简单的用量统计面板
```

**核心问题**：
- 必须依赖 Koishi 运行，脱离 Koishi 则无法配置和管理
- aggregator 和 orchestrator 的功能分散在 TypeScript 侧，Go 后端只是执行者
- config.json 承载了所有状态（启动参数 + 业务配置 + 加密密钥），结构臃肿
- 用量记录使用 JSONL 文件存储，全量加载到内存，存在内存泄漏风险

### 1.2 内存占用分析（600MB 问题的根因）

经过代码分析，发现以下内存问题：

#### 问题 1：usageRecords 无限增长（主因）
`server.go` 中 `usageRecords []usageRecord` 是一个内存切片，每次请求都会 append。虽然有 `compactUsageRecordsLocked()` 做截断，但：
- 默认 `usagePersistMaxRecords = 10000`
- 每条 `usageRecord` 包含 `IncomingBody`、`OutgoingBody`、`ProviderResponse` 三个 `usageBody`，每个最大 64KB
- 单条记录最大约 192KB，10000 条可达 **1.92GB**
- 即使截断到 64KB，10000 条仍需约 **640MB**

#### 问题 2：upstreamUsageObservingBody 的 buffer 累积
`upstreamUsageObservingBody` 在流式响应中缓存所有 SSE 数据行，直到连接关闭才清理。长时间流式请求会累积大量数据。

#### 问题 3：observingStreamWriter 的 responseText
`observingStreamWriter.responseText` 是 `strings.Builder`，会累积完整的响应文本用于 token 估算。对于长响应（如代码生成），这会占用大量内存。

#### 问题 4：rateLimits map 无清理
`rateLimits map[string]*rateLimitState` 只有在日期变更时才会重置计数器，但 map 本身不会清理旧的 key。

#### 问题 5：JSONL 全量加载
`loadUsageRecords()` 启动时将整个 JSONL 文件加载到内存，文件越大内存占用越高。

---

## 二、重构目标

### 2.1 新架构

```
独立部署模式（无需 Koishi）：
├── Go 后端（完整独立工作）
│   ├── config.json — 仅保存启动参数（端口、access token、数据库路径等）
│   ├── elysia.db (SQLite WAL) — 业务数据持久化
│   │   ├── channels 表 — 模型渠道配置（原 aggregator 的源配置）
│   │   ├── models 表 — 自动/手动模型列表
│   │   ├── model_groups 表 — 模型组配置
│   │   ├── access_tokens 表 — API 访问令牌
│   │   ├── usage_records 表 — 用量统计记录
│   │   └── system_logs 表 — 系统日志
│   ├── WebUI（独立前端，由另外模型开发）
│   │   ├── 渠道管理页面 — 增删改查 API 渠道，触发模型拉取
│   │   ├── 模型组配置页面 — 创建/编辑模型组，选择模型和策略
│   │   ├── 用量统计页面 — 查询用量记录、统计图表
│   │   ├── 令牌管理页面 — 管理 API 访问令牌
│   │   └── 系统设置页面 — 修改端口、dashboard token 等
│   └── REST API — 供 WebUI 调用的完整后端 API

Koishi 兼容模式（可选）：
└── Koishi 入口插件（极简）
    └── 仅修改 config.json + 发送启动/重启信号
```

### 2.2 设计原则

1. **config.json 极简化**：只保存后端启动必需的参数（host、port、dashboardToken、dbPath）
2. **SQLite WAL 持久化**：所有业务数据（渠道、模型、模型组、令牌、用量、日志）存入 SQLite
3. **后端 API 完整化**：提供完整的 CRUD API，WebUI 可完全独立管理一切
4. **Koishi 可选化**：无 Koishi 也能完整工作，Koishi 插件只是便利的启动器

---

## 三、详细实现方案

### 3.1 Phase 1：SQLite 持久化层

#### 3.1.1 新的 config.json 结构（极简化）

```json
{
  "host": "127.0.0.1",
  "port": 8765,
  "dashboardToken": "your-dashboard-token",
  "dbPath": "elysia.db",
  "masterKeyPath": "master.key",
  "heartbeatTimeout": 300,
  "httpTimeout": 120,
  "debugMode": false,
  "verboseLog": false
}
```

#### 3.1.2 SQLite 数据库 Schema

```sql
-- 模型渠道（原 aggregator 的源配置）
CREATE TABLE channels (
  id          TEXT PRIMARY KEY,
  name        TEXT NOT NULL UNIQUE,
  base_url    TEXT NOT NULL,
  api_key_enc TEXT,          -- AES-256-GCM 加密
  platform    TEXT NOT NULL, -- openai | claude | gemini | openai-compatible
  enabled     INTEGER NOT NULL DEFAULT 1,
  auto_fetch  INTEGER NOT NULL DEFAULT 1, -- 是否自动拉取模型列表
  created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- 模型列表（从渠道自动拉取或手动添加）
CREATE TABLE models (
  id            TEXT PRIMARY KEY,
  channel_id    TEXT NOT NULL REFERENCES channels(id),
  name          TEXT NOT NULL,
  display_name  TEXT,
  platform      TEXT NOT NULL,
  model_type    TEXT NOT NULL DEFAULT 'llm', -- llm | embedding | reranker
  max_tokens    INTEGER DEFAULT 128000,
  vision_capable  INTEGER DEFAULT 0,
  tools_capable    INTEGER DEFAULT 0,
  structured_output INTEGER DEFAULT 0,
  thinking_mode  TEXT DEFAULT 'both',
  available      INTEGER DEFAULT 1,
  source         TEXT NOT NULL DEFAULT 'auto', -- auto | manual
  last_checked   DATETIME,
  created_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_models_channel ON models(channel_id);
CREATE INDEX idx_models_platform ON models(platform);

-- 模型组（原 orchestrator 的 modelGroups）
CREATE TABLE model_groups (
  id            TEXT PRIMARY KEY,
  name          TEXT NOT NULL UNIQUE,
  enabled       INTEGER NOT NULL DEFAULT 1,
  strategy      TEXT NOT NULL DEFAULT 'round-robin',
  max_retries   INTEGER DEFAULT 3,
  retry_interval INTEGER DEFAULT 1000,
  max_concurrency  INTEGER,
  daily_limit_max_requests INTEGER,
  daily_limit_max_tokens   INTEGER,
  max_tokens    INTEGER,
  model_type    TEXT DEFAULT 'llm',
  vision_capable  INTEGER,
  tools_capable    INTEGER,
  thinking_mode    TEXT,
  created_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- 模型组-模型关联表
CREATE TABLE model_group_items (
  group_id TEXT NOT NULL REFERENCES model_groups(id) ON DELETE CASCADE,
  model_id TEXT NOT NULL REFERENCES models(id) ON DELETE CASCADE,
  sort_order INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (group_id, model_id)
);

-- API 访问令牌
CREATE TABLE access_tokens (
  id        TEXT PRIMARY KEY,
  name      TEXT NOT NULL UNIQUE,
  token_enc TEXT NOT NULL, -- AES-256-GCM 加密
  enabled   INTEGER NOT NULL DEFAULT 1,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- 用量记录（替代 usage-records.jsonl）
CREATE TABLE usage_records (
  id                INTEGER PRIMARY KEY AUTOINCREMENT,
  request_id        TEXT NOT NULL,
  started_at        DATETIME NOT NULL,
  ended_at          DATETIME,
  key_name          TEXT,
  key_hash          TEXT,
  requested_group   TEXT,
  group_id          TEXT,
  group_name        TEXT,
  model_id          TEXT,
  model_name        TEXT,
  platform          TEXT,
  input_format      TEXT,
  target_platform   TEXT,
  stream            INTEGER DEFAULT 0,
  status_code       INTEGER,
  error             TEXT,
  first_byte_ms     INTEGER,
  duration_ms       INTEGER,
  input_tokens      INTEGER DEFAULT 0,
  output_tokens     INTEGER DEFAULT 0,
  total_tokens      INTEGER DEFAULT 0,
  cache_hit_tokens  INTEGER DEFAULT 0,
  estimated_tokens  INTEGER DEFAULT 0,
  is_estimated      INTEGER DEFAULT 0,
  usage_source      TEXT,
  incoming_body     TEXT,     -- JSON，截断到 64KB
  outgoing_body     TEXT,     -- JSON，截断到 64KB
  provider_response TEXT,     -- JSON，截断到 64KB
  created_at        DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_usage_started ON usage_records(started_at);
CREATE INDEX idx_usage_group ON usage_records(group_name);
CREATE INDEX idx_usage_model ON usage_records(model_name);
CREATE INDEX idx_usage_key ON usage_records(key_name);

-- 系统日志
CREATE TABLE system_logs (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  level      TEXT NOT NULL, -- INFO | WARN | ERROR | DEBUG
  message    TEXT NOT NULL,
  source     TEXT,          -- 模块名
  metadata   TEXT,          -- JSON
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_logs_time ON system_logs(created_at);
CREATE INDEX idx_logs_level ON system_logs(level);
```

#### 3.1.3 SQLite WAL 配置

```go
// 数据库初始化时设置 WAL 模式
db.Exec("PRAGMA journal_mode=WAL")
db.Exec("PRAGMA synchronous=NORMAL")   // WAL 模式下 NORMAL 已足够安全
db.Exec("PRAGMA busy_timeout=5000")    // 并发等待超时 5s
db.Exec("PRAGMA cache_size=-64000")    // 64MB 缓存
db.Exec("PRAGMA foreign_keys=ON")
```

### 3.2 Phase 2：后端管理 API（供 WebUI 使用）

新增 `/__admin` 路由组（需要 dashboardToken 认证），提供完整 CRUD：

#### 3.2.1 渠道管理 API

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/__admin/channels` | 列出所有渠道 |
| POST | `/__admin/channels` | 创建渠道 |
| PUT | `/__admin/channels/:id` | 更新渠道 |
| DELETE | `/__admin/channels/:id` | 删除渠道 |
| POST | `/__admin/channels/:id/fetch` | 触发从该渠道拉取模型列表 |
| POST | `/__admin/channels/fetch-all` | 触发从所有启用渠道拉取模型 |

请求/响应体示例：
```json
// POST /__admin/channels
{
  "name": "openai-main",
  "baseUrl": "https://api.openai.com",
  "apiKey": "sk-xxx",
  "platform": "openai",
  "enabled": true,
  "autoFetch": true
}

// 响应
{
  "id": "ch_xxx",
  "name": "openai-main",
  "baseUrl": "https://api.openai.com",
  "platform": "openai",
  "enabled": true,
  "autoFetch": true,
  "modelCount": 42,
  "createdAt": "2026-01-01T00:00:00Z"
}
```

#### 3.2.2 模型管理 API

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/__admin/models` | 列出所有模型（支持 ?channelId= 过滤） |
| POST | `/__admin/models` | 手动添加模型 |
| PUT | `/__admin/models/:id` | 更新模型信息 |
| DELETE | `/__admin/models/:id` | 删除模型 |
| POST | `/__admin/models/batch-delete` | 批量删除模型 |

#### 3.2.3 模型组管理 API

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/__admin/groups` | 列出所有模型组 |
| POST | `/__admin/groups` | 创建模型组 |
| PUT | `/__admin/groups/:id` | 更新模型组 |
| DELETE | `/__admin/groups/:id` | 删除模型组 |
| PUT | `/__admin/groups/:id/models` | 更新模型组的模型列表 |

请求/响应体示例：
```json
// POST /__admin/groups
{
  "name": "gpt-4o",
  "enabled": true,
  "strategy": "round-robin",
  "maxRetries": 3,
  "modelIds": ["model_id_1", "model_id_2"],
  "maxTokens": 128000,
  "visionCapable": true,
  "toolsCapable": true
}

// 响应
{
  "id": "grp_xxx",
  "name": "gpt-4o",
  "enabled": true,
  "strategy": "round-robin",
  "models": [
    { "id": "model_id_1", "name": "gpt-4o", "channelName": "openai-main", "platform": "openai" },
    { "id": "model_id_2", "name": "gpt-4o", "channelName": "azure-backup", "platform": "azure" }
  ],
  ...
}
```

#### 3.2.4 访问令牌管理 API

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/__admin/tokens` | 列出所有令牌（不返回明文） |
| POST | `/__admin/tokens` | 创建令牌 |
| PUT | `/__admin/tokens/:id` | 更新令牌（启用/禁用/改名） |
| DELETE | `/__admin/tokens/:id` | 删除令牌 |

#### 3.2.5 用量统计 API（增强现有）

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/__usage/stats` | 统计概览（保留现有，从 SQLite 查询） |
| GET | `/__usage/logs` | 日志列表（保留现有，从 SQLite 分页查询） |
| GET | `/__usage/logs/:id` | 日志详情（保留现有） |
| POST | `/__usage/reset` | 重置用量（保留现有） |
| GET | `/__usage/export` | 导出用量数据为 CSV |

#### 3.2.6 系统设置 API

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/__admin/settings` | 获取当前配置 |
| PUT | `/__admin/settings` | 更新配置（写入 config.json + 热重载） |
| POST | `/__admin/restart` | 重启后端 |

#### 3.2.7 系统日志 API

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/__admin/logs` | 查询系统日志（支持分页、级别过滤） |

### 3.3 Phase 3：模型拉取功能迁移

将 aggregator 插件的模型拉取逻辑迁移到 Go 后端：

#### 3.3.1 新增 `backend/fetch/` 包

```go
// fetch/fetcher.go
type Fetcher struct {
    client *http.Client
}

// FetchModels 从指定渠道拉取模型列表
func (f *Fetcher) FetchModels(channel *Channel) ([]Model, error)

// fetchOpenAI 从 OpenAI 兼容端点拉取
func (f *Fetcher) fetchOpenAI(channel *Channel) ([]Model, error)

// fetchClaude 从 Claude 端点拉取
func (f *Fetcher) fetchClaude(channel *Channel) ([]Model, error)

// fetchGemini 从 Gemini 端点拉取
func (f *Fetcher) fetchGemini(channel *Channel) ([]Model, error)
```

#### 3.3.2 启动时自动拉取

后端启动时，自动从所有 `enabled=true AND auto_fetch=1` 的渠道拉取模型，更新到 `models` 表。

#### 3.3.3 定期刷新

增加可选的定时拉取机制（如每小时刷新一次），通过配置项控制。

### 3.4 Phase 4：内存优化

#### 3.4.1 usageRecords 从内存切片改为 SQLite 写入

**核心改动**：不再在内存中维护 `usageRecords []usageRecord`，改为直接写入 SQLite。

- `recordUsage()` → 直接 `INSERT INTO usage_records`
- `usageSnapshot()` → `SELECT ... FROM usage_records WHERE started_at BETWEEN ? AND ?`
- `compactUsageRecordsLocked()` → `DELETE FROM usage_records WHERE id NOT IN (SELECT id FROM usage_records ORDER BY started_at DESC LIMIT ?)`
- 统计查询直接用 SQL 聚合，不再全量加载到内存

#### 3.4.2 限制 usageBody 大小

- 将 `usageBodyMaxBytes` 从 64KB 降为 16KB
- 流式响应的 `upstreamUsageObservingBody` 只保留最近 10 个事件（而非 50 个）
- `observingStreamWriter.events` 限制为 10 个

#### 3.4.3 移除 JSONL 持久化

删除 `usage_persist.go` 中的 JSONL 逻辑，完全由 SQLite 接管。

#### 3.4.4 rateLimits 定期清理

增加定期清理机制，每小时清理过期的 rateLimitState。

### 3.5 Phase 5：Koishi 入口插件重构

#### 3.5.1 极简 Koishi 插件

```typescript
// 只保留：
// 1. 配置项：host, port, dashboardToken
// 2. 启动后端进程
// 3. 发送心跳
// 4. 通过修改 config.json + /__reload 热更新配置

export const name = 'elysia-api'

export interface Config {
  host: string
  port: number
  dashboardToken: string
}

export function apply(ctx: Context, config: Config) {
  // 启动 Go 后端进程
  // 发送心跳保持后端存活
  // config 变更时写入 config.json 并调用 /__reload
  // 不再依赖 aggregator 服务
}
```

#### 3.5.2 删除 aggregator 和 orchestrator 包

整个 `packages/aggregator/` 和 `packages/orchestrator/` 目录将被删除，功能已全部迁移到 Go 后端。

### 3.6 Phase 6：WebUI 后端 API 参考文档

为前端开发者提供完整的 API 参考文档，包含：

1. **认证方式**：所有 `/__admin/*` 和 `/__usage/*` 接口使用 `Authorization: Bearer <dashboardToken>` 认证
2. **每个接口的**：请求方法、URL、请求体 Schema、响应体 Schema、错误码
3. **WebSocket 接口**（可选）：`ws://host:port/__admin/ws` 用于实时日志推送
4. **数据模型关系图**：Channel → Model → ModelGroup 的关系

---

## 四、文件结构变更

### 4.1 新增文件

```
backend/
├── db/
│   ├── db.go           — SQLite 初始化、连接管理
│   ├── schema.go       — 建表语句、迁移
│   ├── channels.go     — channels CRUD
│   ├── models.go       — models CRUD
│   ├── groups.go       — model_groups CRUD
│   ├── tokens.go       — access_tokens CRUD
│   ├── usage.go        — usage_records 查询（替代内存存储）
│   └── logs.go         — system_logs 查询
├── fetch/
│   ├── fetcher.go      — 模型拉取器主逻辑
│   ├── openai.go       — OpenAI 模型列表拉取
│   ├── claude.go       — Claude 模型列表拉取
│   └── gemini.go       — Gemini 模型列表拉取
├── admin/
│   └── handlers.go     — /__admin/* 路由处理器
├── config/
│   └── config.go       — 简化后的配置（仅启动参数）
└── main.go             — 更新启动逻辑
```

### 4.2 修改文件

| 文件 | 改动 |
|------|------|
| `backend/server/server.go` | 移除内存 usageRecords，改用 SQLite 查询；config 来源改为 SQLite |
| `backend/server/usage.go` | 重构为 SQLite 查询模式 |
| `backend/server/usage_persist.go` | 删除（由 SQLite 替代） |
| `backend/server/usage_dashboard.go` | 保留，数据来源从内存改为 SQLite |
| `backend/server/responses.go` | config 来源改为 SQLite |
| `backend/config/config.go` | 简化为仅启动参数 |
| `backend/main.go` | 增加 SQLite 初始化、模型自动拉取 |

### 4.3 删除文件

```
packages/aggregator/     — 整个目录删除
packages/orchestrator/   — 整个目录删除
packages/shared/         — 整个目录删除
backend/server/usage_persist.go  — JSONL 持久化删除
```

### 4.4 保留文件

```
backend/relay/           — 协议转换逻辑完整保留，这是核心功能
backend/signal/          — 心跳机制保留
```

---

## 五、实施顺序

| 阶段 | 内容 | 预估工作量 |
|------|------|-----------|
| **Phase 1** | SQLite 持久化层（db 包 + schema） | 中 |
| **Phase 2** | 后端管理 API（admin 包 + 路由） | 大 |
| **Phase 3** | 模型拉取功能迁移（fetch 包） | 中 |
| **Phase 4** | 内存优化（usage 改为 SQLite 写入） | 中 |
| **Phase 5** | Koishi 入口插件重构 | 小 |
| **Phase 6** | WebUI API 参考文档 | 中 |

**建议实施顺序**：Phase 1 → Phase 4 → Phase 3 → Phase 2 → Phase 5 → Phase 6

理由：
- Phase 1 是基础，所有其他 Phase 依赖 SQLite
- Phase 4 解决最紧迫的内存问题
- Phase 3 迁移模型拉取，使后端可以独立获取模型
- Phase 2 在数据层就绪后实现管理 API
- Phase 5 在后端完全独立后简化 Koishi 插件
- Phase 6 文档为前端开发提供参考

---

## 六、600MB 内存问题的立即可行的临时修复

在完整重构前，可以先做以下快速修复来降低内存占用：

1. **降低 `usageBodyMaxBytes`**：从 64KB 降为 8KB
2. **限制内存中保留的 usageRecords 数量**：从 10000 降为 1000
3. **限制 `observingStreamWriter.events`**：从 50 降为 5
4. **限制 `upstreamUsageObservingBody.events`**：从 50 降为 5
5. **启动时只加载最近 N 条记录**：`loadUsageRecords()` 中限制初始加载数量
6. **减少 compact 触发后的内存占用**：compact 后主动释放旧切片

这些改动可以在 1 小时内完成，预计可将内存占用降低到 100-200MB。

---

## 七、兼容性考虑

### 7.1 现有 config.json 迁移

提供一个迁移工具/逻辑，在首次启动时：
1. 检测到旧格式 config.json（包含 modelGroups/tokens 等）
2. 将 channels 信息提取到 `channels` 表
3. 将 modelGroups 提取到 `model_groups` 表
4. 将 tokens 提取到 `access_tokens` 表
5. 生成新的简化 config.json（仅启动参数）
6. 备份旧 config.json 为 config.json.bak

### 7.2 现有 usage-records.jsonl 迁移

首次启动时：
1. 检测到 usage-records.jsonl 存在
2. 将记录导入 SQLite
3. 重命名为 usage-records.jsonl.migrated

### 7.3 API 向后兼容

- 所有现有的 `/v1/*` API 路由和认证方式完全不变
- `/__usage/*` API 保持兼容，只是数据源从内存改为 SQLite
- `/__reload` 保持不变
- `/__heartbeat` 保持不变
