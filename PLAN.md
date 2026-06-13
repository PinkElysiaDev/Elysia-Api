# Elysia-API 独立化改造方案

## 一、现状分析

### 当前架构（三合一模式）

```
┌──────────────────┐     ┌──────────────────┐
│   Aggregator     │────▶│   Orchestrator   │
│  (Koishi 插件)   │     │  (Koishi 插件)   │
│  - 配置 API 源    │     │  - 配置模型组     │
│  - 自动拉取模型   │     │  - 管理 Go 后端   │
│  - 模型验证       │     │  - 写 config.json │
│  - Koishi 服务    │     │  - 发送心跳       │
└──────────────────┘     │  - 发送重载信号   │
                         └────────┬─────────┘
                                  │ spawn + heartbeat
                          ┌───────▼───────────┐
                          │  Go 后端           │
                          │  - API 网关/转发   │
                          │  - 格式转换        │
                          │  - Usage 统计      │
                          │  - 内嵌 Usage 页面 │
                          │  - 心跳超时自杀    │
                          └───────────────────┘
```

**问题**：
1. Aggregator 和 Orchestrator 是 Koishi 插件，完全依赖 Koishi 运行
2. Go 后端通过心跳"摇篮模式"依附于 Koishi，Koishi 停止后后端也会超时退出
3. 配置分散在两个 Koishi 插件的 Schema 中，无法独立使用
4. 模型源配置（aggregator）和模型组配置（orchestrator）必须在 Koishi 控制台操作
5. 心跳机制增加了不必要的复杂性

### 目标架构（独立项目模式）

```
┌──────────────────────────────────────────────┐
│  独立 Go 后端                                │
│  ┌──────────────┐  ┌───────────────────────┐ │
│  │ API 网关/转发 │  │ WebUI 配置面板        │ │
│  │ (已有)       │  │ - 模型源管理 (新增)    │ │
│  │              │  │ - 模型组管理 (新增)    │ │
│  │              │  │ - 用量统计 (已有)      │ │
│  │              │  │ - 系统配置 (新增)      │ │
│  └──────────────┘  └───────────────────────┘ │
│  ┌──────────────┐  ┌───────────────────────┐ │
│  │ 模型拉取服务  │  │ Config 管理器         │ │
│  │ (从TS移植)   │  │ - config.json 读写    │ │
│  │              │  │ - 热重载              │ │
│  └──────────────┘  └───────────────────────┘ │
└──────────────────────────────────────────────┘
         ▲
         │ 仅修改 config.json + 发送重启信号
┌────────┴─────────┐
│ Koishi 入口插件   │
│ (极简，可选)      │
│ - 配置端口        │
│ - 配置 token      │
│ - 启动/重启信号   │
└──────────────────┘
```

## 二、config.json 新 Schema 设计

当前 config.json 只有 server / tokens / modelGroups / usage 等运行时配置。需要新增 `sources` 字段，将 aggregator 的模型源配置合并进来。

### 新 config.json 结构

```jsonc
{
  // === 服务器配置 ===
  "server": {
    "host": "127.0.0.1",
    "port": 8765
  },

  // === 认证配置 ===
  "dashboardToken": "...",         // 面板访问令牌（明文，或用 dashboardTokenEnc 加密）
  "dashboardTokenEnc": { ... },    // 加密的面板令牌
  "tokens": [                      // API 访问令牌
    {
      "name": "test",
      "token": "...",              // 明文（可选）
      "tokenEnc": { ... },         // 加密（可选）
      "enabled": true
    }
  ],

  // === 模型源配置（新增，原 aggregator 功能）===
  "sources": [
    {
      "name": "openai-official",
      "baseUrl": "https://api.openai.com/v1",
      "apiKey": "...",             // 明文（可选）
      "apiKeyEnc": { ... },        // 加密（可选）
      "platform": "openai",       // openai | claude | gemini | openai-compatible
      "enabled": true,
      "autoFetchModels": true,    // 是否自动拉取模型列表
      "manualModels": [           // autoFetchModels=false 时手动指定
        { "id": "gpt-4", "name": "GPT-4" }
      ]
    }
  ],

  // === 模型组配置（原 orchestrator 功能，结构变化）===
  "modelGroups": [
    {
      "id": "default",
      "name": "default",
      "enabled": true,
      "strategy": "round-robin",
      "maxRetries": 3,
      "retryInterval": 1000,
      "maxConcurrency": 10,
      "dailyLimitMaxRequests": 0,
      "dailyLimitMaxTokens": 0,
      "type": "llm",
      "maxTokens": 128000,
      "visionCapable": true,
      "toolsCapable": true,
      // 关键变化：models 改为引用 source 中的模型
      "models": [
        {
          "sourceName": "openai-official",  // 引用 source.name
          "modelName": "gpt-4o",            // 模型 ID
          // 不再需要 baseUrl/apiKey/platform，运行时从 source 解析
        }
      ]
    }
  ],

  // === Responses API 兼容策略 ===
  "responses": {
    "enabled": true,
    "upstreamMode": "auto",
    "transformUnsupportedBehavior": "error",
    "passThroughUnknownFields": true
  },

  // === Usage 统计配置 ===
  "usage": {
    "estimateWhenMissing": true,
    "charsPerToken": 4,
    "defaultOutputTokenEstimate": 1024,
    "imageInputTokenEstimate": 300,
    "fileInputTokenEstimatePerKB": 128
  },
  "usagePersistEnabled": true,
  "usagePersistMaxRecords": 10000,

  // === 运行时配置 ===
  "heartbeatTimeout": 0,     // 0 = 不启用心跳超时自杀
  "httpTimeout": 120,
  "debugMode": false,
  "verboseLog": false
}
```

### 关键设计决策

1. **modelGroups.models 改为引用式**：不再内嵌 baseUrl/apiKey，改为 `sourceName + modelName` 引用，运行时从 sources 解析。这解决了当前 orchestrator 写 config 时需要从 aggregator 拿模型信息的耦合问题。

2. **保留加密机制**：sources 中的 apiKey 和 dashboardToken 仍支持 `*Enc` 加密字段，master.key 机制不变。

3. **heartbeatTimeout 默认改为 0**：独立运行时不启用心跳超时自杀，只有 Koishi 插件模式下才需要。

4. **向后兼容**：旧格式的 config.json（models 内嵌 baseUrl/apiKey）仍应支持读取，实现平滑迁移。

## 三、Go 后端新增功能

### 3.1 模型源管理 API（新增）

```
GET    /__api/sources          - 列出所有模型源
POST   /__api/sources          - 创建模型源
PUT    /__api/sources/:name    - 更新模型源
DELETE /__api/sources/:name    - 删除模型源
POST   /__api/sources/:name/fetch  - 手动触发模型拉取
GET    /__api/sources/:name/models - 获取某个源拉取到的模型列表
```

### 3.2 模型组管理 API（新增）

```
GET    /__api/groups           - 列出所有模型组
POST   /__api/groups           - 创建模型组
PUT    /__api/groups/:id       - 更新模型组
DELETE /__api/groups/:id       - 删除模型组
```

### 3.3 配置管理 API（新增）

```
GET    /__api/config           - 获取当前配置（敏感字段脱敏）
PUT    /__api/config           - 更新配置
POST   /__api/config/reload    - 热重载配置
GET    /__api/config/status    - 获取运行状态
```

### 3.4 令牌管理 API（新增）

```
GET    /__api/tokens           - 列出所有访问令牌（token 脱敏）
POST   /__api/tokens           - 创建令牌
PUT    /__api/tokens/:name     - 更新令牌
DELETE /__api/tokens/:name     - 删除令牌
```

### 3.5 模型拉取服务（从 TypeScript 移植）

将 aggregator 的 `model-fetcher.ts` 逻辑移植到 Go：

- **OpenAI 源拉取**：GET `/v1/models` → 解析 model list
- **Claude 源拉取**：GET `/models` → 解析 model list（失败时回退内置列表）
- **Gemini 源拉取**：GET `/v1beta/models` → 解析 model list
- **定时拉取**：可配置自动拉取间隔
- **模型缓存**：内存中缓存各源拉取到的模型列表

新建文件：`backend/fetcher/fetcher.go`

### 3.6 模型组运行时解析（修改）

当前 `config.ModelGroupConfig.Models` 是 `[]ModelRef`（内嵌 baseUrl/apiKey）。
改为引用式后，运行时需要：

1. 读取 source 配置
2. 如果 `autoFetchModels=true`，从缓存的模型列表中查找
3. 如果 `autoFetchModels=false`，从 source 的 manualModels 构建
4. 解析出实际的 baseUrl / apiKey / platform

新建文件：`backend/server/group_resolver.go`

### 3.7 移除心跳自杀机制

- `heartbeatTimeout` 默认改为 0（不启用）
- 当 `heartbeatTimeout > 0` 时才启动心跳监控（兼容 Koishi 插件模式）
- 独立运行时不需要心跳

修改文件：`backend/signal/heartbeat.go`、`backend/main.go`

### 3.8 WebUI 配置面板

将现有的内嵌 HTML 从 `usage_dashboard.go` 的单页面扩展为完整 SPA。
推荐方案：**Go 后端 embed 静态文件** + **React/Vue 前端构建产物**。

#### WebUI 页面规划

| 页面 | 路由 | 功能 |
|------|------|------|
| 登录 | `/` | 输入 dashboardToken 登录 |
| 总览 | `/dashboard` | 系统状态、快速统计 |
| 模型源 | `/sources` | CRUD 模型源、手动/自动拉取模型 |
| 模型组 | `/groups` | CRUD 模型组、选择源中的模型、配置策略 |
| 用量统计 | `/usage` | 已有的用量统计面板 |
| 系统设置 | `/settings` | 端口、token、Responses 配置、Usage 配置 |

#### 前端技术选型建议

- **React + Vite + shadcn/ui + TailwindCSS**
- 构建产物通过 Go `embed` 嵌入二进制
- 开发时用 Vite dev server 代理到 Go 后端

## 四、Koishi 入口插件改造

### 极简化设计

Koishi 插件仅保留以下功能：

1. **配置项**：后端端口、面板访问令牌
2. **修改 config.json**：将端口和令牌写入 config.json
3. **启动/重启信号**：spawn Go 后端进程，发送心跳/重载信号
4. **不再包含**：模型源配置、模型组配置、模型列表管理

### 新的 Koishi 插件 Schema

```typescript
export const Config: Schema<Config> = Schema.object({
  server: Schema.object({
    host: Schema.string().default('127.0.0.1'),
    port: Schema.number().default(8765),
  }),
  dashboardToken: Schema.string().role('secret'),
  heartbeatInterval: Schema.number().default(60),
  heartbeatTimeout: Schema.number().default(300),
})
```

### 插件逻辑

```typescript
export function apply(ctx: Context, config: Config) {
  const backend = new BackendManager(
    ctx,
    config.server,
    config.dashboardToken,
    config.heartbeatInterval,
    config.heartbeatTimeout,
  )

  ctx.on('ready', () => backend.start())
  ctx.on('dispose', () => backend.stop())

  // 打开面板的链接
  ctx.command('elysia-api.open', '打开配置面板')
    .action(() => `http://${config.server.host}:${config.server.port}/`)
}
```

## 五、实施步骤

### Phase 1：Go 后端 - 配置层改造

1. **修改 `config/config.go`**
   - 新增 `SourceConfig` 结构体和 `Sources []SourceConfig` 字段
   - 修改 `ModelGroupConfig.Models` 支持引用式和内嵌式两种格式
   - 添加向后兼容：旧格式 models（内嵌 baseUrl/apiKey）仍可读取
   - heartbeatTimeout 默认改为 0
   - 新增 `SourceRef` 类型：`{ sourceName, modelName }`

2. **新建 `server/config_api.go`**
   - 实现 `/__api/*` 系列管理 API
   - 所有写操作 → 修改内存中的 Config → 写入 config.json → 可选热重载
   - 读操作返回脱敏后的配置

3. **新建 `server/source_api.go`**
   - 实现 sources CRUD API
   - 触发模型拉取

4. **新建 `server/group_api.go`**
   - 实现 model groups CRUD API

5. **新建 `server/token_api.go`**
   - 实现 tokens CRUD API

### Phase 2：Go 后端 - 模型拉取服务

6. **新建 `fetcher/fetcher.go`**
   - 移植 `model-fetcher.ts` 的 OpenAI / Claude / Gemini 拉取逻辑
   - 实现定时拉取（可配置间隔）
   - 内存缓存模型列表

7. **新建 `server/group_resolver.go`**
   - 运行时解析模型组：从 source 引用解析出实际的 baseUrl / apiKey / platform
   - 处理引用解析失败的回退逻辑
   - 模型组热更新：当 sources 的模型列表更新时，刷新关联的模型组

8. **修改 `server/server.go`**
   - chatCompletions / responses 中使用 group_resolver 获取实际模型信息
   - 注册新的 API 路由

### Phase 3：Go 后端 - 心跳机制改造

9. **修改 `signal/heartbeat.go`**
   - heartbeatTimeout=0 时不启动心跳监控
   - 保留 Koishi 插件模式下的心跳功能

10. **修改 `main.go`**
    - heartbeatTimeout=0 时跳过心跳监控启动

### Phase 4：WebUI 前端

11. **初始化前端项目**
    - 在 `elysia-api/webui/` 下创建 React + Vite 项目
    - 配置 TailwindCSS + shadcn/ui
    - 配置 Vite 代理到 Go 后端

12. **实现登录页**
    - 复用当前 usage dashboard 的登录逻辑
    - 输入 dashboardToken → 存 localStorage → 后续请求带 Authorization

13. **实现模型源管理页**
    - 源列表 + CRUD 表单
    - 每个源：name / baseUrl / apiKey / platform / enabled / autoFetchModels
    - 手动触发拉取按钮
    - 拉取到的模型列表展示

14. **实现模型组管理页**
    - 模型组列表 + CRUD 表单
    - 模型选择器：从已拉取的模型中选择（多选）
    - 策略配置：round-robin / sequential / random
    - 流量限制配置
    - 能力标记：vision / tools / structuredOutput

15. **实现用量统计页**
    - 迁移现有 usage dashboard 的 HTML/JS 逻辑
    - 嵌入到 SPA 路由中

16. **实现系统设置页**
    - 服务器端口/地址配置
    - 访问令牌管理
    - Responses API 配置
    - Usage 统计配置
    - 调试模式开关

17. **构建 & embed**
    - `npm run build` → `webui/dist/`
    - Go 中使用 `//go:embed` 嵌入静态文件
    - 新建 `server/static.go`：提供 embed.FS 服务

### Phase 5：Koishi 插件极简化

18. **新建简化版 Koishi 插件**
    - 仅保留 server / dashboardToken / heartbeat 配置
    - 保留 BackendManager（但大幅简化，不再写 modelGroups / sources 到 config.json）
    - 保留心跳发送（兼容模式）

19. **删除 aggregator 和 orchestrator 插件**
    - 将这两个包从 workspace 中移除
    - shared 包不再需要

### Phase 6：文档 & 发布

20. **更新 README**
    - 独立使用方式：直接运行二进制 + 编辑 config.json
    - Koishi 集成方式：安装插件 + 配置端口
    - WebUI 使用说明
    - config.json 完整 Schema 文档

21. **构建 & 发布**
    - Go 后端多平台交叉编译
    - 前端构建产物嵌入二进制
    - Docker 镜像构建
    - npm 发布 Koishi 入口插件

## 六、文件变更清单

### Go 后端新增文件

| 文件 | 用途 |
|------|------|
| `backend/fetcher/fetcher.go` | 模型拉取服务（从 TS 移植） |
| `backend/server/config_api.go` | 配置管理 API |
| `backend/server/source_api.go` | 模型源管理 API |
| `backend/server/group_api.go` | 模型组管理 API |
| `backend/server/token_api.go` | 令牌管理 API |
| `backend/server/group_resolver.go` | 模型组运行时解析 |
| `backend/server/static.go` | WebUI 静态文件 embed |
| `webui/` | React 前端项目 |

### Go 后端修改文件

| 文件 | 变更 |
|------|------|
| `backend/config/config.go` | 新增 SourceConfig、修改 ModelGroupConfig.Models、heartbeatTimeout 默认值 |
| `backend/server/server.go` | 注册新路由、使用 group_resolver |
| `backend/signal/heartbeat.go` | 支持 heartbeatTimeout=0 不启动 |
| `backend/main.go` | 条件启动心跳监控 |
| `backend/go.mod` | 新增依赖（可能需要） |

### Koishi 插件变更

| 文件 | 变更 |
|------|------|
| `packages/entry/` (新建) | 极简 Koishi 入口插件 |
| `packages/aggregator/` | 删除 |
| `packages/orchestrator/` | 删除 |
| `packages/shared/` | 删除 |

## 七、风险与注意事项

1. **向后兼容**：旧版 config.json（models 内嵌 baseUrl/apiKey）必须仍可读取，实现平滑迁移
2. **加密密钥**：master.key 机制保持不变，sources 中的 apiKey 也使用相同的加密方案
3. **模型组解析**：引用式模型在 source 不可用或模型未拉取时的回退策略需要仔细处理
4. **并发安全**：WebUI 修改 config.json 时需要与 API 请求处理并发安全
5. **WebUI 认证**：所有 `/__api/*` 管理接口需要 dashboardToken 认证
6. **前端构建**：需要 CI 环境安装 Node.js 来构建前端，然后嵌入 Go 二进制
7. **已有 usage dashboard**：现有内嵌 HTML 中的 JS 逻辑（约 800 行）可以逐步迁移到 React 组件，不必一次性重写
