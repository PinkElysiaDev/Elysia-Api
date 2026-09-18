import { clearToken, getToken } from './auth'
import type {
  ApiResult,
  UsageTrendPoint,
  UsageModelStat,
  UsagePulseResult,
  UsageModelDailyPoint,
  ApiToken,
  CustomProtocolAssistDocument,
  CustomProtocolAssistResult,
  CustomProtocolConfig,
  CustomProtocolPreviewResult,
  CustomProtocolSchema,
  CustomProtocolSummary,
  CustomProtocolTestResult,
  CustomProtocolModelsTestResult,
  Health,
  Model,
  ModelGroup,
  ModelSource,
  RuntimeConfig,
  RuntimeConfigUpdate,
  RuntimeConfigUpdateResult,
  SystemLogsResult,
  UsageLogDetail,
  UsageLogsResult,
  UsageQueryParams,
  UsageStats,
  UsageStorageStatus,
} from './types'

export class ApiError extends Error {
  code: string
  status: number
  constructor(code: string, message: string, status: number) {
    super(message)
    this.name = 'ApiError'
    this.code = code
    this.status = status
  }
}

/** 会话失效统一出口：清除本地令牌并抛 unauthorized（页面层订阅后踢回登录页）。 */
function throwUnauthorized(): never {
  clearToken()
  throw new ApiError('unauthorized', '认证已失效，请重新登录', 401)
}

const ADMIN_BASE = '/api/admin'

type QueryValue = string | number | boolean | undefined | null | string[]

interface RequestOptions {
  method?: string
  body?: unknown
  query?: Record<string, QueryValue>
  signal?: AbortSignal
}

function buildUrl(path: string, query?: RequestOptions['query']): string {
  const url = `${ADMIN_BASE}${path}`
  if (!query) return url
  const params = new URLSearchParams()
  for (const [key, value] of Object.entries(query)) {
    if (value === undefined || value === null || value === '') continue
    // 数组值展开为重复参数（?key=a&key=b），对应后端 c.QueryArray。
    if (Array.isArray(value)) {
      for (const item of value) {
        if (item === undefined || item === null || item === '') continue
        params.append(key, String(item))
      }
      continue
    }
    params.set(key, String(value))
  }
  const qs = params.toString()
  return qs ? `${url}?${qs}` : url
}

export async function request<T>(path: string, options: RequestOptions = {}): Promise<T> {
  const token = getToken()
  const headers: Record<string, string> = {}
  if (token) headers.Authorization = `Bearer ${token}`
  if (options.body !== undefined) headers['Content-Type'] = 'application/json'

  let response: Response
  try {
    response = await fetch(buildUrl(path, options.query), {
      method: options.method ?? 'GET',
      headers,
      body: options.body !== undefined ? JSON.stringify(options.body) : undefined,
      signal: options.signal,
    })
  } catch (err) {
    // SWR 等调用方中止请求属正常取消，原样上抛，避免被当成网络错误落进错误态。
    if (err instanceof DOMException && err.name === 'AbortError') throw err
    throw new ApiError('network_error', (err as Error).message || '网络请求失败', 0)
  }

  if (response.status === 401) {
    throwUnauthorized()
  }

  let payload: ApiResult<T> | { error?: string } | null = null
  const text = await response.text()
  if (text) {
    try {
      payload = JSON.parse(text)
    } catch {
      payload = null
    }
  }

  if (payload && typeof payload === 'object' && 'ok' in payload) {
    if (payload.ok) return (payload as { data: T }).data
    const err = (payload as { error: { code: string; message: string } }).error
    throw new ApiError(err?.code ?? 'error', err?.message ?? '请求失败', response.status)
  }

  if (!response.ok) {
    const message =
      (payload && typeof payload === 'object' && 'error' in payload && (payload as { error?: string }).error) ||
      `请求失败（${response.status}）`
    throw new ApiError('http_error', String(message), response.status)
  }

  return payload as T
}

/** 用 panel token 校验登录：命中受保护端点即视为有效。
 * 失败时抛 ApiError 并区分令牌无效与服务异常，由登录页分流展示文案。 */
export async function verifyToken(token: string): Promise<void> {
  let response: Response
  try {
    response = await fetch(`${ADMIN_BASE}/health`, {
      headers: { Authorization: `Bearer ${token}` },
      signal: AbortSignal.timeout(15_000),
    })
  } catch (err) {
    if (err instanceof DOMException && err.name === 'TimeoutError') {
      throw new ApiError('timeout', '连接后端超时，请检查网络与服务状态', 0)
    }
    throw new ApiError('network_error', '无法连接到后端，请检查网络与服务状态', 0)
  }
  if (response.status === 401) {
    throw new ApiError('unauthorized', 'Token 无效，请确认与后端 config.json 中的 panelAccessToken 一致', 401)
  }
  if (!response.ok) {
    throw new ApiError('http_error', `后端服务异常（HTTP ${response.status}），请检查服务状态后重试`, response.status)
  }
}

interface ListEnvelope<T> {
  items: T[]
}

export const api = {
  health: () => request<Health>('/health'),

  runtimeConfig: () => request<RuntimeConfig>('/runtime-config'),
  updateRuntimeConfig: (body: RuntimeConfigUpdate) =>
    request<RuntimeConfigUpdateResult>('/runtime-config', { method: 'PUT', body }),
  reload: () => request<unknown>('/reload', { method: 'POST' }),

  listSources: () => request<ListEnvelope<ModelSource>>('/model-sources').then((r) => r.items),
  createSource: (body: ModelSource) => request<ModelSource>('/model-sources', { method: 'POST', body }),
  updateSource: (id: string, body: ModelSource) =>
    request<ModelSource>(`/model-sources/${encodeURIComponent(id)}`, { method: 'PUT', body }),
  /** 仅切换源启停：轻量端点，不触发整源保存附带的模型自动同步。 */
  setSourceEnabled: (id: string, enabled: boolean) =>
    request<{ updated: boolean; enabled: boolean }>(`/model-sources/${encodeURIComponent(id)}/enabled`, {
      method: 'PATCH',
      body: { enabled },
    }),
  deleteSource: (id: string) =>
    request<{ deleted: boolean }>(`/model-sources/${encodeURIComponent(id)}`, { method: 'DELETE' }),
  /** 发起源的后台模型拉取：立即返回，进度与结果经源列表的 refreshState 轮询。 */
  fetchSource: (id: string) =>
    request<{ started: boolean; alreadyRunning?: boolean }>(
      `/model-sources/${encodeURIComponent(id)}/fetch`,
      { method: 'POST' },
    ),

  modelCatalogStatus: () =>
    request<{
      enabled: boolean
      url: string
      entries: number
      syncIntervalMinutes?: number
      /** 数据来源：snapshot（内置快照）/ cache（落盘缓存）/ network（在线更新）。 */
      source?: string
      sourceURL?: string
      lastSync?: string
      lastError?: string
    }>('/model-catalog/status'),
  modelCatalogRefresh: () =>
    request<{
      refreshed: boolean
      status: {
        enabled: boolean
        url: string
        entries: number
        syncIntervalMinutes?: number
        source?: string
        sourceURL?: string
        lastSync?: string | null
        lastError?: string
      }
    }>('/model-catalog/refresh', { method: 'POST' }),

  listModels: (params?: { sourceId?: string; search?: string }) =>
    request<ListEnvelope<Model>>('/models', {
      query: params && { sourceId: params.sourceId, search: params.search },
    }).then((r) => r.items),
  /** 为所有启用源发起后台拉取：立即返回启动数量，进度见各源 refreshState。 */
  refreshModels: () =>
    request<{ started: number; total: number }>('/models/refresh', { method: 'POST' }),
  // modelId 经 query 传递：模型 ID 常含 "/"（如 org/model），放进路径段会被
  // Gin 在路由前解码拆段导致 404。
  updateModel: (sourceId: string, modelId: string, body: Partial<Omit<Model, 'id' | 'sourceId'>>) =>
    request<{ updated: boolean }>(
      `/models/${encodeURIComponent(sourceId)}?modelId=${encodeURIComponent(modelId)}`,
      { method: 'PATCH', body },
    ),
  deleteModel: (sourceId: string, modelId: string) =>
    request<{ deleted: boolean }>(`/models/${encodeURIComponent(sourceId)}?modelId=${encodeURIComponent(modelId)}`, {
      method: 'DELETE',
    }),

  listGroups: () => request<ListEnvelope<ModelGroup>>('/model-groups').then((r) => r.items),
  createGroup: (body: ModelGroup) => request<ModelGroup>('/model-groups', { method: 'POST', body }),
  updateGroup: (id: string, body: ModelGroup) =>
    request<ModelGroup>(`/model-groups/${encodeURIComponent(id)}`, { method: 'PUT', body }),
  deleteGroup: (id: string) =>
    request<{ deleted: boolean }>(`/model-groups/${encodeURIComponent(id)}`, { method: 'DELETE' }),
  addGroupMembers: (id: string, models: string[]) =>
    request<{ added: number }>(`/model-groups/${encodeURIComponent(id)}/models`, {
      method: 'POST',
      body: { models },
    }),
  removeGroupMembers: (id: string, models: string[]) =>
    request<{ removed: number }>(`/model-groups/${encodeURIComponent(id)}/models`, {
      method: 'DELETE',
      body: { models },
    }),

  listTokens: () => request<ListEnvelope<ApiToken>>('/api-tokens').then((r) => r.items ?? []),
  revealToken: (name: string) =>
    request<{ name: string; token: string }>(`/api-tokens/${encodeURIComponent(name)}/reveal`),
  createToken: (body: ApiToken) => request<ApiToken>('/api-tokens', { method: 'POST', body }),
  updateToken: (name: string, body: ApiToken) =>
    request<ApiToken>(`/api-tokens/${encodeURIComponent(name)}`, { method: 'PUT', body }),
  deleteToken: (name: string) =>
    request<{ deleted: boolean }>(`/api-tokens/${encodeURIComponent(name)}`, { method: 'DELETE' }),

  usageStats: (params: UsageQueryParams) => request<UsageStats>('/usage/stats', { query: serializeUsage(params) }),
  usageTrend: (params: UsageQueryParams & { utcOffsetMinutes: number }) =>
    request<UsageTrendPoint[]>('/usage/trend', {
      query: { ...serializeUsage(params), utcOffsetMinutes: params.utcOffsetMinutes },
    }),
  usageByModel: (params: UsageQueryParams) =>
    request<UsageModelStat[]>('/usage/by-model', { query: serializeUsage(params) }),
  usagePulse: (params: UsageQueryParams & { utcOffsetMinutes: number; bucketMinutes: number }) =>
    request<UsagePulseResult>('/usage/pulse', {
      query: {
        ...serializeUsage(params),
        utcOffsetMinutes: params.utcOffsetMinutes,
        bucketMinutes: params.bucketMinutes,
      },
    }),
  usageByModelDaily: (params: UsageQueryParams & { utcOffsetMinutes: number; top?: number }) =>
    request<UsageModelDailyPoint[]>('/usage/by-model-daily', {
      query: { ...serializeUsage(params), utcOffsetMinutes: params.utcOffsetMinutes, top: params.top },
    }),
  usageLogs: (params: UsageQueryParams) => request<UsageLogsResult>('/usage/logs', { query: serializeUsage(params) }),
  usageLogDetail: (id: string) => request<UsageLogDetail>(`/usage/logs/${encodeURIComponent(id)}`),
  usageSeq: () => request<{ seq: number }>('/usage/seq'),
  usageReset: () => request<unknown>('/usage/reset', { method: 'POST' }),

  /** 日志占用状态：DB 体积/记录数/外置资产/最近一轮清理结果（设置页展示）。 */
  usageStorage: () => request<UsageStorageStatus>('/usage/storage'),
  /** 手动触发一轮日志清理巡检（异步执行；已在跑时后端返回 accepted=false）。 */
  usageCleanup: () => request<{ accepted: boolean }>('/usage/cleanup', { method: 'POST' }),
  /** 拉取某条记录的外置媒体文件（二进制）。<img> 无法附带 Bearer 头，
   * 前端经此函数取 blob 再 objectURL 渲染。 */
  usageAssetBlob: async (requestId: string, file: string): Promise<Blob> => {
    const token = getToken()
    const headers: Record<string, string> = {}
    if (token) headers.Authorization = `Bearer ${token}`
    const response = await fetch(
      buildUrl(`/usage/assets/${encodeURIComponent(requestId)}/${encodeURIComponent(file)}`),
      { headers },
    )
    // 与 request() 保持一致：会话失效时踢回登录页，而非仅报媒体获取失败。
    if (response.status === 401) {
      throwUnauthorized()
    }
    if (!response.ok) {
      throw new ApiError('asset_fetch_failed', `媒体文件获取失败（${response.status}）`, response.status)
    }
    return response.blob()
  },

  systemLogs: (params: { limit?: number; offset?: number; level?: string }) =>
    request<SystemLogsResult>('/logs', { query: params }),

  // ---- 协议设计器 ----
  listCustomProtocols: () =>
    request<ListEnvelope<CustomProtocolSummary>>('/custom-protocols').then((r) => r.items ?? []),
  /** 字段目录与约束（UI 下拉与校验共用）。 */
  customProtocolSchema: () => request<CustomProtocolSchema>('/custom-protocols/schema'),
  upsertCustomProtocol: (protocol: CustomProtocolConfig) =>
    request<{ saved: boolean; id: string; synced: boolean; warning?: string }>(
      `/custom-protocols/${encodeURIComponent(protocol.id)}`,
      { method: 'PUT', body: protocol },
    ),
  deleteCustomProtocol: (id: string) =>
    request<{ deleted: boolean; synced: boolean }>(`/custom-protocols/${encodeURIComponent(id)}`, {
      method: 'DELETE',
    }),
  /** 用样例 Maheshvara 请求渲染协议，预览真实发送形态（凭证打码）。 */
  previewCustomProtocol: (body: { protocol: CustomProtocolConfig; sampleRequest?: unknown }, options?: { signal?: AbortSignal }) =>
    request<CustomProtocolPreviewResult>('/custom-protocols/preview', { method: 'POST', body, signal: options?.signal }),
  /** 向所选模型源或临时凭据（baseUrl+apiKey 直连）真实发送渲染后的请求。 */
  testCustomProtocol: (body: {
    protocol: CustomProtocolConfig
    sourceId?: string
    model: string
    baseUrl?: string
    apiKey?: string
    stream?: boolean
    sampleRequest?: unknown
  }) => request<CustomProtocolTestResult>('/custom-protocols/test', { method: 'POST', body }),
  /** 按协议 models 发现配置试拉模型列表（临时凭据，不落库）。 */
  testCustomProtocolModels: (body: {
    protocol: CustomProtocolConfig
    baseUrl: string
    apiKey?: string
  }) => request<CustomProtocolModelsTestResult>('/custom-protocols/test-models', { method: 'POST', body }),
  /** AI harness：读取文档/图片/文本生成草稿，服务端校验+自动修复+离线验证。 */
  assistCustomProtocol: (body: {
    sourceId: string
    model: string
    protocolType?: string
    message?: string
    documents?: CustomProtocolAssistDocument[]
    currentConfig?: unknown
    exampleResponse?: unknown
    maxRepairRounds?: number
  }) => request<CustomProtocolAssistResult>('/custom-protocols/assist', { method: 'POST', body }),
}

function serializeUsage(params: UsageQueryParams): Record<string, QueryValue> {
  return {
    from: params.from,
    to: params.to,
    limit: params.limit,
    offset: params.offset,
    keyName: params.keyName,
    keyHash: params.keyHash,
    groupName: params.groupName,
    modelName: params.modelName,
    status: params.status,
    statusCode: params.statusCode !== undefined ? params.statusCode : undefined,
    // 多选数组按重复参数发送（keyName/groupName/modelName），后端用 QueryArray 读取。
    ...(params.keyNames?.length ? { keyName: params.keyNames } : {}),
    ...(params.groupNames?.length ? { groupName: params.groupNames } : {}),
    ...(params.modelNames?.length ? { modelName: params.modelNames } : {}),
    ...(params.sourceIds?.length ? { sourceId: params.sourceIds } : {}),
  }
}

