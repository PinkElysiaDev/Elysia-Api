import { useEffect, useState, type ReactNode } from 'react'
import { cn } from '@/lib/utils'

/** 开合同一时长，避免展开瞬间跳满、收起却要等过渡。 */
export const EXPAND_ROW_MS = 300

type ExpandRowChildren = ReactNode | (() => ReactNode)

/** 下一帧绘制后再跑 callback：单次 rAF 仍可能赶在 0fr 提交之前就把 1fr 设上。 */
function afterPaint(fn: () => void) {
  let inner = 0
  const outer = requestAnimationFrame(() => {
    inner = requestAnimationFrame(fn)
  })
  return () => {
    cancelAnimationFrame(outer)
    cancelAnimationFrame(inner)
  }
}

/** 可展开表格行。关闭稳态卸载内容；打开/收起走 0fr ↔ 1fr。 */
export function ExpandRow({
  open,
  colSpan,
  children,
  className,
}: {
  open: boolean
  colSpan: number
  children: ExpandRowChildren
  className?: string
}) {
  const [mounted, setMounted] = useState(open)
  const [expanded, setExpanded] = useState(open)

  useEffect(() => {
    if (open) {
      setMounted(true)
      return afterPaint(() => setExpanded(true))
    }
    setExpanded(false)
    const id = window.setTimeout(() => setMounted(false), EXPAND_ROW_MS)
    return () => window.clearTimeout(id)
  }, [open])

  if (!mounted) return null

  const content = typeof children === 'function' ? children() : children

  return (
    <tr aria-hidden={!expanded} className="border-0 hover:bg-transparent">
      <td colSpan={colSpan} className="p-0 align-top">
        <div
          className={cn(
            'grid transition-[grid-template-rows] ease-smooth motion-reduce:transition-none',
            expanded ? 'grid-rows-[1fr]' : 'grid-rows-[0fr]',
          )}
          style={{ transitionDuration: `${EXPAND_ROW_MS}ms` }}
        >
          <div className="min-h-0 overflow-hidden">
            <div
              className={cn(
                'border-b bg-secondary/60 px-4 pb-4 pt-3.5',
                expanded ? 'border-border' : 'border-transparent',
                className,
              )}
            >
              {content}
            </div>
          </div>
        </div>
      </td>
    </tr>
  )
}
