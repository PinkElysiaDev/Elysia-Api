import { cn } from '@/lib/utils'

export interface SegOption<T extends string | number> {
  value: T
  label: string
}

/**
 * 分段控件：7px 圆角的连体按钮组，选中项 wash 底 + 玫红字。
 */
export function Seg<T extends string | number>({
  options,
  value,
  onChange,
  size = 'md',
  className,
  'aria-label': ariaLabel,
}: {
  options: SegOption<T>[]
  value: T
  onChange: (value: T) => void
  size?: 'md' | 'lg'
  className?: string
  'aria-label'?: string
}) {
  const sizing =
    size === 'lg'
      ? 'min-h-[34px] min-w-[4.5rem] px-3.5 text-xs'
      : 'min-h-[28px] px-2.5 text-xs'
  return (
    <div
      role="group"
      aria-label={ariaLabel}
      className={cn('inline-flex overflow-hidden rounded-[7px] border border-input bg-card', className)}
    >
      {options.map((opt, i) => {
        const on = opt.value === value
        return (
          <button
            type="button"
            key={String(opt.value)}
            aria-pressed={on}
            onClick={() => onChange(opt.value)}
            className={cn(
              'inline-flex h-full items-center justify-center whitespace-nowrap leading-none transition-colors duration-150 focus-visible:z-10 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring',
              sizing,
              i > 0 && 'border-l border-border',
              on ? 'bg-wash font-semibold text-rose' : 'text-muted-foreground hover:text-foreground',
            )}
          >
            {opt.label}
          </button>
        )
      })}
    </div>
  )
}
