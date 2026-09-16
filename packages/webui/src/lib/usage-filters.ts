import { useMemo, useState } from 'react'
import type { RangeKey } from '@/components/usage-filter-bar'
import { useMinuteTick, useSources, useUsageFilterOptions } from './hooks'
import { bucketedTimeISO, startOfRange, USAGE_BUCKET_MS } from './utils'

/**
 * 用量页（调用日志 + Usage 统计）共用的筛选状态、下拉选项与查询参数构造。
 * 任一筛选维度变化时触发 onChange（调用日志页用于重置分页）。
 *
 * params 的 to 取下一 5 分钟边界：缓存键稳定，且当前桶内新记录能进半开
 * 区间；相对窗起点用 now，避免临近午夜把 from 推到次日。页面专属参数
 * （状态/状态码/分页）由调用方在 spread params 后追加。
 */
export function useUsageFilters(onChange?: () => void) {
  const [range, setRangeState] = useState<RangeKey>('7d')
  const [groupNames, setGroupNamesState] = useState<string[]>([])
  const [modelNames, setModelNamesState] = useState<string[]>([])
  const [sourceIds, setSourceIdsState] = useState<string[]>([])
  const [keyNames, setKeyNamesState] = useState<string[]>([])
  const minuteTick = useMinuteTick()

  const setRange = (value: RangeKey) => {
    setRangeState(value)
    onChange?.()
  }
  const setGroupNames = (value: string[]) => {
    setGroupNamesState(value)
    onChange?.()
  }
  const setModelNames = (value: string[]) => {
    setModelNamesState(value)
    onChange?.()
  }
  const setSourceIds = (value: string[]) => {
    setSourceIdsState(value)
    onChange?.()
  }
  const setKeyNames = (value: string[]) => {
    setKeyNamesState(value)
    onChange?.()
  }

  const { groupOptions, modelOptions, keyOptions } = useUsageFilterOptions()
  const { data: sources } = useSources()
  const sourceOptions = useMemo(
    () => (sources ?? []).filter((s) => s.enabled).map((s) => ({ value: s.id, label: s.name || s.id })),
    [sources],
  )

  const params = useMemo(() => {
    const nowMs = minuteTick * 60_000
    return {
      from: startOfRange(range, new Date(nowMs).toISOString()),
      to: bucketedTimeISO(nowMs, USAGE_BUCKET_MS),
      groupNames: groupNames.length ? groupNames : undefined,
      modelNames: modelNames.length ? modelNames : undefined,
      sourceIds: sourceIds.length ? sourceIds : undefined,
      keyNames: keyNames.length ? keyNames : undefined,
    }
  }, [range, groupNames, modelNames, sourceIds, keyNames, minuteTick])

  /** UsageFilterBar 的标准接线，页面专属控件经 children 追加。 */
  const barProps = {
    range,
    onRangeChange: setRange,
    groupOptions,
    modelOptions,
    keyOptions,
    sourceOptions,
    groupNames,
    onGroupNamesChange: setGroupNames,
    modelNames,
    onModelNamesChange: setModelNames,
    sourceIds,
    onSourceIdsChange: setSourceIds,
    keyNames,
    onKeyNamesChange: setKeyNames,
  }

  return {
    minuteTick,
    range,
    groupNames,
    modelNames,
    sourceIds,
    keyNames,
    setRange,
    setGroupNames,
    setModelNames,
    setSourceIds,
    setKeyNames,
    sources: sources ?? [],
    groupOptions,
    modelOptions,
    keyOptions,
    sourceOptions,
    params,
    barProps,
  }
}
