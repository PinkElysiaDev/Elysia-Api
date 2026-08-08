// 类型合同：与 backend storage 类型 + docs/webui-data-model.md 严格对齐。

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

// API 协议（线路 API），与上游 wire API 一一对应。旧值 openai/openai-compatible/claude
// 仍可能出现在存量数据里，后端读取时会归一化；类型保留它们避免存量源在下拉里显示空。
export type Platform =
  | 'responses'
  | 'chat_completions'
  | 'anthropic'
  | 'gemini'
  | 'openai'
  | 'openai-compatible'
  | 'claude'
  | `custom:${string}`
export type ModelType = 'llm' | 'embedding' | 'reranker'
export type GroupStrategy = 'round-robin' | 'sequential' | 'random'
export type LogLevel = 'debug' | 'info' | 'warn' | 'error'
export type ThinkingMode = 'both' | 'non-thinking-only' | 'thinking-only'

export interface RuntimeConfig {
  host: string
  port: number
  panelAccessToken: string
  databasePath: string
  defaultDatabasePath: string
  logLevel: LogLevel
  httpTimeout: number
  enablePprof: boolean
  allowFakeIPOutbound: boolean
}

export interface RuntimeConfigUpdate {
  host?: string
  port?: number
  logLevel?: LogLevel
  httpTimeout?: number
  panelAccessToken?: string
  databasePath?: string
  enablePprof?: boolean
  allowFakeIPOutbound?: boolean
}

export interface RuntimeConfigUpdateResult {
  updated: boolean
  restartRequired: boolean
}

export interface ManualModel {
  id: string
  name: string
  type?: ModelType
  maxTokens?: number
  visionCapable?: boolean
  toolsCapable?: boolean
  structuredOutput?: boolean
  thinkingMode?: ThinkingMode
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

export interface Model {
  id: string
  name: string
  sourceId?: string
  sourceName?: string
  baseUrl: string
  platform: Exclude<Platform, 'openai-compatible'> | Platform
  type: ModelType
  maxTokens: number
  visionCapable: boolean
  toolsCapable: boolean
  structuredOutput: boolean
  thinkingMode: ThinkingMode
  available: boolean
  lastCheckedAt: string
}

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

export interface ApiToken {
  name: string
  token?: string
  enabled: boolean
  allowedGroups?: string[]
  createdAt?: string
  updatedAt?: string
}

export interface UsageStats {
  requests: number
  success: number
  failed: number
  inputTokens: number
  outputTokens: number
  totalTokens: number
  cacheHitTokens: number
  cacheHitRate: number
  avgDurationMs: number
  avgFirstByteMs: number
  firstUsedAt?: string
  lastUsedAt?: string
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

export interface UsageLogsResult {
  total: number
  items: UsageLogItem[]
}

/** 单段链路内容（请求体 / 响应体），content 可能是 JSON 字符串。 */
export interface UsageBody {
  content: string
  truncated: boolean
}

export interface UsageTokenUsage {
  inputTokens?: number
  outputTokens?: number
  totalTokens?: number
  cacheHitTokens?: number
  estimatedTokens?: number
  estimated?: boolean
}

export interface UsageRetryEvent {
  attempt: number
  model: string
  error?: string
}

/** GET /usage/logs/:id 返回的完整记录，含四段链路原文。 */
export interface UsageLogDetail {
  requestId: string
  startedAt: string
  endedAt: string
  keyName: string
  keyHash: string
  requestedModelGroup?: string
  groupId?: string
  groupName: string
  modelId?: string
  modelName: string
  platform: string
  inputFormat?: string
  sourceFormat?: string
  targetFormat?: string
  sourceEndpoint?: string
  targetEndpoint?: string
  relayMode?: string
  responsesMode?: string
  conversionChain?: string[]
  usageSource?: string
  requestWarnings?: string[]
  stream: boolean
  statusCode: number
  error?: string
  firstByteMs: number
  durationMs: number
  usage: UsageTokenUsage
  usageDetail?: Record<string, unknown>
  builtinToolUsage?: Record<string, number>
  retryCount: number
  retryEvents?: UsageRetryEvent[]
  incomingBody: UsageBody
  outgoingBody: UsageBody
  providerResponse: UsageBody
  downstreamResponse: UsageBody
}

export interface SystemLog {
  id: number
  createdAt: string
  level: LogLevel | string
  message: string
  fields?: string
}

export interface SystemLogsResult {
  total: number
  items: SystemLog[]
}

export interface Health {
  status: string
  database: boolean
  memory: {
    alloc: number
    sys: number
    numGC: number
  }
}

export interface UsageQueryParams {
  from?: string
  to?: string
  limit?: number
  offset?: number
  keyName?: string
  keyHash?: string
  groupName?: string
  modelName?: string
  statusCode?: number
  // 多选筛选：非空时后端按 IN (...) 匹配，优先于对应单值字段。
  keyNames?: string[]
  groupNames?: string[]
  modelNames?: string[]
}
