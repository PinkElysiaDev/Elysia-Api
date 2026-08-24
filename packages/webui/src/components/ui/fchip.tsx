import { cn } from '@/lib/utils'

/**
 * 图例胶囊：999px 圆角 + 可选色点，选中玫红描边加粗。
 * 图表系列开关 / 筛选 chip 用。
 */
export function Fchip({
  label,
  color,
  active = true,
  onClick,
  className,
}: {
  label: string
  /** 色点颜色（CSS 色），不传则无点 */
  color?: string
  active?: boolean
  onClick?: () => void
  className?: string
}) {
  const clickable = onClick != null
  return (
    <button
      type="button"
      onClick={onClick}
      aria-pressed={clickable ? active : undefined}
      disabled={!clickable}
      className={cn(
        'inline-flex h-[29px] items-center gap-1.5 rounded-full border border-input bg-card px-3 text-xs text-muted-foreground transition-colors duration-150',
        clickable && 'hover:border-foreground/40 hover:text-foreground',
        active && 'border-rose bg-wash font-semibold text-rose',
        className,
      )}
    >
      {color && (
        <span
          aria-hidden
          className="h-[7px] w-[7px] shrink-0 rounded-full"
          style={{ background: color }}
        />
      )}
      {label}
    </button>
  )
}
