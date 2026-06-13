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

export type Platform = 'openai' | 'openai-compatible' | 'claude' | 'gemini'
export type ModelType = 'llm' | 'embedding' | 'reranker'
export type GroupStrategy = 'round-robin' | 'sequential' | 'random'
export type LogLevel = 'debug' | 'info' | 'warn' | 'error'
export type ThinkingMode = 'both' | 'non-thinking-only' | 'thinking-only'

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

export interface UsageLogsResult {
  total: number
  items: UsageLogItem[]
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
}
