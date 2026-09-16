import { useEffect, useId, useLayoutEffect, useMemo, useRef, useState } from 'react'
import { Check, ChevronDown, Search, X } from 'lucide-react'
import { cn } from '@/lib/utils'
import { Z_INDEX } from '@/lib/z-index'

export interface MultiSelectOption {
  value: string
  label: string
  /** 可选副标题，例如模型所属的模型组，便于区分同名项。 */
  hint?: string
}

/** 搜索多选：方向键定位、Enter 切换选中、Escape 关闭，Tab 自然离开筛选。 */
export function MultiSelect({
  label,
  options,
  value,
  onChange,
  searchPlaceholder = '搜索…',
  emptyText = '暂无选项',
}: {
  label: string
  options: (string | MultiSelectOption)[]
  value: string[]
  onChange: (value: string[]) => void
  searchPlaceholder?: string
  emptyText?: string
}) {
  const id = useId()
  const panelId = `${id}-panel`
  const listId = `${id}-list`
  const normalized = useMemo(
    () => options.map((o) => (typeof o === 'string' ? { value: o, label: o } : o)),
    [options],
  )
  const [open, setOpen] = useState(false)
  const [query, setQuery] = useState('')
  const [activeValue, setActiveValue] = useState<string | null>(null)
  const [panelPosition, setPanelPosition] = useState({ left: 0, above: false, maxHeight: 360 })
  const rootRef = useRef<HTMLDivElement>(null)
  const triggerRef = useRef<HTMLButtonElement>(null)
  const panelRef = useRef<HTMLDivElement>(null)
  const searchRef = useRef<HTMLInputElement>(null)
  const optionRefs = useRef(new Map<string, HTMLDivElement>())

  useEffect(() => {
    if (!open) return
    const dismiss = (event: PointerEvent) => {
      if (!rootRef.current?.contains(event.target as Node)) setOpen(false)
    }
    document.addEventListener('pointerdown', dismiss)
    return () => document.removeEventListener('pointerdown', dismiss)
  }, [open])

  // 基于真实弹层宽度避让左右边缘；旋转屏幕或筛选计数改变时重新定位。
  useLayoutEffect(() => {
    if (!open) return
    const position = () => {
      if (!rootRef.current || !panelRef.current) return
      const rect = rootRef.current.getBoundingClientRect()
      const width = panelRef.current.getBoundingClientRect().width
      const left = Math.max(16, Math.min(rect.left, window.innerWidth - width - 16))
      const below = window.innerHeight - rect.bottom - 22
      const above = rect.top - 22
      const placeAbove = below < 240 && above > below
      const next = { left: left - rect.left, above: placeAbove, maxHeight: Math.max(100, placeAbove ? above : below) }
      setPanelPosition((prev) => prev.left === next.left && prev.above === next.above && prev.maxHeight === next.maxHeight ? prev : next)
    }
    position()
    window.addEventListener('resize', position)
    window.addEventListener('scroll', position, true)
    return () => {
      window.removeEventListener('resize', position)
      window.removeEventListener('scroll', position, true)
    }
  }, [open, value.length])

  useEffect(() => {
    if (open) searchRef.current?.focus()
    else {
      setQuery('')
      setActiveValue(null)
    }
  }, [open])

  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase()
    return q
      ? normalized.filter((o) => o.label.toLowerCase().includes(q) || o.hint?.toLowerCase().includes(q))
      : normalized
  }, [normalized, query])
  const selected = useMemo(() => new Set(value), [value])
  const activeIndex = filtered.findIndex((option) => option.value === activeValue)

  useEffect(() => {
    if (activeValue !== null) optionRefs.current.get(activeValue)?.scrollIntoView({ block: 'nearest' })
  }, [activeValue])

  function toggle(optionValue: string) {
    onChange(selected.has(optionValue) ? value.filter((v) => v !== optionValue) : [...value, optionValue])
  }

  const hasSelection = value.length > 0

  return (
    <div
      ref={rootRef}
      className="relative"
      onBlur={(event) => {
        if (!event.currentTarget.contains(event.relatedTarget as Node | null)) setOpen(false)
      }}
      onKeyDown={(event) => {
        if (open && event.key === 'Escape') {
          event.preventDefault()
          event.stopPropagation()
          setOpen(false)
          triggerRef.current?.focus()
        }
      }}
    >
      <div
        className={cn(
          'flex min-h-[34px] items-center rounded-full border text-sm transition-colors duration-150 max-rail:min-h-11',
          hasSelection
            ? 'border-[color:color-mix(in_srgb,var(--rose)_30%,transparent)] bg-wash text-rose'
            : 'border-transparent bg-[var(--well)] text-muted-foreground hover:text-foreground',
        )}
      >
        <button
          ref={triggerRef}
          type="button"
          aria-expanded={open}
          aria-haspopup="dialog"
          aria-controls={open ? panelId : undefined}
          aria-label={`${label}筛选${hasSelection ? `（已选 ${value.length} 项）` : ''}`}
          onClick={() => setOpen((prev) => !prev)}
          onKeyDown={(event) => {
            if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
              event.preventDefault()
              setOpen(true)
              // panel 已开时（焦点经 Shift+Tab 回到触发器），把焦点送回搜索框恢复方向键导航。
              if (open) searchRef.current?.focus()
            }
          }}
          className="flex min-h-[32px] items-center gap-1.5 whitespace-nowrap rounded-full px-3.5 max-rail:min-h-11"
        >
          <span className={hasSelection ? 'font-medium' : undefined}>{label}</span>
          {hasSelection && <span className="font-mono text-xs">· {value.length}</span>}
          <ChevronDown aria-hidden className={cn('h-3.5 w-3.5 opacity-60 transition-transform', open && 'rotate-180')} />
        </button>
        {hasSelection && (
          <button
            type="button"
            aria-label={`清空${label}选择`}
            className="mr-1 flex h-7 w-7 items-center justify-center rounded-full hover:bg-background/60 hover:text-foreground max-rail:h-11 max-rail:w-11"
            onClick={() => {
              onChange([])
              triggerRef.current?.focus()
            }}
          >
            <X className="h-3.5 w-3.5" />
          </button>
        )}
      </div>

      {open && (
        <div
          ref={panelRef}
          id={panelId}
          role="dialog"
          aria-label={`${label}筛选选项`}
          style={{
            left: panelPosition.left,
            top: panelPosition.above ? undefined : 'calc(100% + 6px)',
            bottom: panelPosition.above ? 'calc(100% + 6px)' : undefined,
            maxHeight: panelPosition.maxHeight,
          }}
          className={cn("absolute flex w-72 max-w-[calc(100vw-2rem)] flex-col overflow-hidden rounded-xl border border-border bg-popover text-popover-foreground shadow-lg transition-none animate-in fade-in-0 duration-150", Z_INDEX.multiSelectPanel)}
        >
          <div className="flex shrink-0 items-center gap-2 border-b border-border/60 px-3 py-2.5">
            <Search aria-hidden className="h-4 w-4 shrink-0 text-muted-foreground" />
            <input
              ref={searchRef}
              role="combobox"
              aria-label={`搜索${label}`}
              aria-autocomplete="list"
              aria-expanded={open}
              aria-controls={listId}
              aria-activedescendant={activeIndex >= 0 ? `${id}-option-${activeIndex}` : undefined}
              value={query}
              onChange={(event) => {
                setQuery(event.target.value)
                setActiveValue(null)
              }}
              onKeyDown={(event) => {
                if (event.nativeEvent.isComposing) return
                if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
                  event.preventDefault()
                  if (!filtered.length) return
                  const next = activeIndex < 0
                    ? (event.key === 'ArrowDown' ? 0 : filtered.length - 1)
                    : (activeIndex + (event.key === 'ArrowDown' ? 1 : -1) + filtered.length) % filtered.length
                  setActiveValue(filtered[next].value)
                } else if (event.key === 'Enter') {
                  event.preventDefault()
                  if (activeIndex >= 0) toggle(filtered[activeIndex].value)
                }
              }}
              placeholder={searchPlaceholder}
              className="min-w-0 w-full bg-transparent text-sm outline-none focus-visible:outline-none placeholder:text-muted-foreground max-rail:min-h-6 max-rail:text-base"
            />
          </div>
          <div id={listId} role="listbox" aria-label={label} aria-multiselectable="true" className="min-h-0 max-h-60 overflow-auto overscroll-contain p-1">
            {filtered.map((option, index) => {
              const checked = selected.has(option.value)
              return (
                <div
                  key={option.value}
                  id={`${id}-option-${index}`}
                  ref={(element) => {
                    if (element) optionRefs.current.set(option.value, element)
                    else optionRefs.current.delete(option.value)
                  }}
                  role="option"
                  aria-selected={checked}
                  onMouseDown={(event) => event.preventDefault()}
                  onClick={() => {
                    setActiveValue(option.value)
                    toggle(option.value)
                    searchRef.current?.focus()
                  }}
                  className={cn(
                    'flex cursor-pointer items-center gap-2 rounded-md px-2.5 py-2 text-sm transition-colors max-rail:min-h-11',
                    checked ? 'font-medium text-rose' : 'text-foreground',
                    index === activeIndex ? 'bg-wash ring-1 ring-inset ring-ring/30' : 'hover:bg-wash',
                  )}
                >
                  <span aria-hidden className={cn('flex h-4 w-4 shrink-0 items-center justify-center rounded border', checked ? 'border-primary bg-primary text-primary-foreground' : 'border-input')}>
                    {checked && <Check className="h-3 w-3" strokeWidth={3} />}
                  </span>
                  <span className="min-w-0 flex-1 break-words">
                    {option.label}
                    {option.hint && <span className="mt-0.5 block text-xs font-normal text-muted-foreground">{option.hint}</span>}
                  </span>
                </div>
              )
            })}
          </div>
          {filtered.length === 0 && (
            <p role="status" className="px-3 py-4 text-center text-xs text-muted-foreground">
              {query.trim() ? '没有匹配的选项，请尝试其他关键词' : emptyText}
            </p>
          )}
          <div className="shrink-0 border-t border-border/60 px-3 py-2 text-xs text-muted-foreground" role="status">
            {hasSelection ? `已选择 ${value.length} 项` : '可选择多个选项'}
          </div>
        </div>
      )}
    </div>
  )
}
