import useSWR, { mutate as globalMutate, type SWRConfiguration } from 'swr'
import { api } from './api'
import type { UsageQueryParams } from './types'

const defaultConfig: SWRConfiguration = {
  revalidateOnFocus: false,
  shouldRetryOnError: false,
  dedupingInterval: 2000,
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
