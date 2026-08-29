import type { ReactNode } from 'react'
import { cn } from '@/lib/utils'

export function KpiGrid({
  children,
  cols,
  className,
}: {
  /** lg+ 档的目标列数：4（诊断）/ 5（缺省）/ 6（用量统计/总览）/ 8（历史）。 */
  cols?: number
  children: ReactNode
  className?: string
}) {
  const lgCols =
    cols === 4
      ? 'lg:grid-cols-4'
      : cols === 5
        ? 'lg:grid-cols-5'
        : cols === 6
          ? 'lg:grid-cols-3 xl:grid-cols-6'
          : cols === 8
            ? 'lg:max-xl:grid-cols-4 xl:grid-cols-8'
            : 'lg:grid-cols-5'
  // divide-x 只跳过第一个子元素，换行后每行行首仍会留下一条竖线。
  const rowStart =
    cols === 4
      ? 'lg:[&>:nth-child(4n+1)]:border-l-0 lg:[&>:nth-child(4n+1)]:pl-0'
      : cols === 5
        ? 'lg:[&>:nth-child(5n+1)]:border-l-0 lg:[&>:nth-child(5n+1)]:pl-0'
        : cols === 6
          ? 'lg:max-xl:[&>:nth-child(3n+1)]:border-l-0 lg:max-xl:[&>:nth-child(3n+1)]:pl-0 xl:[&>:nth-child(6n+1)]:border-l-0 xl:[&>:nth-child(6n+1)]:pl-0'
          : cols === 8
            ? 'lg:max-xl:[&>:nth-child(4n+1)]:border-l-0 lg:max-xl:[&>:nth-child(4n+1)]:pl-0 xl:[&>:nth-child(8n+1)]:border-l-0 xl:[&>:nth-child(8n+1)]:pl-0'
            : 'lg:[&>:nth-child(5n+1)]:border-l-0 lg:[&>:nth-child(5n+1)]:pl-0'
  return (
    <div
      className={cn(
        'relative grid grid-cols-2 gap-y-7 sm:gap-y-9 md:grid-cols-3',
        '[&>*]:border-l [&>*]:border-border/35',
        'max-md:[&>:nth-child(odd)]:border-l-0 max-md:[&>:nth-child(odd)]:pl-0',
        'md:max-lg:[&>:nth-child(3n+1)]:border-l-0 md:max-lg:[&>:nth-child(3n+1)]:pl-0',
        rowStart,
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
  onClick,
  title,
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
  onClick?: () => void
  title?: string
  className?: string
}) {
  const isHero = variant === 'hero'
  const frame = cn(
    'group relative flex flex-col justify-between py-1.5 px-4 sm:px-6 text-left transition-all duration-200',
    onClick && 'cursor-pointer rounded-lg hover:bg-wash/50 active:scale-[0.98]',
    className,
  )
  const body = (
    <>
      <div>
        <div className="flex items-center justify-between gap-2">
          <span className="text-2xs font-semibold uppercase tracking-wider text-muted-foreground/80">
            {label}
          </span>
          {icon && (
            <span className="opacity-60 transition-transform duration-200 group-hover:scale-110 group-hover:opacity-100">
              {icon}
            </span>
          )}
        </div>

        <div className="mt-3.5 flex items-baseline gap-1.5">
          <span
            className={cn(
              'tnum font-display font-medium tracking-tight transition-colors',
              isHero
                ? 'text-3xl sm:text-4xl lg:text-[42px] bg-brand-grad bg-clip-text text-transparent leading-none'
                : 'text-2xl sm:text-[34px] text-foreground leading-none group-hover:text-foreground',
            )}
          >
            {value}
          </span>
          {unit && (
            <span className="font-mono text-xs font-semibold text-muted-foreground/80">
              {unit}
            </span>
          )}
        </div>
      </div>

      {delta && (
        <div
          className={cn(
            'tnum mt-3.5 inline-flex items-center gap-1.5 text-xs font-normal',
            deltaTone === 'up' && 'text-jade',
            deltaTone === 'down' && 'text-ember',
            deltaTone === 'neutral' && 'text-muted-foreground/80',
          )}
        >
          {delta}
        </div>
      )}
    </>
  )
  if (onClick) {
    return (
      <button type="button" onClick={onClick} title={title} className={frame}>
        {body}
      </button>
    )
  }
  return <div className={frame}>{body}</div>
}
