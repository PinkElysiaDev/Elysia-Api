import type { ReactNode } from 'react'
import { X } from 'lucide-react'
import { MultiSelect, type MultiSelectOption } from '@/components/ui/multi-select'
import { Seg, type SegOption } from '@/components/ui/seg'

export type RangeKey = '24h' | '7d' | '30d' | 'all'

const RANGE_OPTIONS: SegOption<RangeKey>[] = [
  { value: '24h', label: '24小时' },
  { value: '7d', label: '7天' },
  { value: '30d', label: '30天' },
  { value: 'all', label: '全部' },
]

/**
 * 用量页共用筛选工具栏（调用日志 + Usage 统计）：时间窗 Seg + 模型组 / 模型 /
 * 模型源 / 调用方筛选胶囊。单行紧凑布局，窄屏 flex-wrap 换行为小胶囊而非
 * 堆叠的大配置框；任一维度激活时提供一键「清除筛选」。页面专属筛选控件经
 * children 追加，右侧汇总/指示区经 right 提供。
 */
export function UsageFilterBar({
  range,
  onRangeChange,
  groupOptions,
  modelOptions,
  keyOptions,
  sourceOptions,
  groupNames,
  onGroupNamesChange,
  modelNames,
  onModelNamesChange,
  sourceNames,
  onSourceNamesChange,
  keyNames,
  onKeyNamesChange,
  children,
  right,
}: {
  range: RangeKey
  onRangeChange: (value: RangeKey) => void
  groupOptions: (string | MultiSelectOption)[]
  modelOptions: (string | MultiSelectOption)[]
  keyOptions: (string | MultiSelectOption)[]
  /** 启用源名称列表（与 Model.sourceName 对应）；为空则不显示源筛选。 */
  sourceOptions: (string | MultiSelectOption)[]
  groupNames: string[]
  onGroupNamesChange: (value: string[]) => void
  modelNames: string[]
  onModelNamesChange: (value: string[]) => void
  sourceNames: string[]
  onSourceNamesChange: (value: string[]) => void
  keyNames: string[]
  onKeyNamesChange: (value: string[]) => void
  /** 页面专属筛选控件（追加在调用方之后）。 */
  children?: ReactNode
  /** 右侧区域（汇总图例 / 更新指示）。 */
  right?: ReactNode
}) {
  const hasFilters =
    groupNames.length > 0 || modelNames.length > 0 || sourceNames.length > 0 || keyNames.length > 0

  return (
    <div className="flex flex-wrap items-center gap-x-2.5 gap-y-2">
      <Seg
        aria-label="时间窗"
        className="h-[34px]"
        options={RANGE_OPTIONS}
        value={range}
        onChange={onRangeChange}
      />
      <span className="h-5 w-px bg-border/70" aria-hidden />
      <MultiSelect
        label="模型组"
        options={groupOptions}
        value={groupNames}
        onChange={onGroupNamesChange}
        searchPlaceholder="搜索模型组"
      />
      <MultiSelect
        label="模型"
        options={modelOptions}
        value={modelNames}
        onChange={onModelNamesChange}
        searchPlaceholder="搜索模型"
      />
      {sourceOptions.length > 0 && (
        <MultiSelect
          label="模型源"
          options={sourceOptions}
          value={sourceNames}
          onChange={onSourceNamesChange}
          searchPlaceholder="搜索模型源"
        />
      )}
      <MultiSelect
        label="调用方"
        options={keyOptions}
        value={keyNames}
        onChange={onKeyNamesChange}
        searchPlaceholder="搜索调用方"
      />
      {children}
      {hasFilters && (
        <button
          type="button"
          onClick={() => {
            onGroupNamesChange([])
            onModelNamesChange([])
            onSourceNamesChange([])
            onKeyNamesChange([])
          }}
          className="flex h-[34px] items-center gap-1 rounded-md px-2 text-xs text-muted-foreground transition-colors duration-150 hover:bg-wash hover:text-rose focus:outline-none focus-visible:ring-2 focus-visible:ring-ring"
        >
          <X className="h-3.5 w-3.5" />
          清除筛选
        </button>
      )}
      {right ? <div className="ml-auto pl-1">{right}</div> : null}
    </div>
  )
}
