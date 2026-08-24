import type { ReactNode } from 'react'
import { cn } from '@/lib/utils'

const KPI_GRID_COLS = {
  4: 'grid-cols-2 rail:grid-cols-4 max-rail:[&>*:nth-child(odd)]:border-l-0 max-rail:[&>*:nth-child(odd)]:pl-0',
  5: 'grid-cols-2 gap-y-4 rail:grid-cols-5 rail:gap-y-0 max-rail:[&>*:nth-child(odd)]:border-l-0 max-rail:[&>*:nth-child(odd)]:pl-0',
  6: 'grid-cols-2 rail:grid-cols-3 xl:grid-cols-6 max-rail:[&>*:nth-child(odd)]:border-l-0 max-rail:[&>*:nth-child(odd)]:pl-0 rail:max-xl:[&>*:nth-child(3n+1)]:border-l-0 rail:max-xl:[&>*:nth-child(3n+1)]:pl-0',
} as const

export function KpiGrid({
  cols,
  children,
  className,
}: {
  cols: keyof typeof KPI_GRID_COLS
  children: ReactNode
  className?: string
}) {
  return (
    <div
      className={cn(
        'grid [&>*:first-child]:border-l-0 [&>*:first-child]:pl-0',
        KPI_GRID_COLS[cols],
        className,
      )}
    >
      {children}
    </div>
  )
}

/** KPI 卡：label / 渐变数字 / delta。 */
export function KpiCard({
  label,
  value,
  unit,
  delta,
  deltaTone = 'neutral',
  className,
}: {
  label: ReactNode
  value: ReactNode
  /** 数字内的小单位，如 % / ms / rpm。 */
  unit?: string
  /** 下方说明行；up jade / down ember / neutral muted 由调用方拼内容 */
  delta?: ReactNode
  deltaTone?: 'up' | 'down' | 'neutral'
  className?: string
}) {
  return (
    <div
      className={cn(
        'min-w-0 border-l border-border px-6 py-1',
        className,
      )}
    >
      <div className="flex items-center gap-[7px] text-sm font-semibold tracking-[0.01em] text-foreground">{label}</div>
      <span className="tnum mt-[3px] block bg-brand-grad bg-clip-text font-display text-display font-medium leading-[1.25] tracking-[0.01em] text-transparent">
        {value}
        {unit && (
          <small className="ml-[2px] text-[0.52em] font-medium text-muted-foreground [-webkit-text-fill-color:hsl(var(--muted-foreground))]">
            {unit}
          </small>
        )}
      </span>
      {delta && (
        <span
          className={cn(
            'tnum mt-[3px] block text-xs',
            deltaTone === 'up' && 'text-jade',
            deltaTone === 'down' && 'text-ember',
            deltaTone === 'neutral' && 'text-muted-foreground',
          )}
        >
          {delta}
        </span>
      )}
    </div>
  )
}
