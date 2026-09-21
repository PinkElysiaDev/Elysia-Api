import { useState, type ReactNode } from 'react'
import { ChevronDown } from 'lucide-react'
import { cn } from '@/lib/utils'
import { colorize } from '@/lib/json-highlight'

/** 可折叠区块：消息卡与上下文面板共用（tone 控制边框风格，defaultOpen 控制初始态）。 */
export function Collapse({
  title,
  icon,
  children,
  tone = 'muted',
  defaultOpen = false,
  stopPropagation = false,
}: {
  title: string
  icon?: ReactNode
  children: ReactNode
  tone?: 'muted' | 'amber'
  defaultOpen?: boolean
  /** 嵌在可点击卡片内时阻止事件冒泡（消息卡里的工具详情块）。 */
  stopPropagation?: boolean
}) {
  const [open, setOpen] = useState(defaultOpen)
  return (
    <div
      className={cn(
        'rounded-md border text-xs',
        tone === 'amber' ? 'border-amber/30 bg-amber/5' : 'border-border bg-muted/40',
      )}
    >
      <button
        type="button"
        className="flex w-full items-center gap-1.5 px-2.5 py-1.5 text-left text-muted-foreground hover:text-foreground"
        onClick={(event) => {
          if (stopPropagation) event.stopPropagation()
          setOpen((value) => !value)
        }}
      >
        {icon}
        <span className="flex-1 truncate">{title}</span>
        <ChevronDown className={cn('h-3.5 w-3.5 transition-transform', open && 'rotate-180')} />
      </button>
      {open && <div className="border-t border-border/60 px-2.5 py-2">{children}</div>}
    </div>
  )
}

/** JSON/文本展示块：语法高亮 + 可控最大高度。 */
export function JsonBlock({ value, maxHeight = 'max-h-72' }: { value: unknown; maxHeight?: string }) {
  const text = typeof value === 'string' ? value : JSON.stringify(value, null, 2) ?? ''
  return (
    <pre
      className={cn('overflow-auto whitespace-pre rounded-[7px] border border-border bg-code px-3 py-2.5 font-mono text-2xs leading-[1.7]', maxHeight)}
      dangerouslySetInnerHTML={{ __html: colorize(text) }}
    />
  )
}
