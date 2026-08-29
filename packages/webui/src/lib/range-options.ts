import type { RangeKey } from '@/components/usage-filter-bar'
import type { SegOption } from '@/components/ui/seg'

/** 用量页共用的时间窗选项（24 小时 / 7 天 / 30 天 / 全部）。 */
export const RANGE_OPTIONS: SegOption<RangeKey>[] = [
  { value: '24h', label: '24小时' },
  { value: '7d', label: '7天' },
  { value: '30d', label: '30天' },
  { value: 'all', label: '全部' },
]
