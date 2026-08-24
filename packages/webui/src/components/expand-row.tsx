import { useEffect, useState, type ReactNode } from 'react'
import { cn } from '@/lib/utils'

const CLOSE_MS = 300

type ExpandRowChildren = ReactNode | (() => ReactNode)

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
      const id = requestAnimationFrame(() => setExpanded(true))
      return () => cancelAnimationFrame(id)
    }
    setExpanded(false)
    const id = window.setTimeout(() => setMounted(false), CLOSE_MS)
    return () => window.clearTimeout(id)
  }, [open])

  if (!mounted) return null

  const content = typeof children === 'function' ? children() : children

  return (
    <tr aria-hidden={!expanded} {...(!expanded ? { inert: '' } : {})}>
      <td colSpan={colSpan} className="p-0 align-top">
        <div
          className={cn(
            'grid transition-[grid-template-rows] duration-300 ease-smooth',
            expanded ? 'grid-rows-[1fr]' : 'grid-rows-[0fr]',
          )}
        >
          <div className="overflow-hidden">
            <div className={cn('border-b border-border bg-secondary/60 px-4 pb-4 pt-3.5', className)}>{content}</div>
          </div>
        </div>
      </td>
    </tr>
  )
}
