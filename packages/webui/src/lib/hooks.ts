import { useEffect, useState } from 'react'
import useSWR, { mutate as globalMutate, type SWRConfiguration } from 'swr'
import { api } from './api'
import type { UsageQueryParams } from './types'

const defaultConfig: SWRConfiguration = {
  revalidateOnFocus: false,
  shouldRetryOnError: false,
  dedupingInterval: 2000,
}

/**
 * 每分钟推进一次的计数器。usage 查询窗口的 `to = normalizedNow()` 若只放在
 * useMemo 里，SWR 刷新时会重放同一个闭包，窗口永远冻结在挂载（或上次改
 * 过滤器）那一刻——开着"全部时间"也看不到新记录。把该计数器加进 useMemo
 * 依赖，查询 key 每分钟更新一次，窗口随时间前进并自动重新请求。
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

export function useHealth(refreshInterval = 0) {
  return useSWR('health', () => api.health(), { ...defaultConfig, refreshInterval })
}

export function useRuntimeConfig() {
  return useSWR('runtime-config', () => api.runtimeConfig(), defaultConfig)
}

export function useSources() {
  return useSWR('model-sources', () => api.listSources(), defaultConfig)
}

export function useModels() {
  return useSWR('models', () => api.listModels(), defaultConfig)
}

export function useGroups() {
  return useSWR('model-groups', () => api.listGroups(), defaultConfig)
}

export function useTokens() {
  return useSWR('api-tokens', () => api.listTokens(), defaultConfig)
}

const usageConfig: SWRConfiguration = {
  ...defaultConfig,
  keepPreviousData: true,
  dedupingInterval: 5000,
}

export function useUsageStats(params: UsageQueryParams) {
  return useSWR(['usage-stats', params], () => api.usageStats(params), usageConfig)
}

export function useUsageLogs(params: UsageQueryParams) {
  return useSWR(['usage-logs', params], () => api.usageLogs(params), usageConfig)
}

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
  health: () => globalMutate('health'),
  usage: () =>
    globalMutate((key) => Array.isArray(key) && (key[0] === 'usage-stats' || key[0] === 'usage-logs'), undefined, {
      revalidate: true,
    }),
}
