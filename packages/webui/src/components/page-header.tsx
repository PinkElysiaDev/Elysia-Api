import type { ReactNode } from 'react'
import { cn } from '@/lib/utils'

/** 页头：display 字体标题 + 描述 + 右侧操作槽。 */
export function PageHeader({
  title,
  description,
  actions,
  className,
}: {
  title: string
  description?: string
  actions?: ReactNode
  className?: string
}) {
  return (
    <div
      className={cn(
        'flex flex-wrap items-end justify-between gap-x-[18px] gap-y-3 pb-5',
        className,
      )}
    >
      <div className="min-w-0">
        <h1 className="font-display text-2xl font-semibold leading-[1.15] tracking-[0.01em] [text-wrap:balance]">
          {title}
        </h1>
        {description && (
          <p className="mt-[7px] max-w-[56ch] text-sm text-muted-foreground">{description}</p>
        )}
      </div>
      {actions && <div className="flex flex-wrap items-center gap-2.5 max-rail:w-full">{actions}</div>}
    </div>
  )
}
