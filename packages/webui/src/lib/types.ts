// 类型合同：与 backend storage 类型 + docs/webui-data-model.md 严格对齐。

import type { ConversionIssue } from './protocol-v2'

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

/** 出站网络策略：禁止拨号的 IP 段（CIDR 列表）；空数组 = 全放行。 */
export interface OutboundConfig {
  deniedIpRanges: string[]
  /** 预置默认段（恢复默认按钮用）。 */
  defaultDeniedIpRanges?: string[]
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
  outbound?: OutboundConfig
  usageLog: UsageLogRuntimeConfig
  systemLog: LogRetentionConfig
  modelCatalog?: ModelCatalogInfo
  agentRemote?: AgentRemoteRuntimeConfig
}

/** AI 助手远程暴露面（REST/MCP/A2A）的运行时配置。 */
export interface AgentRemoteRuntimeConfig {
  /** 三面总开关（false = 全部 404）；缺省视为 true。 */
  enabled?: boolean
  /** 对外基础地址（反代后填），Agent Card 绝对 URL 用；空 = 按访问地址推导。 */
  publicUrl?: string
}

/** 日志管理配置（/api/admin/usage 留存策略，运行配置页「日志管理」卡片）。 */
export interface UsageLogRuntimeConfig {
  /** 日志持久化总开关；false 时完全不落库。 */
  persistEnabled: boolean
  /** 过期清理天数；0 = 不启用。 */
  retentionDays: number
  /** 日志内容与去重媒体的预算（MiB）；0 = 不限，不含汇总和索引。 */
  maxContentMB: number
  /** 保留记录条数上限；0 = 不限。 */
  maxRecords: number
  /** 单段请求/响应正文落库上限（KB）；默认 0 不保存正文，正数显式开启。 */
  bodyMaxKB: number
  /** 仅失败请求保留请求体。 */
  bodyOnErrorOnly: boolean
  /** base64 媒体外置为文件 + 占位符。 */
  externalizeMedia: boolean
  /** 清理巡检周期（分钟）。 */
  cleanupIntervalMinutes: number
}

export interface RuntimeConfigUpdate {
  host?: string
  port?: number
  logLevel?: LogLevel
  httpTimeout?: number
  panelAccessToken?: string
  databasePath?: string
  enablePprof?: boolean
  outbound?: { deniedIpRanges: string[] }
  usageLog?: Partial<UsageLogRuntimeConfig>
  systemLog?: Partial<LogRetentionConfig>
  modelCatalog?: {
    syncIntervalMinutes?: number
  }
  agentRemote?: {
    enabled?: boolean
    /** 显式空串 = 清空 publicUrl；undefined = 不修改。 */
    publicUrl?: string
  }
}

export interface LogRetentionConfig {
  retentionDays: number
  maxRecords: number
  maxContentMB: number
}

export interface LogMaintenance {
  state: 'idle' | 'queued' | 'running' | 'waiting' | 'completed' | 'failed'
  phase: 'idle' | 'retention' | 'assets' | 'reclaim' | 'checkpoint'
  pending: boolean
  lastRunAt: string
  finishedAt?: string
  usageDeleted: { byTTL: number; byRecords: number; byContent: number }
  systemDeleted: { byTTL: number; byRecords: number; byContent: number }
  assetsRemoved: number
  remainingFreePages: number
  checkpointBlocked: boolean
  lastError?: string
}

export interface UsageStorageStatus {
  db: {
    fileBytes: number
    usedBytes: number
    freeBytes: number
    walBytes: number
    rollupBytes: number
    indexBytes: number
    pageOverheadBytes: number
    freePages: number
  }
  content: {
    usageBytes: number
    usageRecords: number
    systemBytes: number
    systemRecords: number
    mediaBytes: number
    pendingMediaBytes: number
    mediaFiles: number
  }
  assets: { bytes: number; files: number; dirs: number }
  usageLog: UsageLogRuntimeConfig
  systemLog: LogRetentionConfig
  maintenance?: LogMaintenance
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

/** 多 key 配置中的一条（源级密钥集合）。 */
export interface SourceAPIKey {
  value: string
  note?: string
  disabled?: boolean
  /** 该 key 上次独立拉取到的模型集（权限自动发现结果，勾选界面的展示宇宙）。 */
  fetchedModels?: string[]
  /** 用户勾选启用的模型子集；undefined = 未勾选过 = 全部启用。 */
  allowedModels?: string[]
}

/** 源的后台拉取任务状态（后端运行时叠加，不落库）。 */
export interface SourceRefreshState {
  refreshing: boolean
  lastCount?: number
  lastAdded?: number
  lastRemoved?: number
  lastError?: string
  lastFinishedAt?: string
  lastKeys?: { index: number; note?: string; count: number; error?: string }[]
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
  /** 模型列表拉取专用地址（手动启停开关）：空 = 与 baseUrl 一致。 */
  fetchBaseUrl?: string
  /** 多 key 配置（源级密钥集合）：空 = 单 key（apiKey）。 */
  apiKeys?: SourceAPIKey[]
  keyStrategy?: SourceKeyStrategy
  /** 源级缓存断点合成：目标支持 cache.breakpoints 且调用方未打满时补结构断点。默认关闭。 */
  cacheSynthesis?: boolean
  /** 后台拉取任务状态（轮询进度与最近结果）。 */
  refreshState?: SourceRefreshState
  createdAt?: string
  updatedAt?: string
}

export interface Model {
  id: string
  name: string
  sourceId?: string
  sourceName?: string
  baseUrl: string
  platform: Platform
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
  /** 端点作用域：agent=远程访问 Key（AI 助手专用，不参与推理）；空=仅推理。 */
  scopes?: string[]
  /** 仅重命名请求携带：非空且 ≠ name 时服务端执行改名（请求专用字段）。 */
  newName?: string
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
  /** 命中率是否可信：false 表示分母（input）缺失或分子>分母，展示应降级为「—」。 */
  cacheHitRateReliable?: boolean
  /** 缓存创建 token 合计（仅含上游上报的行）。 */
  cacheCreationTokens?: number
  /** 上报了创建计数的成功记录占比（0–1）；低覆盖率时该合计不代表全部流量。 */
  cacheCreationCoverage?: number
  avgDurationMs: number
  avgFirstByteMs: number
}

export interface UsageLogItem {
  requestedModelGroup: string
  requestId: string
  startedAt: string
  keyName: string
  keyHash: string
  groupName: string
  modelName: string
  sourceId?: string
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
  /** 命中 prompt 缓存的输入 token 数（缓存命中展示用；0/缺省表示无命中）。 */
  cacheHitTokens?: number
  /** 缓存创建 token 数；仅当 usageReportMask 的 UsageReportCreation 位置位时才代表上游上报值。 */
  cacheCreationTokens?: number
  /** usage_report_mask：区分「上游未上报」与「上报为零」；历史行缺省视为未上报。 */
  usageReportMask?: number
  incomingBodyTruncated: boolean
  providerResponseTruncated: boolean
}

/** 趋势图聚合行（含细分请求数、tokens 与模型级消耗字典）。 */
export interface UsageTrendPoint {
  date: string
  requests: number
  successRequests: number
  failedRequests: number
  inputTokens?: number
  outputTokens?: number
  cacheHitTokens?: number
  tokens: number
  modelTokens?: Record<string, number>
}

/** 按模型聚合行（热门模型 / 明细表）。 */
export interface UsageModelStat {
  model: string
  requests: number
  failed: number
  tokens: number
}

/** 短窗脉搏单桶（t 为桶起点 Unix 毫秒）。 */
export interface UsagePulsePoint {
  t: number
  requests: number
  avgDurationMs: number
  p95DurationMs: number
  totalTokens?: number
}

/** 整段脉搏窗口汇总。P95：样本 ≤ 16384 时精确，超出为蓄水池估算；不是桶 P95 均值。 */
export interface UsagePulseWindow {
  requests: number
  avgDurationMs: number
  p95DurationMs: number
  totalTokens: number
}

export interface UsagePulseResult {
  points: UsagePulsePoint[]
  window: UsagePulseWindow
}

/** 本地日 × 模型请求数。isOther 为 Top N 之外的合计，展示文案由前端决定。 */
export interface UsageModelDailyPoint {
  date: string
  model: string
  requests: number
  isOther?: boolean
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
  /** 缓存创建 token 数；详情载荷为指针语义，缺省（omitempty）即上游未上报，显式 0 为上报为零。 */
  cacheCreationTokens?: number
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
  sourceId?: string
  requestId: string
  startedAt: string
  endedAt: string
  keyName: string
  keyHash: string
  requestedModelGroup: string
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
  /** 错误归类：client_canceled=客户端取消、upstream=上游失败；空表示未归类。 */
  errorKind?: string
  firstByteMs: number
  durationMs: number
  usage: UsageTokenUsage
  usageDetail?: Record<string, unknown>
  builtinToolUsage?: Record<string, number>
  /** 转发诊断：缓存创建 TTL 分桶被目标协议省略但总量保留时在此留 warning（见后端 conversionIssues）。 */
  conversionIssues?: ConversionIssue[]
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
  sourceIds?: string[]
}

// ---- 协议设计器：字段级双向映射模型（backend/relay/custom_protocol_mapping.go）----

/** 协议任务类型：llm 为默认；reranker/embedding 为声明式预留；x- 前缀自定义扩展。 */
/** 新建模型组的默认值：建组表单与快捷建组对话框共用，防两处漂移。 */
export function defaultGroup(): ModelGroup {
  return {
    id: '',
    name: '',
    enabled: true,
    models: [],
    strategy: 'round-robin',
    maxRetries: 3,
    retryInterval: 1000,
    maxConcurrency: 0,
    dailyLimitMaxRequests: 0,
    dailyLimitMaxTokens: 0,
    type: 'llm',
    maxTokens: 0,
    visionCapable: false,
    toolsCapable: false,
  }
}
