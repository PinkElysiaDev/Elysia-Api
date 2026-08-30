import { useEffect, useMemo, useRef, useState } from 'react'
import useSWR, { mutate as globalMutate, type SWRConfiguration } from 'swr'
import { api } from './api'
import type { UsageQueryParams } from './types'
import { uniqueSorted } from './utils'

const defaultConfig: SWRConfiguration = {
  revalidateOnFocus: false,
  shouldRetryOnError: false,
  dedupingInterval: 2000,
}

/**
 * 每分钟推进一次的计数器。usage 查询窗口的 `to` 若只放在 useMemo 里，
 * SWR 刷新时会重放同一个闭包，窗口永远冻结在挂载（或上次改过滤器）那一刻。
 * 把该计数器加进 useMemo 依赖，查询 key 每分钟更新一次，窗口随时间前进。
 */
export function useMinuteTick(): number {
  const [tick, setTick] = useState(() => Math.floor(Date.now() / 60_000))
  useEffect(() => {
    const id = setInterval(() => {
      const next = Math.floor(Date.now() / 60_000)
      setTick((prev) => (prev === next ? prev : next))
    }, 15_000)
    return () => clearInterval(id)
  }, [])
  return tick
}

/**
 * 防抖值：输入框即时回显原始值，重活（过滤、请求）读防抖后的值，
 * 连续击键只在停顿 debounceMs 后触发一次下游计算。
 */
export function useDebouncedValue<T>(value: T, debounceMs = 160): T {
  const [debounced, setDebounced] = useState(value)
  useEffect(() => {
    const id = setTimeout(() => setDebounced(value), debounceMs)
    return () => clearTimeout(id)
  }, [value, debounceMs])
  return debounced
}

/** 网关健康状态（内存/DB），refreshInterval 毫秒轮询。 */
export function useHealth(refreshInterval = 0) {
  return useSWR('health', () => api.health(), { ...defaultConfig, refreshInterval })
}

export function useRuntimeConfig() {
  return useSWR('runtime-config', () => api.runtimeConfig(), defaultConfig)
}

/** 模型源列表（可选轮询间隔毫秒）。 */
export function useSources(refreshInterval = 0) {
  return useSWR('model-sources', () => api.listSources(), { ...defaultConfig, refreshInterval })
}

/** 能力目录（models.dev）状态：未加载或加载失败时模型能力不会被自动回填，
 * 供 sources 页提示用户自诊断（目录不可达 → 配置 modelCatalog.url/proxy）。 */
export function useModelCatalogStatus() {
  return useSWR(
    'model-catalog-status',
    () => api.modelCatalogStatus(),
    { ...defaultConfig, refreshInterval: 60_000 },
  )
}

/** 全量模型列表（可选轮询间隔毫秒）。 */
export function useModels(refreshInterval = 0) {
  return useSWR('models', () => api.listModels(), { ...defaultConfig, refreshInterval })
}

/** 模型组列表。 */
export function useGroups() {
  return useSWR('model-groups', () => api.listGroups(), defaultConfig)
}

/** API token 列表（列表值已脱敏）。 */
export function useTokens() {
  return useSWR('api-tokens', () => api.listTokens(), defaultConfig)
}

/** Usage 统计 / 调用日志共用的筛选下拉选项。 */
export function useUsageFilterOptions() {
  const { data: groups } = useGroups()
  const { data: models } = useModels()
  const { data: tokens } = useTokens()
  const groupOptions = useMemo(() => uniqueSorted((groups ?? []).map((g) => g.name)), [groups])
  const modelOptions = useMemo(() => uniqueSorted((models ?? []).map((m) => m.name)), [models])
  const keyOptions = useMemo(() => uniqueSorted((tokens ?? []).map((t) => t.name)), [tokens])
  return { groupOptions, modelOptions, keyOptions }
}

const usageConfig: SWRConfiguration = {
  ...defaultConfig,
  keepPreviousData: true,
  // 有新调用时由 useUsageLive 按 seq 立刻重拉。这里只做慢兜底（脉搏窗口随时间滑动）。
  dedupingInterval: 2_000,
  refreshInterval: 30_000,
}

/** 登录后探测 usage 序号：原子计数，无 SQL。序号变化才重拉 KPI/日志/图表。 */
export function useUsageLive() {
  const { data } = useSWR('usage-seq', () => api.usageSeq(), {
    ...defaultConfig,
    refreshInterval: 2_000,
    dedupingInterval: 1_000,
  })
  const seq = data?.seq
  const prev = useRef<number | undefined>()
  useEffect(() => {
    if (seq == null) return
    if (prev.current != null && prev.current !== seq) {
      void revalidate.usage()
    }
    prev.current = seq
  }, [seq])
}

/** Usage KPI 汇总（params 见 serializeUsage）。 */
export function useUsageStats(params: UsageQueryParams) {
  return useSWR(['usage-stats', params], () => api.usageStats(params), usageConfig)
}

/**
 * 按日趋势聚合（后端按固定 UTC offset 换算为本地日，不受明细 limit 钳制影响）。
 * 查询参数只有 from（日内内容稳定，SWR 键全天不变），由 usageConfig 轮询
 * 刷新当日桶；如需不同节奏可显式传 refreshInterval 覆盖。
 */
export function useUsageTrend(params: UsageQueryParams & { utcOffsetMinutes: number }, refreshInterval?: number) {
  return useSWR(['usage-trend', params], () => api.usageTrend(params), {
    ...usageConfig,
    ...(refreshInterval ? { refreshInterval } : {}),
  })
}

/** 按模型聚合（热门模型 / 明细表）。 */
export function useUsageByModel(params: UsageQueryParams) {
  return useSWR(['usage-by-model', params], () => api.usageByModel(params), usageConfig)
}

export function useUsagePulse(params: UsageQueryParams & { utcOffsetMinutes: number; bucketMinutes: number }) {
  return useSWR(['usage-pulse', params], () => api.usagePulse(params), usageConfig)
}

export function useUsageByModelDaily(params: UsageQueryParams & { utcOffsetMinutes: number; top?: number }) {
  return useSWR(['usage-by-model-daily', params], () => api.usageByModelDaily(params), usageConfig)
}

/** Usage 调用日志分页（params 含 limit/offset）。 */
export function useUsageLogs(params: UsageQueryParams) {
  return useSWR(['usage-logs', params], () => api.usageLogs(params), usageConfig)
}

/** 系统日志分页。 */
export function useSystemLogs(params: { limit?: number; offset?: number; level?: string }) {
  return useSWR(['system-logs', params], () => api.systemLogs(params), defaultConfig)
}

/** 数据变更后批量刷新缓存。 */
export const revalidate = {
  sources: () => globalMutate('model-sources'),
  models: () => globalMutate('models'),
  groups: () => globalMutate('model-groups'),
  tokens: () => globalMutate('api-tokens'),
  runtimeConfig: () => globalMutate('runtime-config'),
  modelCatalogStatus: () => globalMutate('model-catalog-status'),
  health: () => globalMutate('health'),
  usage: () =>
    globalMutate(
      (key) =>
        Array.isArray(key) &&
        (key[0] === 'usage-stats' ||
          key[0] === 'usage-logs' ||
          key[0] === 'usage-trend' ||
          key[0] === 'usage-by-model' ||
          key[0] === 'usage-pulse' ||
          key[0] === 'usage-by-model-daily'),
      undefined,
      { revalidate: true },
    ),
}
