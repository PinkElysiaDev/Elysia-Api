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
/** 源级多 key 调度策略：single（单 key）/ round-robin / random / priority（按列表顺序）。 */
export type SourceKeyStrategy = 'single' | 'round-robin' | 'random' | 'priority'

export interface ModelCatalogInfo {
  enabled: boolean
  url: string
  proxy?: string
  /** 刷新周期（分钟），0 = 默认 1440（24 小时）。 */
  syncIntervalMinutes: number
}

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
  modelCatalog?: ModelCatalogInfo
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
  modelCatalog?: {
    syncIntervalMinutes?: number
  }
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

/** 多 key 配置中的一条（方向6）。 */
export interface SourceAPIKey {
  value: string
  note?: string
  disabled?: boolean
  /** 该 key 上次独立拉取到的模型集（权限自动发现结果，勾选界面的展示宇宙）。 */
  fetchedModels?: string[]
  /** 用户勾选启用的模型子集；undefined = 未勾选过 = 全部启用。 */
  allowedModels?: string[]
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
  /** 模型列表拉取专用地址（方向5）：空 = 与 baseUrl 一致。 */
  fetchBaseUrl?: string
  /** 多 key 配置（方向6）：空 = 单 key（apiKey）。 */
  apiKeys?: SourceAPIKey[]
  keyStrategy?: SourceKeyStrategy
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
  /** 用户手动启停（方向4），与 available（健康检测自动）分离：可调度 = enabled && available。 */
  enabled: boolean
  /** 行来源：fetched（随刷新合并替换）/ manual（刷新永不触碰）。 */
  origin?: 'fetched' | 'manual'
  /** 能力字段填充来源：''（默认）/ 'catalog'（models.dev 回填）/ 'manual'（用户编辑，刷新保留）。 */
  capabilitySource?: '' | 'catalog' | 'manual'
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

/** 趋势图单日聚合行（date 为浏览器传入固定 UTC offset 后的本地日）。 */
export interface UsageTrendPoint {
  date: string
  requests: number
  tokens: number
}

/** 按模型聚合行（热门模型 / 明细表）。 */
export interface UsageModelStat {
  model: string
  requests: number
  failed: number
  tokens: number
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
  status?: 'success' | 'failed'
  statusCode?: number
  // 多选筛选：非空时后端按 IN (...) 匹配，优先于对应单值字段。
  keyNames?: string[]
  groupNames?: string[]
  modelNames?: string[]
}
