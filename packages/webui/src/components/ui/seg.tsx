import { useLayoutEffect, useRef, useState } from 'react'
import { cn } from '@/lib/utils'

export interface SegOption<T extends string | number> {
  value: T
  label: string
}

/**
 * 分段控件：凹槽轨道 + 凸起瓷键（材质 token 见 index.css --well / --seg-key）。
 * 选中 = text-rose，文字恒定字重；滑块 300ms transform 滑移。
 * 高度三档：sm 29 / md 34（与 Input、Button 一致）/ lg 38。
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
  size?: 'sm' | 'md' | 'lg'
  className?: string
  'aria-label'?: string
}) {
  const containerRef = useRef<HTMLDivElement>(null)
  const buttonRefs = useRef(new Map<T, HTMLButtonElement>())
  const [thumb, setThumb] = useState<{ left: number; width: number } | null>(null)

  const sizing =
    size === 'lg'
      ? 'min-h-[34px] min-w-[4.5rem] px-3.5 text-xs'
      : size === 'md'
        ? 'min-h-[30px] px-3 text-xs'
        : 'min-h-[25px] px-2.5 text-xs'

  useLayoutEffect(() => {
    const measure = () => {
      const btn = buttonRefs.current.get(value)
      if (btn) setThumb({ left: btn.offsetLeft, width: btn.offsetWidth })
    }
    measure()
    const container = containerRef.current
    if (!container || typeof ResizeObserver === 'undefined') return
    const ro = new ResizeObserver(measure)
    ro.observe(container)
    return () => ro.disconnect()
  }, [value, options])

  return (
    <div
      ref={containerRef}
      role="group"
      aria-label={ariaLabel}
      className={cn(
        'relative inline-flex rounded-full border border-[color:var(--well-border)] bg-[var(--well)] p-[2px]',
        // shadow-[var(--x)] 会被 Tailwind 误判成 shadow-color，须用任意属性语法
        '[box-shadow:var(--well-shadow)]',
        className,
      )}
    >
      {thumb && (
        <span
          aria-hidden
          className={cn(
            'absolute bottom-[2px] left-0 top-[2px] rounded-full border border-[color:var(--seg-key-border)]',
            'bg-[linear-gradient(180deg,var(--seg-key-a),var(--seg-key-b))]',
            '[box-shadow:var(--seg-key-shadow)]',
            'transition-[transform,width] duration-300 ease-smooth motion-reduce:transition-none',
          )}
          style={{ width: thumb.width, transform: `translateX(${thumb.left}px)` }}
        />
      )}
      {options.map((opt) => {
        const on = opt.value === value
        return (
          <button
            type="button"
            key={String(opt.value)}
            ref={(el) => {
              if (el) buttonRefs.current.set(opt.value, el)
              else buttonRefs.current.delete(opt.value)
            }}
            aria-pressed={on}
            onClick={() => onChange(opt.value)}
            className={cn(
              'relative z-10 inline-flex items-center justify-center whitespace-nowrap rounded-full leading-none transition-colors duration-200 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring',
              sizing,
              on ? 'text-rose' : 'text-muted-foreground hover:text-foreground',
            )}
          >
            {opt.label}
          </button>
        )
      })}
    </div>
  )
}
