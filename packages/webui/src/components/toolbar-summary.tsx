import { cn, formatNumber } from '@/lib/utils'

export interface ToolbarStatItem {
  label: string
  value: number
  /** 有色调时显示色点：jade = 启用/正常，ember = 停用/异常 */
  tone?: 'jade' | 'ember'
}

/** 列表页工具栏右侧统计簇：色点/标签计数 + 发丝分隔线 + 「共 N 个单位」。 */
export function ToolbarSummary({
  items,
  total,
  unit,
  className,
}: {
  items: ToolbarStatItem[]
  total: number
  unit: string
  className?: string
}) {
  return (
    <div className={cn('flex items-center gap-4 text-xs', className)}>
      <span className="tnum flex items-center gap-3 font-mono text-muted-foreground">
        {items.map((item) => (
          <span key={item.label} className="flex items-center gap-1.5">
            {item.tone && (
              <span
                aria-hidden
                className={cn('h-2 w-2 rounded-full', item.tone === 'jade' ? 'bg-jade' : 'bg-ember')}
              />
            )}
            {item.label} <b className="font-semibold text-foreground">{formatNumber(item.value)}</b>
          </span>
        ))}
      </span>
      <span className="h-3 w-px bg-border/70" aria-hidden />
      <span className="font-mono text-muted-foreground">
        共 <b className="tnum font-semibold text-foreground">{formatNumber(total)}</b> {unit}
      </span>
    </div>
  )
}
