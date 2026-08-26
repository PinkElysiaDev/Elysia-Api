import type { ReactNode } from 'react'
import { cn } from '@/lib/utils'

export function KpiGrid({
  children,
  cols,
  className,
}: {
  /** lg+ 档的目标列数：4（诊断）/ 5（首页，缺省）/ 6（用量统计）。 */
  cols?: number
  children: ReactNode
  className?: string
}) {
  const lgCols =
    cols === 4 ? 'lg:grid-cols-4' : cols === 6 ? 'lg:grid-cols-3 xl:grid-cols-6' : 'lg:grid-cols-5'
  return (
    <div
      className={cn(
        'relative grid grid-cols-2 gap-y-6 sm:gap-y-8 md:grid-cols-3',
        'md:divide-x md:divide-border/30',
        lgCols,
        className,
      )}
    >
      {children}
    </div>
  )
}

/**
 * 无界灵动 KPI 计量锚点：数据直接生长在开放画布上，超大 Fraunces 衬线数字，彻底摒弃封闭四周边框与卡片盒子。
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
        'group relative flex flex-col justify-between py-1 px-3 sm:px-5 transition-colors duration-200',
        className,
      )}
    >
      <div>
        <div className="flex items-center justify-between gap-2">
          <span className="text-2xs font-semibold uppercase tracking-widest text-muted-foreground">
            {label}
          </span>
          {icon && <span className="opacity-75 transition-transform duration-200 group-hover:scale-110 group-hover:opacity-100">{icon}</span>}
        </div>

        <div className="mt-3 flex items-baseline gap-1.5">
          <span
            className={cn(
              'tnum font-display font-medium tracking-tight',
              isHero
                ? 'text-3xl sm:text-4xl lg:text-[40px] bg-brand-grad bg-clip-text text-transparent leading-none'
                : 'text-2xl sm:text-3xl lg:text-[34px] text-foreground leading-none',
            )}
          >
            {value}
          </span>
          {unit && (
            <span className="font-mono text-xs font-semibold text-muted-foreground/90">
              {unit}
            </span>
          )}
        </div>
      </div>

      {delta && (
        <div
          className={cn(
            'tnum mt-3 inline-flex items-center gap-1.5 text-xs font-normal',
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
