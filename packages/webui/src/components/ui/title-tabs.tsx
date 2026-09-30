import { useCallback, useLayoutEffect, useRef, useState, type ReactNode } from 'react'
import * as Tabs from '@radix-ui/react-tabs'
import type { LucideIcon } from 'lucide-react'
import { cn } from '@/lib/utils'

/** 标题即 Tab：无边框文字切换，底部 2px 瑰梅游标平滑滑移。 */
export function TitleTabs<T extends string>({
  options,
  value,
  'aria-label': ariaLabel,
}: {
  options: { value: T; label: ReactNode; icon?: LucideIcon }[]
  value: T
  'aria-label'?: string
}) {
  const listRef = useRef<HTMLDivElement>(null)
  const btnRefs = useRef(new Map<T, HTMLButtonElement>())
  const [bar, setBar] = useState({ left: 0, width: 0 })

  const measure = useCallback(() => {
    const list = listRef.current
    const btn = btnRefs.current.get(value)
    if (!list || !btn) return
    const lr = list.getBoundingClientRect()
    const br = btn.getBoundingClientRect()
    setBar({ left: br.left - lr.left + list.scrollLeft, width: br.width })
  }, [value])

  const optionKey = options.map((o) => `${o.value}:${String(o.label)}`).join('|')
  useLayoutEffect(() => {
    measure()
    const list = listRef.current
    if (!list) return
    const observer = new ResizeObserver(measure)
    observer.observe(list)
    for (const btn of btnRefs.current.values()) observer.observe(btn)
    return () => observer.disconnect()
  }, [measure, optionKey])

  return (
    <div className="min-w-0 overflow-x-auto [scrollbar-width:none] [&::-webkit-scrollbar]:hidden">
      <Tabs.List ref={listRef} aria-label={ariaLabel} className="relative flex w-max min-w-full items-center gap-8">
        {options.map((opt) => {
          const on = opt.value === value
          const Icon = opt.icon
          return (
            <Tabs.Trigger
              key={opt.value}
              value={opt.value}
              ref={(el) => {
                if (el) btnRefs.current.set(opt.value, el)
                else btnRefs.current.delete(opt.value)
              }}
              className={cn(
                'inline-flex shrink-0 items-center gap-2 whitespace-nowrap pb-2.5 pt-1 text-sm tracking-tight transition-colors duration-200 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring max-rail:min-h-11',
                on
                  ? 'font-semibold text-foreground'
                  : 'font-medium text-muted-foreground hover:text-foreground',
              )}
            >
              {Icon && <Icon className={cn('h-4 w-4', on ? 'text-primary' : 'text-muted-foreground/80')} />}
              {opt.label}
            </Tabs.Trigger>
          )
        })}
        <span
          aria-hidden
          className="pointer-events-none absolute bottom-0 h-0.5 rounded-full bg-rose transition-[left,width] duration-300 ease-out motion-reduce:transition-none"
          style={{ left: bar.left, width: bar.width }}
        />
      </Tabs.List>
    </div>
  )
}
