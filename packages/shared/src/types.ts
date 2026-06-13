// Shared types for Elysia-API plugins
// Both Koishi plugins and standalone WebUI frontend use these contracts.

export type PlatformType = 'openai' | 'claude' | 'gemini'
export type SourcePlatformType = PlatformType | 'openai-compatible'
export type ModelType = 'llm' | 'embedding' | 'reranker'
export type ThinkingMode = 'both' | 'non-thinking-only' | 'thinking-only'
export type ModelSourceType = 'auto' | 'manual'

export interface Model {
  // Identification
  id: string
  name: string

  // Source info
  source: ModelSourceType
  sourceName?: string  // For auto-fetched models

  // Connection
  baseUrl: string
  apiKey: string
  platform: PlatformType

  // Properties
  type: ModelType
  maxTokens: number

  // LLM-specific capabilities
  visionCapable: boolean
  toolsCapable: boolean
  structuredOutput: boolean
  thinkingMode: ThinkingMode

  // Status
  available: boolean
  lastChecked: Date
}

export interface ManualSourceModel {
  id: string
  name: string
}

export interface AggregatorSourceConfigBase {
  name: string
  baseUrl: string
  apiKey: string
  platform: SourcePlatformType
  enabled: boolean
}

export interface AutoFetchAggregatorSource extends AggregatorSourceConfigBase {
  autoFetchModels: true
}

export interface ManualAggregatorSource extends AggregatorSourceConfigBase {
  autoFetchModels: false
  manualModels: ManualSourceModel[]
}

export type AggregatorSourceConfig =
  | AutoFetchAggregatorSource
  | ManualAggregatorSource

// legacy aliases kept for aggregator internal migration/refactor convenience
export type AutoFetchSource = AutoFetchAggregatorSource

export interface ManualModel {
  id: string
  name: string
  baseUrl: string
  apiKey: string
  platform: PlatformType
  sourceName: string  // 源名称，用于标识模型来源
}

// ==========================================
// WebUI Specific Types & API Envelopes
// ==========================================

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
export type GroupStrategy = 'round-robin' | 'sequential' | 'random'
export type LogLevel = 'debug' | 'info' | 'warn' | 'error'

export interface WebuiRuntimeConfig {
  host: string
  port: number
  panelAccessTokenConfigured: boolean
  databasePath: string
  logLevel: LogLevel
  httpTimeout: number
}

export interface WebuiRuntimeConfigUpdate {
  host?: string
  port?: number
  logLevel?: LogLevel
  httpTimeout?: number
}

export interface WebuiManualModel {
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

export interface WebuiModelSource {
  id: string
  name: string
  baseUrl: string
  apiKey?: string
  platform: Platform
  enabled: boolean
  autoFetchModels: boolean
  manualModels?: WebuiManualModel[]
  createdAt?: string
  updatedAt?: string
}

export interface WebuiModelCache {
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
  thinkingMode: ThinkingMode
  available: boolean
  lastCheckedAt: string
}

export interface WebuiModelGroup {
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

export interface WebuiApiToken {
  name: string
  token?: string
  enabled: boolean
  createdAt?: string
  updatedAt?: string
}

export interface WebuiUsageStats {
  requests: number
  success: number
  failed: number
  inputTokens: number
  outputTokens: number
  totalTokens: number
  avgDurationMs: number
}

export interface WebuiUsageLogItem {
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

export interface WebuiSystemLog {
  id: number
  createdAt: string
  level: LogLevel | string
  message: string
  fields?: string
}

export interface WebuiHealth {
  status: 'ok'
  database: boolean
  memory: {
    alloc: number
    sys: number
    numGC: number
  }
}
