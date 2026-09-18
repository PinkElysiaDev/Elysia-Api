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
  usageLog?: UsageLogRuntimeConfig
  modelCatalog?: ModelCatalogInfo
}

/** 日志管理配置（/api/admin/usage 留存策略，运行配置页「日志管理」卡片）。 */
export interface UsageLogRuntimeConfig {
  /** 日志持久化总开关；false 时完全不落库。 */
  persistEnabled: boolean
  /** 过期清理天数；0 = 不启用。 */
  retentionDays: number
  /** 数据库占用上限（MB）；0 = 不限。 */
  maxStorageMB: number
  /** 保留记录条数上限；0 = 不限。 */
  maxRecords: number
  /** 单段请求体落库上限（KB）；0 = 不保存任何请求体。 */
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
  allowFakeIPOutbound?: boolean
  usageLog?: Partial<UsageLogRuntimeConfig>
  modelCatalog?: {
    syncIntervalMinutes?: number
  }
}

/** /api/admin/usage/storage：日志占用状态（设置页展示）。 */
export interface UsageStorageStatus {
  db: {
    totalBytes: number
    logicalBytes: number
    pageCount: number
    pageSize: number
    freePages: number
  }
  recordCount: number
  assets: { bytes: number; files: number; dirs: number }
  config: UsageLogRuntimeConfig
  lastCleanup?: {
    lastRunAt: string
    deletedByTTL: number
    deletedByRecords: number
    deletedBySize: number
    assetsRemoved: number
    vacuumed: boolean
    lastError?: string
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
  /** 模型列表拉取专用地址（方向5）：空 = 与 baseUrl 一致。 */
  fetchBaseUrl?: string
  /** 多 key 配置（方向6）：空 = 单 key（apiKey）。 */
  apiKeys?: SourceAPIKey[]
  keyStrategy?: SourceKeyStrategy
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
  /** 错误归类：conversion=协议转换失败、upstream=上游失败；空表示未归类。 */
  errorKind?: string
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
  sourceIds?: string[]
}

// ---- 协议设计器：字段级双向映射模型（backend/relay/custom_protocol_mapping.go）----

/** 协议任务类型：llm 为默认；reranker/embedding 为声明式预留；x- 前缀自定义扩展。 */
export type CustomProtocolType = 'llm' | 'reranker' | 'embedding' | `x-${string}` | ''

export interface CustomProtocolAuth {
  /** bearer（默认）/ none / header / query */
  mode?: string
  header?: string
  prefix?: string
  query?: string
}

/** 请求体字段引用叶子：声明该字段对应 Maheshvara 的哪个请求字段。 */
export interface CustomProtocolBodyFieldRef {
  field: string
  /** json（默认）：原生 JSON 值插入；string：字符串插入 */
  mode?: 'string' | 'json'
  /** Maheshvara 侧缺失或为空时的兜底值 */
  default?: unknown
  /** 渲染后为空则删除该键 */
  omitIfEmpty?: boolean
  /** 渲染后值类型化等于该字面量则删除该键（覆盖 false/0 场景） */
  omitIf?: unknown
  /** 条件成立才写入该键（对 Maheshvara 请求求值） */
  when?: CustomProtocolMatch
}

/** 常量叶子：上游必填但 Maheshvara 无对应的固定值。 */
export interface CustomProtocolBodyConstant {
  value: unknown
}

export type CustomProtocolBodyLeaf = CustomProtocolBodyFieldRef | CustomProtocolBodyConstant

/**
 * 请求体构造树（结构即配置）：容器为普通 JSON 对象/数组；叶子为字段引用或
 * 常量。注册时由后端编译为渲染模板。
 */
export type CustomProtocolBodyTree =
  | CustomProtocolBodyLeaf
  | { [key: string]: CustomProtocolBodyTree }
  | CustomProtocolBodyTree[]

/** 响应字段映射行：上游响应路径 → Maheshvara 响应字段。 */
export interface CustomProtocolResponseFieldMapping {
  /** 上游响应路径（点路径，支持数组下标，如 choices[0].delta.content） */
  path: string
  /** Maheshvara 响应字段（见 schema.responseFields，如 text / usage.input_tokens） */
  field: string
  transform?: string
}

/**
 * 通用条件原语：在载荷上按点路径取值，与任意类型的预期值做类型化比较。
 * op = nonEmpty(默认)/equals/notEquals/in/notIn/contains/isNull/notNull/
 * isTrue/isFalse/gt/gte/lt/lte；isTrue/isFalse 对缺失字段按 false。
 */
export interface CustomProtocolMatch {
  path: string
  op?: string
  value?: unknown
}

/** 流终止值：raw（文本字面量）与 json（类型化值）二选一。 */
export interface CustomProtocolDoneValue {
  raw?: string
  json?: unknown
}

export interface CustomProtocolStreamMapping {
  payloadPath?: string
  /** delta（默认，事件即增量）/ cumulative（事件为累计全文） */
  mode?: string
  doneValues?: string[]
  /** 类型化终止值（按解析后的载荷值匹配，如 {json: true}） */
  done?: CustomProtocolDoneValue[]
  /** 移除默认 [DONE] 终止值 */
  doneValuesReplace?: boolean
  events?: string[]
  /** JSON 载荷内事件名判别键，默认 ["type","event"] */
  eventKeys?: string[]
  /** 终止判定覆盖（缺省沿用 finishReasonPath 字符串化非空） */
  finishWhen?: CustomProtocolMatch
  /** 完成状态判定覆盖（缺省沿用 status=="completed"） */
  statusWhen?: CustomProtocolMatch
  /** 按字段族覆盖全局 mode（text/reasoning/arguments 各自 delta|cumulative） */
  modes?: { text?: string; reasoning?: string; arguments?: string }
  /** 异构帧逐帧映射（声明后优先于 events 白名单） */
  frames?: CustomProtocolStreamFrame[]
  response?: CustomProtocolResponse
}

/** 帧级工具拼装规则：身份帧给 id/name，参数帧给增量参数片段；按 idPath 或 indexPath 关联。 */
export interface CustomProtocolStreamTool {
  idPath?: string
  indexPath?: string
  namePath?: string
  argumentsPath?: string
  /** delta（默认，片段原样追加）/ cumulative（片段为累计快照） */
  argumentsMode?: string
}

/** 异构流的一类帧的映射规则：按事件名与/或 JSON 谓词匹配，命中即用该帧自己的映射。 */
export interface CustomProtocolStreamFrame {
  /** SSE event 字段（或 eventKeys 判别键）；与 match 同给时须同时成立 */
  event?: string
  /** 帧 JSON 谓词（无事件名协议如 Gemini data-only 帧） */
  match?: CustomProtocolMatch
  payloadPath?: string
  /** 分帧工具调用拼装（content_block_start/input_json_delta 等） */
  tool?: CustomProtocolStreamTool
  response?: CustomProtocolResponse
  /** 命中即判定流终态（response.completed / message_stop 型收尾） */
  terminal?: boolean
}

/** 返回体构造树叶子的映射标注 */
export interface CustomProtocolResponseBodyLeaf {
  /** Maheshvara 响应字段（见 schema.responseFields） */
  field?: string
  /** 示例值（便于理解结构，不参与运行时） */
  value?: unknown
  transform?: string
}

/**
 * 返回体构造树（结构即配置）：容器为普通 JSON 对象/数组；叶子为映射标注
 * （含 field）或纯结构占位（裸标量 / {value: ...}）。
 */
export type CustomProtocolResponseBodyTree =
  | CustomProtocolResponseBodyLeaf
  | { [key: string]: CustomProtocolResponseBodyTree }
  | CustomProtocolResponseBodyTree[]

export interface CustomProtocolResponse {
  /** 返回体构造树（新模型，与 fields 二选一） */
  body?: CustomProtocolResponseBodyTree
  /** textPath 指向对象数组时按元素过滤再提取（如分离 thinking/text 块） */
  textFilter?: CustomProtocolMatch
  /** reasoningPath 指向对象数组时按元素过滤再提取 */
  reasoningFilter?: CustomProtocolMatch
  /** 字段级映射行表（新模型，UI 与 AI 产出；与 body 二选一） */
  fields?: CustomProtocolResponseFieldMapping[]
  /** 上游示例响应原文（供点选与离线验证） */
  sample?: unknown
  stream?: CustomProtocolStreamMapping
  /* ---- legacy 直接路径（后端兼容读取，UI 不再产出） ---- */
  idPath?: string
  modelPath?: string
  statusPath?: string
  textPath?: string
  reasoningPath?: string
  toolCallsPath?: string
  usagePath?: string
  finishReasonPath?: string
  errorPath?: string
  fieldMappings?: { target: string; source?: string; value?: unknown; default?: unknown; transform?: string; omitIfEmpty?: boolean }[]
}

export interface CustomProtocolRequest {
  method?: string
  path?: string
  /** 流式请求的路径覆盖（按流切换动词的端点，如 Gemini） */
  pathStream?: string
  /** 消息/工具线制形状（openai-chat/anthropic/gemini/responses）——复用内置整形器 */
  shape?: string
  headers?: Record<string, string>
  query?: Record<string, string>
  contentType?: string
  auth?: CustomProtocolAuth
  /** 字段级构造树（新模型；与 legacy bodyTemplate 二选一） */
  body?: CustomProtocolBodyTree
  /* ---- legacy ---- */
  bodyTemplate?: string
  submitBody?: string
  omitIfEmpty?: string[]
}

/**
 * 模型列表发现端点：声明后 custom:<id> 模型源可开启自动拉取。请求构造与
 * 鉴权注入复用协议管线（auth 缺省继承 request.auth），响应以 listPath 定位
 * 模型数组，idPath/namePath 在元素内取标识与展示名。
 */
export interface CustomProtocolModels {
  /** 默认 GET，仅 GET/POST */
  method?: string
  /** 相对源 baseUrl 的路径，如 /v1/models */
  path: string
  headers?: Record<string, string>
  query?: Record<string, string>
  auth?: CustomProtocolAuth
  /** 点路径到模型数组，如 data / output.models */
  listPath: string
  /** 元素内标识路径，默认 id */
  idPath?: string
  /** 元素内展示名路径 */
  namePath?: string
}

/** 提取阶段键名别名覆盖：提供即整体替换该类默认表；usage/toolCall 条目支持点路径。 */
export interface CustomProtocolAliases {
  /** 文本提取魔键（默认 text/content/message/value/output） */
  textKeys?: string[]
  /** input/output/total/cached/reasoning → 键列表 */
  usage?: Record<string, string[]>
  /** id/name/arguments → 键列表 */
  toolCall?: Record<string, string[]>
}

export interface CustomProtocolConfig {
  id: string
  name?: string
  version?: string
  type?: CustomProtocolType
  request: CustomProtocolRequest
  response?: CustomProtocolResponse
  models?: CustomProtocolModels
  aliases?: CustomProtocolAliases
  metadata?: Record<string, unknown>
}

/** 字段目录条目（GET /api/admin/custom-protocols/schema）。 */
export interface MaheshvaraFieldSpec {
  name: string
  label: string
  /** string（文本）/ native（对象/数组）/ scalar（数字/布尔） */
  shape: string
  group: string
}

export interface CustomProtocolSchema {
  requestFields: MaheshvaraFieldSpec[]
  responseFields: MaheshvaraFieldSpec[]
  transforms: string[]
  modes: string[]
  types: { value: string; label: string; hint: string }[]
}

export interface CustomProtocolSummary {
  id: string
  name?: string
  version?: string
  type: CustomProtocolType
  valid: boolean
  error?: string
  config: CustomProtocolConfig
  updatedAt?: string
}

export interface CustomProtocolPreviewResult {
  method: string
  path: string
  query?: Record<string, string>
  headers: Record<string, string>
  contentType: string
  body?: string
  authPreview: string
}

export interface CustomProtocolStreamEventSample {
  event: string
  data: string
}

export interface CustomProtocolTestResult {
  statusCode: number
  durationMs: number
  targetModel: string
  stream: boolean
  rawBody?: string
  maheshvara?: unknown
  mappingError?: string
  streamError?: string
  events?: CustomProtocolStreamEventSample[]
  decoded?: unknown[]
}

/** 模型发现试拉结果（/custom-protocols/test-models）。 */
export interface CustomProtocolModelsTestResult {
  statusCode: number
  durationMs: number
  models?: { id: string; name: string }[]
  rawBody?: string
  parseError?: string
}

export interface CustomProtocolAssistDocument {
  name: string
  mime?: string
  /** 纯文本输入（粘贴内容/文本文件） */
  text?: string
  /** data: URL（图片与 PDF 等二进制文档，交给模型原生多模态能力） */
  dataUrl?: string
}

/** 离线验证结果：样例请求渲染 + 示例响应映射，不发起真实请求。 */
export interface CustomProtocolAssistVerification {
  request?: CustomProtocolPreviewResult
  requestError?: string
  mappedResponse?: unknown
  mappingError?: string
}

export interface CustomProtocolAssistResult {
  reply: string
  model: string
  durationMs: number
  /** 生成轮次（含修复轮） */
  rounds?: number
  config?: CustomProtocolConfig
  valid?: boolean
  issues?: string
  verification?: CustomProtocolAssistVerification
  usage?: { inputTokens?: number; outputTokens?: number; totalTokens?: number }
}
