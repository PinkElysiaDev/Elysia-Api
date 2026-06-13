# WebUI Data Model

本文档是前端实现 Elysia-API WebUI 的类型合同。WebUI 只调用新后端 `/api/admin/*`，不读取、不修改旧 Koishi `aggregator` / `orchestrator` 配置。

## Common Types

```ts
export interface ApiEnvelope<T> {
  ok: true
  data: T
}

export interface ApiErrorEnvelope {
  ok: false
  error: {
    code: string
    message: string
  }
}

export type ApiResult<T> = ApiEnvelope<T> | ApiErrorEnvelope
export type Platform = 'openai' | 'openai-compatible' | 'claude' | 'gemini'
export type ModelType = 'llm' | 'embedding' | 'reranker'
export type GroupStrategy = 'round-robin' | 'sequential' | 'random'
export type LogLevel = 'debug' | 'info' | 'warn' | 'error'
```

## Runtime Config

```ts
export interface RuntimeConfig {
  host: string
  port: number
  panelAccessTokenConfigured: boolean
  databasePath: string
  logLevel: LogLevel
  httpTimeout: number
}

export interface RuntimeConfigUpdate {
  host?: string
  port?: number
  logLevel?: LogLevel
  httpTimeout?: number
}
```

表单规则：`host` 默认 `127.0.0.1`；`port` 为 1-65535；`httpTimeout` 为非负整数；修改 `host` 或 `port` 后 UI 应提示需要重启。

## Model Source

```ts
export interface ManualModel {
  id: string
  name: string
  type?: ModelType
  maxTokens?: number
  visionCapable?: boolean
  toolsCapable?: boolean
  structuredOutput?: boolean
  thinkingMode?: 'both' | 'non-thinking-only' | 'thinking-only'
  available?: boolean
}

export interface ModelSource {
  id: string
  name: string
  baseUrl: string
  apiKey?: string
  platform: Platform
  enabled: boolean
  autoFetchModels: boolean
  manualModels?: ManualModel[]
  createdAt?: string
  updatedAt?: string
}
```

表单规则：`id` 可由后端从 `name` 自动生成；`name`、`baseUrl`、`platform` 必填；`apiKey` 输入框必须按 secret 处理；`autoFetchModels=false` 时显示 `manualModels` 表格。

## Model Cache

```ts
export interface Model {
  id: string
  name: string
  sourceId?: string
  sourceName?: string
  baseUrl: string
  platform: Exclude<Platform, 'openai-compatible'>
  type: ModelType
  maxTokens: number
  visionCapable: boolean
  toolsCapable: boolean
  structuredOutput: boolean
  thinkingMode: 'both' | 'non-thinking-only' | 'thinking-only'
  available: boolean
  lastCheckedAt: string
}
```

展示规则：默认按 `sourceName` 分组；禁用/不可用模型灰显；API key 不在列表中展示。

## Model Group

```ts
export interface ModelGroup {
  id: string
  name: string
  enabled: boolean
  models: string[]
  strategy: GroupStrategy
  maxRetries: number
  retryInterval: number
  maxConcurrency?: number
  dailyLimitMaxRequests?: number
  dailyLimitMaxTokens?: number
  type: ModelType
  maxTokens?: number
  visionCapable: boolean
  toolsCapable: boolean
}
```

表单规则：`name` 是客户端请求时看到的模型名；`models` 从 `/api/admin/models` 多选；`maxRetries`、`retryInterval` 为非负整数；限流字段为空或 0 表示不限制。

## API Token

```ts
export interface ApiToken {
  name: string
  token?: string
  enabled: boolean
  createdAt?: string
  updatedAt?: string
}
```

展示规则：列表中 `token` 已脱敏；创建/更新时允许输入明文；保存后不要在前端 state 中长期保留明文 token。

## Usage

```ts
export interface UsageStats {
  requests: number
  success: number
  failed: number
  inputTokens: number
  outputTokens: number
  totalTokens: number
  avgDurationMs: number
}

export interface UsageLogItem {
  requestId: string
  startedAt: string
  keyName: string
  keyHash: string
  groupName: string
  modelName: string
  platform: string
  sourceFormat: string
  targetFormat: string
  relayMode: string
  responsesMode: string
  usageSource: string
  stream: boolean
  statusCode: number
  error?: string
  firstByteMs: number
  durationMs: number
  inputTokens: number
  outputTokens: number
  totalTokens: number
  incomingBodyTruncated: boolean
  providerResponseTruncated: boolean
}
```

查询参数：`from`、`to`、`limit`、`offset`、`keyName`、`keyHash`、`groupName`、`modelName`、`statusCode`。

## System Logs and Health

```ts
export interface SystemLog {
  id: number
  createdAt: string
  level: LogLevel | string
  message: string
  fields?: string
}

export interface Health {
  status: 'ok'
  database: boolean
  memory: {
    alloc: number
    sys: number
    numGC: number
  }
}
```
