import type { ReactNode } from 'react'
import { Check } from 'lucide-react'
import { cn } from '@/lib/utils'

/**
 * 可选中芯片：选中 = 玫红描边 + 淡玫底 + 内嵌 check 方块；未选 = 素边灰字。
 * 统一此前在 KeyModelsPanel / 手动模型 Key 分配 / 组表单模型选项里
 * 各自手写三遍的同款交互（替代迷你降级开关做能力/多选切换）。
 */
export function ToggleChip({
  selected,
  onToggle,
  disabled,
  className,
  children,
  title,
}: {
  selected: boolean
  onToggle: () => void
  disabled?: boolean
  className?: string
  children: ReactNode
  title?: string
}) {
  return (
    <button
      type="button"
      title={title}
      disabled={disabled}
      aria-pressed={selected}
      onClick={onToggle}
      className={cn(
        'flex h-7 items-center gap-1.5 rounded-md border px-2 text-xs transition-colors',
        selected
          ? 'border-primary/50 bg-primary/10 text-primary'
          : 'border-border/60 text-muted-foreground hover:bg-accent',
        disabled && 'pointer-events-none opacity-50',
        className,
      )}
    >
      <span
        className={cn(
          'flex h-3 w-3 shrink-0 items-center justify-center rounded border',
          selected
            ? 'border-primary bg-primary text-primary-foreground'
            : 'border-border',
        )}
      >
        {selected && <Check className="h-2 w-2" strokeWidth={3} />}
      </span>
      {children}
    </button>
  )
}
