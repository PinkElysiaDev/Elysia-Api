import type { ReactNode } from 'react'
import { cn } from '@/lib/utils'

/** 区块标题：菱形标记、可选计数、右侧 tools。 */
export function SectionHeader({
  title,
  count,
  tools,
  sm = false,
  className,
}: {
  title: ReactNode
  /** 标题旁的弱化计数。 */
  count?: ReactNode
  tools?: ReactNode
  /** 三栏小区块使用的小号样式。 */
  sm?: boolean
  className?: string
}) {
  return (
    <div
      className={cn(
        'mb-[18px] flex flex-wrap items-baseline justify-between gap-x-4 gap-y-2',
        sm && 'mb-2',
        className,
      )}
    >
      <h2
        className={cn(
          'flex items-center gap-2.5 font-display font-semibold tracking-[0.01em]',
          sm ? 'text-base' : 'text-xl',
        )}
      >
        <span
          aria-hidden
          className="h-2 w-2 shrink-0 rotate-45 rounded-[1.5px] bg-brand-grad"
        />
        {title}
        {count != null && (
          <span className="font-sans text-xs font-normal tracking-normal text-muted-foreground">{count}</span>
        )}
      </h2>
      {tools && (
        <div className="ml-auto flex flex-wrap items-center justify-end gap-2.5">{tools}</div>
      )}
    </div>
  )
}
