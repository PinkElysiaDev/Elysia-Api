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
  className,
  'aria-label': ariaLabel,
}: {
  options: SegOption<T>[]
  value: T
  onChange: (value: T) => void
  className?: string
  'aria-label'?: string
}) {
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
              'px-[13px] py-1.5 text-xs transition-colors duration-150',
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
