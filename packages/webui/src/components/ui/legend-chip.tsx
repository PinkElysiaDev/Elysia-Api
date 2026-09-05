import { cn } from '@/lib/utils'

/**
 * 图例胶囊：999px 圆角 + 可选色点。多选开关语义（与 Seg 的单选档位区分）。
 *
 * 材质沿用系统原生的发丝线圈 + 卡片底：未选中就是安静的描边胶囊（色点
 * 降透暗示该系列当前隐藏）；选中只发生一次安静的染色——底色与描边染上
 * 系列色（「这个 chip 就是图表里那条线」），色点点亮。不引入玻璃/渐变/
 * 高光等外来材质，文字恒定字重避免选中宽度重排；未传色点的开关回落玫红
 * wash。
 */
export function LegendChip({
  label,
  color,
  active = true,
  onClick,
  className,
}: {
  label: string
  /** 色点颜色（CSS 色，可为 var(--x)），不传则无点 */
  color?: string
  active?: boolean
  onClick?: () => void
  className?: string
}) {
  const clickable = onClick != null
  const tinted = active && color != null
  return (
    <button
      type="button"
      onClick={onClick}
      aria-pressed={clickable ? active : undefined}
      disabled={!clickable}
      style={
        tinted
          ? {
              background: `color-mix(in srgb, ${color} 8%, transparent)`,
              borderColor: `color-mix(in srgb, ${color} 35%, transparent)`,
            }
          : undefined
      }
      className={cn(
        'inline-flex h-[29px] items-center gap-1.5 rounded-full border border-input bg-card px-3 text-xs text-muted-foreground transition-colors duration-200',
        clickable && !active && 'hover:border-foreground/40 hover:text-foreground',
        tinted && 'text-foreground',
        active && !color && 'border-rose bg-wash text-rose',
        className,
      )}
    >
      {color && (
        <span
          aria-hidden
          className={cn(
            'h-[7px] w-[7px] shrink-0 rounded-full transition-opacity duration-200',
            !active && 'opacity-35',
          )}
          style={{ background: color }}
        />
      )}
      {label}
    </button>
  )
}
