import { clearToken, getToken } from './auth'
import type {
  ApiResult,
  UsageTrendPoint,
  UsageModelStat,
  UsagePulseResult,
  UsageModelDailyPoint,
  ApiToken,
  Health,
  Model,
  ModelGroup,
  ModelSource,
  RuntimeConfig,
  RuntimeConfigUpdate,
  RuntimeConfigUpdateResult,
  SystemLogsResult,
  UsageLogDetail,
  UsageLogItem,
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
    throw new ApiError('network_error', (err as Error).message || '网络请求失败', 0)
  }

  if (response.status === 401) {
    clearToken()
    throw new ApiError('unauthorized', '认证已失效，请重新登录', 401)
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

/** 用 panel token 校验登录：命中受保护端点即视为有效。 */
export async function verifyToken(token: string): Promise<boolean> {
  const response = await fetch(`${ADMIN_BASE}/health`, {
    headers: { Authorization: `Bearer ${token}` },
  })
  return response.ok
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

  listSources: () => request<ListEnvelope<ModelSource>>('/model-sources').then((r) => r.items ?? []),
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
    }).then((r) => r.items ?? []),
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

  listGroups: () =>
    request<ListEnvelope<ModelGroup>>('/model-groups').then((r) =>
      (r.items ?? []).map((group) => ({ ...group, models: group.models ?? [] })),
    ),
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
    if (!response.ok) {
      throw new ApiError('asset_fetch_failed', `媒体文件获取失败（${response.status}）`, response.status)
    }
    return response.blob()
  },

  systemLogs: (params: { limit?: number; offset?: number; level?: string }) =>
    request<SystemLogsResult>('/logs', { query: params }),
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
    statusCode: params.statusCode || undefined,
    // 多选数组按重复参数发送（keyName/groupName/modelName），后端用 QueryArray 读取。
    ...(params.keyNames?.length ? { keyName: params.keyNames } : {}),
    ...(params.groupNames?.length ? { groupName: params.groupNames } : {}),
    ...(params.modelNames?.length ? { modelName: params.modelNames } : {}),
    ...(params.sourceIds?.length ? { sourceId: params.sourceIds } : {}),
  }
}

export type { UsageLogItem }
