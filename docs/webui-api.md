# 管理 API

[文档索引](README.md) · **简体中文** · [English](webui-api.en.md)

WebUI 使用 `/api/admin` 管理网关。路由定义见 [admin.go](../backend/server/admin.go)，数据语义见[数据模型](webui-data-model.md)。本文中的路径均相对于运行中的网关。

## 鉴权

请求使用面板令牌：

```http
Authorization: Bearer <PANEL_TOKEN>
```

面板令牌来自 `config.json`。浏览器还可通过 `panel_access_token` Cookie 认证。推理令牌与 Agent Key 不代替面板令牌。请求通常使用 `Content-Type: application/json`。

## 响应

管理 handler 通常返回以下信封：

```json
{"ok": true, "data": {}}
```

```json
{"ok": false, "error": {"code": "invalid_json", "message": "Invalid request body"}}
```

列表通常在 `data.items`，分页列表另含 `data.total`。鉴权中间件的 401 为 `{"error":"..."}`，重载和 SSE 使用各自格式；不要假设所有响应都有 `ok`。

常见失败：400 参数校验失败；401 面板令牌无效；404 对象不存在；409 冲突或任务进行中；503 `store_unavailable`。`error.code` 还包括 `save_source_failed`、`save_group_failed`、`save_token_failed`、`usage_log_not_found` 等操作相关错误。

## 启动配置

配置字段、环境变量及数据路径见[部署指南](deployment.md#配置)。模型源和令牌使用 SQLite；当前配置解析不导入旧 `tokens`、`modelGroups` 或 `dashboardToken` 字段。

## 运行配置

| 方法 | 路径 | 返回 / 行为 |
| --- | --- | --- |
| GET | `/api/admin/runtime-config` | 当前配置及生效策略，**含明文 `panelAccessToken`**，仅限管理端 |
| PUT | `/api/admin/runtime-config` | 更新指定字段，返回 `{updated, restartRequired}` |
| POST | `/api/admin/reload` | 重新读取配置；独立响应包含 `reloaded`、`debugMode`、`verboseLog`、`serverChangedRequiresRestart` 及新旧监听地址 `server` |
| POST | `/api/admin/restart-required/check` | 返回重启提示状态 |

增量更新示例：

```json
{
  "httpTimeout": 120,
  "usageLog": {"bodyMaxKB": 0, "retentionDays": 30},
  "agentRemote": {"enabled": true, "publicUrl": "https://gw.example.com"}
}
```

可更新字段：`host`、`port`、`logLevel`、`httpTimeout`、`panelAccessToken`、`databasePath`、`enablePprof`、`outbound.deniedIpRanges`、`usageLog`、`systemLog`、`modelCatalog.syncIntervalMinutes`、`agentRemote`。

省略字段保持原值。`usageLog` 支持逐字段更新，`0` 是有效值；`outbound` 替换整个禁止段列表，空列表全部放行。`agentRemote.publicUrl: ""` 清空对外地址。`webuiDir`、`secretKeyPath`、`maxBodyBytes` 不在此更新接口中。

监听地址、端口、数据库路径和 pprof 变更要求重启；HTTP 超时、日志正文策略等可对后续请求生效。`bodyMaxKB` 默认 0，保存正文需显式开启；清理参数在后台下一轮巡检读取。不要记录或公开运行配置响应中的令牌。

## 模型源

| 方法 | 路径 | 行为 |
| --- | --- | --- |
| GET | `/api/admin/model-sources` | 返回 `items`，包含 `refreshState` |
| POST | `/api/admin/model-sources` | 保存模型源；缺省 ID 从名称生成 |
| PUT | `/api/admin/model-sources/:id` | 按路径 ID 保存模型源 |
| PATCH | `/api/admin/model-sources/:id/enabled` | `{enabled: boolean}`，仅切换启停，不重新拉取 |
| DELETE | `/api/admin/model-sources/:id` | 删除源及相关数据 |
| POST | `/api/admin/model-sources/:id/fetch` | 启动后台拉取，返回 `{started, alreadyRunning?}` |
| POST | `/api/admin/models/refresh` | 为启用的源启动拉取，返回 `{started, total}` |
| GET | `/api/admin/model-catalog/status` | 模型能力目录状态 |
| POST | `/api/admin/model-catalog/refresh` | 立即刷新目录，返回 `{refreshed, status}` |

手工模型源示例，替换地址、密钥和模型 ID 后保存：

```json
{
  "id": "upstream-main",
  "name": "Main upstream",
  "baseUrl": "https://api.example.com/v1",
  "apiKey": "<UPSTREAM_API_KEY>",
  "platform": "chat_completions",
  "enabled": true,
  "autoFetchModels": false,
  "manualModels": [{"id": "provider-model", "name": "Provider model", "type": "llm", "available": true}]
}
```

协议标识使用 `chat_completions`、`responses`、`anthropic`、`gemini`、`custom:<id>`；旧 `openai`、`openai-compatible`、`claude` 仍有兼容处理。`fetchBaseUrl` 可单独设置模型发现地址。自动发现源设置 `autoFetchModels: true`。

`apiKeys` 支持多 Key 与逐 Key 模型权限，`keyStrategy` 支持 `single`、`round-robin`、`random`、`priority`。拉取接口返回不代表任务完成；轮询源的 `refreshState`。拉取期间部分写操作返回冲突。刷新保留手工模型和用户编辑的能力字段。

## 模型

| 方法 | 路径 | 行为 |
| --- | --- | --- |
| GET | `/api/admin/models?sourceId=&search=` | 按源和文本筛选，返回 `items` |
| PATCH | `/api/admin/models/:sourceId?modelId=` | 更新名称、类型、能力、`maxTokens`、`thinkingMode`、`enabled` |
| DELETE | `/api/admin/models/:sourceId?modelId=` | 删除模型并清理组引用 |

模型 ID 可能包含 `/`，因此必须放在 URL 编码后的 `modelId` 查询参数中。以 `(sourceId, id)` 区分不同源的同名模型。

## 模型组

客户端请求的 `model` 使用组名。成员优先使用 `sourceId:modelId`，避免同名歧义：

```json
{
  "id": "default",
  "name": "default",
  "enabled": true,
  "models": ["upstream-main:provider-model"],
  "strategy": "round-robin",
  "maxRetries": 3,
  "retryInterval": 1000,
  "maxConcurrency": 10,
  "dailyLimitMaxRequests": 0,
  "dailyLimitMaxTokens": 0,
  "type": "llm"
}
```

| 方法 | 路径 | 行为 |
| --- | --- | --- |
| GET / POST | `/api/admin/model-groups` | 列表 / 保存 |
| PUT / DELETE | `/api/admin/model-groups/:id` | 保存 / 删除 |
| POST / DELETE | `/api/admin/model-groups/:id/models` | `{models: ["source:model"]}`，增加 / 移除成员 |

策略：`round-robin`、`sequential`、`random`；`retryInterval` 单位为毫秒；并发和日限额为 `0` 时不限。删除组可能级联禁用仅获该组授权的令牌，返回 `disabledTokens`，避免空授权列表意外扩大权限。

## 访问令牌

```json
{"name":"client-main","token":"<RANDOM_RELAY_TOKEN>","enabled":true,"allowedGroups":["default"],"scopes":[]}
```

| 方法 | 路径 | 行为 |
| --- | --- | --- |
| GET / POST | `/api/admin/api-tokens` | 脱敏列表 / 保存 |
| PUT / DELETE | `/api/admin/api-tokens/:name` | 更新 / 删除 |
| GET | `/api/admin/api-tokens/:name/reveal` | 按需读取明文 `{name, token}` |

创建要求非空 token；更新时 token 为空保留原密钥，省略 `allowedGroups` / `scopes` 保留原值，显式空数组清空。改名通过 `newName`；改名失败不回滚此前已保存的属性。普通令牌的空 `allowedGroups` 表示不限制模型组。带 `agent` 作用域的 Key 用于远程运维，被推理端点拒绝。

## 用量

查询时间使用 RFC3339，范围为 `[from, to)`。`keyName`、`groupName`、`modelName`、`sourceId` 支持重复参数，多选不是逗号拼接。还支持 `keyHash`、`statusCode`、`status=success|failed`，`modelGroup` 为组名兼容参数。

| GET 路径（前缀 `/api/admin/usage`） | 内容 |
| --- | --- |
| `/stats` | 请求、成功/失败、token、缓存命中率与平均耗时 |
| `/trend` | 按本地日聚合，`utcOffsetMinutes` 为本地时间减 UTC 的分钟数 |
| `/by-model` | `{model, requests, failed, tokens}` 列表 |
| `/by-model-daily` | `top` 为 1–20，默认 8；返回 `{date, model, requests, isOther}` 列表 |
| `/pulse` | `from` 必填，窗口最多 48 小时；`bucketMinutes` 为 1、5 或 15；返回 `{points, window}` |
| `/logs` | `limit` / `offset` 分页，返回 `{items, total}` |
| `/logs/:id` | 存储的请求详情，正文是否存在由捕获策略决定 |
| `/seq` | 用量序列，用于刷新检测 |
| `/assets/:requestId/:file` | 受管理鉴权保护的媒体；文件名为 `<16-hex>.<ext>` |
| `/storage` | 数据库文件/WAL/空闲页、请求/系统日志内容、媒体/汇总/索引占用与维护状态 |

`pulse.points[].t` 为 Unix 毫秒；P95 在最多 16384 个样本时精确，超出后使用蓄水池估算；窗口 P95 不是各桶 P95 均值。`by-model-daily` 的其他模型行使用 `isOther: true` 与空 `model`，由客户端显示名称。

`POST /api/admin/usage/cleanup` 触发日志留存和空间回收任务，返回排队结果。`GET /api/admin/usage/maintenance` 返回阶段、阻塞及失败状态。`POST /api/admin/usage/reset` 同步清除请求日志、汇总和附件引用，返回 `{reset, reclaimQueued}`；附件文件删除和数据库空间回收在后台进行。正文捕获及留存说明见[部署指南](deployment.md#请求日志)。

## 协议与助手

| 方法 | 路径 | 行为 |
| --- | --- | --- |
| GET | `/api/admin/protocols`、`/api/admin/protocols/enabled` | 草稿/活动修订与可执行协议目录 |
| GET | `/api/admin/protocols/schema` | 当前定义、语义、能力、映射和诊断目录 |
| GET / PUT | `/api/admin/protocols/:id/draft` | 读取 / 保存完整草稿；PUT 使用 `If-Match` 检查旧草稿哈希 |
| POST | `/api/admin/protocols/validate`、`/api/admin/protocols/preview` | 编译与请求/响应/事件/工作流预览 |
| POST | `/api/admin/protocols/:id/verify` | 验证确切草稿，保存不可变修订与离线报告 |
| GET | `/api/admin/protocols/:id/revisions`、`/api/admin/protocols/:id/diff` | 修订列表及比较 |
| POST | `/api/admin/protocols/:id/activate`、`/api/admin/protocols/:id/rollback` | 在当前引擎验证门槛下切换活动版本 |
| POST | `/api/admin/protocols/test` | 真实生成或模型目录操作验证，可能产生费用 |
| POST | `/api/admin/protocols/combinations` | 验证入口/上游的能力组合 |
| GET / PUT | `/api/admin/protocols/bindings` | 读取 / 保存模型源及模型组协议约束 |
| POST | `/api/admin/protocols/reload` | 编译成功后原子替换注册表 |
| GET | `/api/admin/protocols/migration` | 迁移状态 |
| POST | `/api/admin/protocols/migration/preview`、`/api/admin/protocols/migration/apply` | 全图迁移预演及事务切换 |

旧 `/custom-protocols` 执行端点已移除。保存不启用；离线报告必须匹配内容哈希和当前编译器。真实验证与离线报告独立。完整路径、请求结构和并发要求见[协议定义](protocol-definition-reference.md)、[处理器](../backend/server/protocol_revision_admin.go)及[前端 v2 契约](../packages/webui/src/lib/protocol-v2.ts)。

客户端的新协议入口为 `/gateway/:protocolId/*path`，按活动定义分派 HTTP、WebSocket 或任务操作；使用网关统一推理授权。既有四个公共协议入口保留，使用同一内核。不能通过协议定义绕过授权或伪造能力，详见[指南](protocol-guide.md)。

`/api/admin/agent/*` 与[远程 REST](remote-agent-api.md#rest)共用会话 handler，但使用面板令牌。创建／编辑会话、消息、审批、停止、截断和草稿恢复均见该文档。助手通过 `elysia_cli` 执行运维，另有 `ask_user`、`update_plan`；模型调用计入用量，`relayMode=agent-assist`。凭证在会话视图中脱敏，测试凭证的复用限协议测试相关命令。

## 系统日志与健康

- `GET /api/admin/logs?level=info&limit=100&offset=0`：分页系统日志。
- `GET /api/admin/health`：数据库状态、内存分配、系统内存和 GC 计数。
- `GET /health`：公开存活检查，与管理端详细诊断不同。

## 最小检查

```bash
curl http://127.0.0.1:8765/api/admin/health \
  -H "Authorization: Bearer <PANEL_TOKEN>"
```

成功时检查 `ok` 与 `data`；401 时先核对令牌类型与配置路径。
