import type { ReactElement, ReactNode } from 'react'
import { ResponsiveContainer } from 'recharts'
import { formatNumber } from '@/lib/utils'

export function ChartFrame({ height, children }: { height: number; children: ReactElement }) {
  return (
    <div className="w-full overflow-hidden" style={{ height }}>
      <ResponsiveContainer width="100%" height={height}>
        {children}
      </ResponsiveContainer>
    </div>
  )
}

export function ChartTooltip({
  active,
  payload,
  label,
  formatValue,
}: {
  active?: boolean
  payload?: { name?: string; value?: number | string; color?: string }[]
  label?: ReactNode
  formatValue?: (value: number) => string
}) {
  if (!active || !payload?.length) return null
  const fmt = formatValue ?? ((v: number) => formatNumber(v))
  return (
    <div className="min-w-[180px] rounded-xl border border-border/80 bg-card/95 px-3 py-2 text-xs shadow-xl backdrop-blur-md tnum">
      <p className="font-mono text-2xs font-medium text-muted-foreground">{label}</p>
      {payload.map((entry, i) => (
        <p key={`${entry.name ?? 's'}-${i}`} className="flex items-center gap-2 leading-relaxed">
          <i className="h-2 w-2 rounded-full" style={{ background: entry.color }} aria-hidden />
          <span className="max-w-[160px] truncate font-mono text-muted-foreground">{entry.name}</span>
          <b className="ml-auto pl-3 font-mono font-semibold text-foreground">{fmt(Number(entry.value))}</b>
        </p>
      ))}
    </div>
  )
}
