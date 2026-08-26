import { MultiSelect, type MultiSelectOption } from '@/components/ui/multi-select'
import { RangeSelect, type RangeKey } from '@/components/range-select'
import type { Model } from '@/lib/types'

/**
 * 模型源筛选是纯前端实现：选中源 → 该源下全部模型名，与「模型」筛选取交集后
 * 走后端已有的 modelName 多值 IN 过滤（零后端改动）。两侧都未选时返回空数组
 * （= 不过滤）。
 */
export function effectiveModelFilter(
  modelNames: string[],
  sourceNames: string[],
  models: Model[],
): string[] {
  if (sourceNames.length === 0) return modelNames
  const set = new Set(sourceNames)
  const fromSources = models.filter((m) => m.sourceName && set.has(m.sourceName)).map((m) => m.name)
  if (modelNames.length === 0) return fromSources
  const sourceSet = new Set(fromSources)
  return modelNames.filter((name) => sourceSet.has(name))
}

/**
 * 用量页共用筛选条（调用日志 + Usage 统计）：时间窗 + 模型组 / 模型 / 模型源 /
 * 调用方，风格与全站 h-[34px] 控件对齐。页面专属筛选控件经 children 追加，
 * 右侧汇总/指示区经 right 提供。
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
  children?: React.ReactNode
  /** 右侧区域（汇总图例 / 更新指示）。 */
  right?: React.ReactNode
}) {
  return (
    <div className="flex flex-wrap items-center justify-between gap-x-6 gap-y-3 rounded-xl border border-border/70 bg-card p-3.5 shadow-soft">
      <div className="flex flex-wrap items-center gap-2.5">
        <div className="w-[130px]">
          <RangeSelect value={range} onChange={onRangeChange} />
        </div>
        <div className="min-w-[120px] flex-1 sm:flex-none">
          <MultiSelect
            options={groupOptions}
            value={groupNames}
            onChange={onGroupNamesChange}
            placeholder="全部模型组"
            searchPlaceholder="搜索模型组"
          />
        </div>
        <div className="min-w-[120px] flex-1 sm:flex-none">
          <MultiSelect
            options={modelOptions}
            value={modelNames}
            onChange={onModelNamesChange}
            placeholder="全部模型"
            searchPlaceholder="搜索模型"
          />
        </div>
        {sourceOptions.length > 0 && (
          <div className="min-w-[110px] flex-1 sm:flex-none">
            <MultiSelect
              options={sourceOptions}
              value={sourceNames}
              onChange={onSourceNamesChange}
              placeholder="全部模型源"
              searchPlaceholder="搜索模型源"
            />
          </div>
        )}
        <div className="min-w-[110px] flex-1 sm:flex-none">
          <MultiSelect
            options={keyOptions}
            value={keyNames}
            onChange={onKeyNamesChange}
            placeholder="全部调用方"
            searchPlaceholder="搜索调用方"
          />
        </div>
        {children}
      </div>
      {right}
    </div>
  )
}
