import { clearToken, getToken } from './auth'
import type {
  ApiResult,
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
  deleteSource: (id: string) =>
    request<{ deleted: boolean }>(`/model-sources/${encodeURIComponent(id)}`, { method: 'DELETE' }),
  fetchSource: (id: string) =>
    request<{ refreshed: boolean; count: number }>(`/model-sources/${encodeURIComponent(id)}/fetch`, {
      method: 'POST',
    }),

  listModels: () => request<ListEnvelope<Model>>('/models').then((r) => r.items ?? []),
  refreshModels: () => request<{ refreshed: boolean; count: number }>('/models/refresh', { method: 'POST' }),

  listGroups: () =>
    request<ListEnvelope<ModelGroup>>('/model-groups').then((r) =>
      (r.items ?? []).map((group) => ({ ...group, models: group.models ?? [] })),
    ),
  createGroup: (body: ModelGroup) => request<ModelGroup>('/model-groups', { method: 'POST', body }),
  updateGroup: (id: string, body: ModelGroup) =>
    request<ModelGroup>(`/model-groups/${encodeURIComponent(id)}`, { method: 'PUT', body }),
  deleteGroup: (id: string) =>
    request<{ deleted: boolean }>(`/model-groups/${encodeURIComponent(id)}`, { method: 'DELETE' }),

  listTokens: () => request<ListEnvelope<ApiToken>>('/api-tokens').then((r) => r.items ?? []),
  revealToken: (name: string) =>
    request<{ name: string; token: string }>(`/api-tokens/${encodeURIComponent(name)}/reveal`),
  createToken: (body: ApiToken) => request<ApiToken>('/api-tokens', { method: 'POST', body }),
  updateToken: (name: string, body: ApiToken) =>
    request<ApiToken>(`/api-tokens/${encodeURIComponent(name)}`, { method: 'PUT', body }),
  deleteToken: (name: string) =>
    request<{ deleted: boolean }>(`/api-tokens/${encodeURIComponent(name)}`, { method: 'DELETE' }),

  usageStats: (params: UsageQueryParams) => request<UsageStats>('/usage/stats', { query: serializeUsage(params) }),
  usageLogs: (params: UsageQueryParams) => request<UsageLogsResult>('/usage/logs', { query: serializeUsage(params) }),
  usageLogDetail: (id: string) => request<UsageLogDetail>(`/usage/logs/${encodeURIComponent(id)}`),
  usageReset: () => request<unknown>('/usage/reset', { method: 'POST' }),

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
    statusCode: params.statusCode || undefined,
    // 多选数组按重复参数发送（keyName/groupName/modelName），后端用 QueryArray 读取。
    ...(params.keyNames?.length ? { keyName: params.keyNames } : {}),
    ...(params.groupNames?.length ? { groupName: params.groupNames } : {}),
    ...(params.modelNames?.length ? { modelName: params.modelNames } : {}),
  }
}

export type { UsageLogItem }
