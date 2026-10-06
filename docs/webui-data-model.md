# 数据模型

[文档索引](README.md) · **简体中文** · [English](webui-data-model.en.md)

管理 API 的核心字段与语义。完整类型以[前端 types.ts](../packages/webui/src/lib/types.ts)、[存储类型](../backend/storage/types.go)和 [Agent 类型](../packages/webui/src/lib/agent/types.ts)为核对入口；请求与响应以对应 handler 为准。WebUI 通过 HTTP 管理数据，不直接读写配置文件。

## 通用类型

```ts
type ApiResult<T> =
  | { ok: true; data: T }
  | { ok: false; error: { code: string; message: string } }

type Platform = 'chat_completions' | 'responses' | 'anthropic' | 'gemini'
  | 'openai' | 'openai-compatible' | 'claude' | `custom:${string}`
type ModelType = 'llm' | 'embedding' | 'reranker'
type GroupStrategy = 'round-robin' | 'sequential' | 'random'
type SourceKeyStrategy = 'single' | 'round-robin' | 'random' | 'priority'
type ThinkingMode = 'both' | 'non-thinking-only' | 'thinking-only'
type LogLevel = 'debug' | 'info' | 'warn' | 'error'
```

`openai`、`openai-compatible`、`claude` 是存量兼容值；新配置使用标准线路标识。`embedding`、`reranker` 等类型字段不等同于已提供对应推理路由，实际端点见 [API](webui-api.md)和[协议说明](protocol-guide.md)。

## 协议修订与能力绑定

协议使用 `schemaVersion: 2` 定义，语义模型独立使用版本 1。作者版本、定义哈希与编译器版本分别记录。草稿、不可变修订、活动指针和离线/真实验证报告彼此独立；保存不启用。模型源和模型组的绑定记录方向、能力、传输及组合证据，不能仅凭 `platform` 推断工具或媒体能力。

当前契约见 [protocol-v2.ts](../packages/webui/src/lib/protocol-v2.ts) 和[协议参考](protocol-definition-reference.md)。前端以原始 JSON 文本编辑定义；不要把携带长整数和原生扩展的配置经普通 JavaScript 对象往返后再保存。用量语义区分缺失与明确零值，缓存策略区分 TTL、键和有作用域的资源引用。

## 运行配置

| 字段 | 语义 |
| --- | --- |
| `host`、`port` | 配置监听地址；端口 1–65535，改变后重启监听 |
| `panelAccessToken` | 受管理鉴权保护的明文面板令牌，不是旧文档中的 `panelAccessTokenConfigured` |
| `databasePath`、`defaultDatabasePath` | 当前数据库路径与缺省路径 |
| `logLevel`、`httpTimeout`、`enablePprof` | 日志级别、秒级超时、诊断开关 |
| `outbound` | `deniedIpRanges` 与只读 `defaultDeniedIpRanges` |
| `usageLog` | 正文捕获、持久化及留存参数 |
| `modelCatalog` | `enabled`、`url`、`proxy`、`syncIntervalMinutes`；显式周期 0 停止定时同步 |
| `agentRemote` | `enabled`、`publicUrl` |

更新类型 `RuntimeConfigUpdate` 只允许管理接口支持的字段；并非所有读取字段都可写。响应为 `{updated, restartRequired}`。参数默认值、重启要求见[部署](deployment.md#配置)。

## 模型源

| 字段 | 语义 |
| --- | --- |
| `id`、`name` | 稳定标识与显示名称 |
| `baseUrl`、`platform` | 转发地址与上游线路协议 |
| `apiKey`、`apiKeys`、`keyStrategy` | 单 Key 回退、多 Key 列表及调度策略；读取列表时秘密字段脱敏 |
| `enabled`、`autoFetchModels` | 源启停、模型发现模式 |
| `manualModels` | 手工模型；包含 ID、名称、类型、能力、上下文长度及可用性 |
| `fetchBaseUrl` | 模型列表请求专用地址；空时使用源地址 |
| `refreshState` | 运行时任务状态，不是持久化配置 |
| `createdAt`、`updatedAt` | 时间戳 |

`refreshState` 包含 `refreshing`、`lastCount`、`lastAdded`、`lastRemoved`、`lastError`、`lastFinishedAt`，以及逐 Key 的 `lastKeys` 结果。拉取返回后需轮询此状态。

多 Key 条目结构：

```ts
interface SourceAPIKey {
  value: string
  note?: string
  disabled?: boolean
  fetchedModels?: string[]
  allowedModels?: string[]
}
```

`allowedModels` 未设置时使用拉取集；两者都未设置时不限制。显式空数组表示该 Key 不服务任何模型。不要把“缺省”和“空数组”合并。

## 模型

| 字段组 | 说明 |
| --- | --- |
| `id`、`sourceId` | 联合标识；模型 ID 可能含 `/` |
| `name`、`sourceName`、`baseUrl`、`platform`、`type` | 显示及来源信息 |
| `maxTokens`、`visionCapable`、`toolsCapable`、`structuredOutput`、`thinkingMode` | 能力与上下文信息 |
| `enabled` / `available` | 手动开关 / 健康状态；可调度需两者为真，同时满足源和路由约束 |
| `origin` | `fetched` 或 `manual`；手工行不被刷新覆盖 |
| `capabilitySource` | 空、`catalog` 或 `manual`；用户修改的能力在刷新时保留 |
| `lastCheckedAt` | 最近检查时间 |

目录未命中时保留可用的手工能力值。模型级能力可用于候选优选；组级能力用于请求约束，不应将两者混为一谈。

## 模型组

| 字段 | 语义 |
| --- | --- |
| `id`、`name`、`enabled` | 管理标识、对客户端公布的模型名、启停 |
| `models` | 成员引用数组，推荐 `sourceId:modelId` |
| `strategy` | `round-robin` / `sequential` / `random` |
| `maxRetries`、`retryInterval` | 重试配置；间隔为毫秒 |
| `maxConcurrency`、`dailyLimitMaxRequests`、`dailyLimitMaxTokens` | 并发及日限额；0 为不限 |
| `type`、`maxTokens`、`visionCapable`、`toolsCapable` | 组的模型类型和能力 |

删除组时，失去全部限定组授权的令牌会被禁用，不能把空列表解释成自动获得所有组权限。

## 访问令牌

`ApiToken` 包含 `name`、`token`、`enabled`、`allowedGroups`、`scopes`、`createdAt`、`updatedAt`。`newName` 仅用于改名请求。

- 列表与保存响应中的 `token` 脱敏；`reveal` 接口按需返回明文。
- 普通令牌的空 `allowedGroups` 表示不限制组；明确指定时按组名授权。
- `scopes` 包含 `agent` 时用于远程运维，不能调用推理接口；模型组授权与此作用域相互独立。
- 保存后清理表单中的明文。不要把脱敏结果作为新密钥重新提交。

## 用量

| 类型 | 内容 |
| --- | --- |
| `UsageStats` | `requests`、`success`、`failed`、`inputTokens`、`outputTokens`、`totalTokens`、`cacheHitTokens`、`cacheHitRate`、`avgDurationMs`、`avgFirstByteMs` |
| `UsageTrendPoint` | 本地日 `date`、请求成功/失败计数、token 总量与可选分项、`modelTokens` |
| `UsageModelStat` | `model`、`requests`、`failed`、`tokens` |
| `UsagePulseResult` | `points` 与 `window`；桶起点 `t` 为 Unix 毫秒，窗口包含请求数、平均耗时、P95、token |
| `UsageModelDailyPoint` | `date`、`model`、`requests`、`isOther` |
| `UsageLogsResult` | `{items, total}` |

`cacheHitRate` 范围为 `[0, 1]`。请求时间使用 RFC3339；时间筛选为 `[from, to)`，固定 UTC offset 控制按日聚合，不自动应用时区夏令时规则。

`UsageLogItem` 包含请求 ID、开始时间、调用方名称/哈希、原始请求模型组 `requestedModelGroup`、实际 `groupName` / `modelName`、源、协议、转换模式、状态、错误、流式标记、耗时、token 与截断标记。路由前失败时，实际命中模型可为空，但保留请求组名。

`UsageLogDetail` 还包含结束时间、组/模型 ID、端点、转换链、用量来源、警告、`usageDetail`、`builtinToolUsage`、`retryCount`、`retryEvents` 和四段正文：`incomingBody`、`outgoingBody`、`providerResponse`、`downstreamResponse`。`errorKind` 可标记 `client_canceled` 或 `upstream`。

正文结构：

```ts
interface UsageBody {
  content: string
  truncated: boolean
}
```

`content` 可能是 JSON 字符串，不保证可解析。捕获关闭时正文为空；媒体外置时包含 `__ELYSIA_ASSET__` 占位符。截断仅描述日志副本，不表示发给客户端的响应被截断。

`UsageQueryParams` 的多选字段 `keyNames`、`groupNames`、`modelNames`、`sourceIds` 经前端序列化为重复的单数查询参数。其余筛选见 [API](webui-api.md#用量)。

## 系统日志与健康

`SystemLog` 包含 `id`、`createdAt`、`level`、`message`、可选 `fields`；分页结果为 `{items, total}`。`Health` 包含 `status`、`database` 和 `memory.{alloc,sys,numGC}`，内存数值为字节。

## 协议与 Agent 类型

自定义协议类型集中在同一 `types.ts` 的 `CustomProtocol*` 定义。接口 schema 提供可配置字段目录；不要将完整内部模型误当作 HTTP 请求体。语义见[自定义协议参考](protocol-definition-reference.md)。

Agent 会话、消息、设置、pending action、图表和 SSE 结构见[远程接口](remote-agent-api.md)及 Agent 类型文件；持久化状态与当前运行状态需区分。
