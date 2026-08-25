import type { ReactNode } from 'react'
import { cn } from '@/lib/utils'

export function KpiGrid({
  children,
  cols,
  className,
}: {
  /** lg+ 档的目标列数：4（诊断）/ 5（首页，缺省）/ 6（用量统计）。
   * 基础档统一 grid-cols-2 → md:grid-cols-3。 */
  cols?: number
  children: ReactNode
  className?: string
}) {
  const lgCols =
    cols === 4 ? 'lg:grid-cols-4' : cols === 6 ? 'lg:grid-cols-3 xl:grid-cols-6' : 'lg:grid-cols-5'
  return (
    <div className={cn('grid grid-cols-2 gap-3.5 sm:gap-4 md:grid-cols-3', lgCols, className)}>
      {children}
    </div>
  )
}

/**
 * 现代高质感 KPI 卡片：支持 Hero 主卡片与标准数据卡片两档视觉权重。
 */
export function KpiCard({
  label,
  value,
  unit,
  delta,
  deltaTone = 'neutral',
  variant = 'standard',
  icon,
  className,
}: {
  label: ReactNode
  value: ReactNode
  /** 数字内的小单位，如 % / ms / rpm。 */
  unit?: string
  /** 下方说明行；up jade / down ember / neutral muted 由调用方拼内容 */
  delta?: ReactNode
  deltaTone?: 'up' | 'down' | 'neutral'
  /** hero 卡片用于首页第一视觉重心，突出总量与健康度 */
  variant?: 'hero' | 'standard'
  icon?: ReactNode
  className?: string
}) {
  const isHero = variant === 'hero'

  return (
    <div
      className={cn(
        'relative flex flex-col justify-between overflow-hidden rounded-xl border p-4 sm:p-5 transition-all duration-200',
        isHero
          ? 'border-primary/30 bg-gradient-to-b from-card via-card to-primary/[0.03] shadow-soft hover:shadow-md hover:border-primary/50'
          : 'border-border/80 bg-card shadow-soft hover:shadow-md hover:border-border',
        className,
      )}
    >
      {/* 顶部微光装饰条（Hero 卡片拥有品牌渐变边线） */}
      {isHero && (
        <div className="absolute inset-x-0 top-0 h-[2px] bg-brand-grad" aria-hidden />
      )}

      <div>
        <div className="flex items-center justify-between gap-2">
          <span className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">
            {label}
          </span>
          {icon && <span className="text-muted-foreground/70">{icon}</span>}
        </div>

        <div className="mt-2 flex items-baseline gap-1">
          <span
            className={cn(
              'tnum font-display font-semibold tracking-tight text-foreground',
              isHero
                ? 'text-2xl sm:text-3xl bg-brand-grad bg-clip-text text-transparent'
                : 'text-xl sm:text-2xl text-foreground',
            )}
          >
            {value}
          </span>
          {unit && (
            <span className="font-mono text-xs font-medium text-muted-foreground">
              {unit}
            </span>
          )}
        </div>
      </div>

      {delta && (
        <div
          className={cn(
            'tnum mt-3 inline-flex items-center gap-1.5 text-xs font-medium',
            deltaTone === 'up' && 'text-jade',
            deltaTone === 'down' && 'text-ember',
            deltaTone === 'neutral' && 'text-muted-foreground',
          )}
        >
          {delta}
        </div>
      )}
    </div>
  )
}
