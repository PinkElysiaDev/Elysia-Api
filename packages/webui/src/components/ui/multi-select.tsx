import { useEffect, useMemo, useRef, useState } from 'react'
import { Check, ChevronDown, Search, X } from 'lucide-react'
import { cn } from '@/lib/utils'

export interface MultiSelectOption {
  value: string
  label: string
  /** 可选副标题，例如模型所属的模型组，便于区分同名项。 */
  hint?: string
}

/**
 * 带搜索的复选下拉菜单。受控组件：value 为已选值数组，onChange 回传新数组。
 * 自实现弹层（而非 Radix DropdownMenu），以便内嵌搜索框不被菜单的焦点管理抢走输入。
 */
export function MultiSelect({
  options,
  value,
  onChange,
  placeholder = '全部',
  searchPlaceholder = '搜索…',
  emptyText = '暂无选项',
  className,
}: {
  options: MultiSelectOption[]
  value: string[]
  onChange: (value: string[]) => void
  placeholder?: string
  searchPlaceholder?: string
  emptyText?: string
  className?: string
}) {
  const [open, setOpen] = useState(false)
  const [query, setQuery] = useState('')
  const rootRef = useRef<HTMLDivElement>(null)
  const searchRef = useRef<HTMLInputElement>(null)

  // 点击外部 / Esc 关闭。
  useEffect(() => {
    if (!open) return
    function onPointerDown(e: MouseEvent) {
      if (rootRef.current && !rootRef.current.contains(e.target as Node)) setOpen(false)
    }
    function onKeyDown(e: KeyboardEvent) {
      if (e.key === 'Escape') setOpen(false)
    }
    document.addEventListener('mousedown', onPointerDown)
    document.addEventListener('keydown', onKeyDown)
    return () => {
      document.removeEventListener('mousedown', onPointerDown)
      document.removeEventListener('keydown', onKeyDown)
    }
  }, [open])

  // 打开时聚焦搜索框，关闭时清空查询。
  useEffect(() => {
    if (open) {
      const id = window.setTimeout(() => searchRef.current?.focus(), 0)
      return () => window.clearTimeout(id)
    }
    setQuery('')
  }, [open])

  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase()
    if (!q) return options
    return options.filter(
      (o) => o.label.toLowerCase().includes(q) || (o.hint?.toLowerCase().includes(q) ?? false),
    )
  }, [options, query])

  const selected = useMemo(() => new Set(value), [value])

  function toggle(optionValue: string) {
    if (selected.has(optionValue)) {
      onChange(value.filter((v) => v !== optionValue))
    } else {
      onChange([...value, optionValue])
    }
  }

  const caption =
    value.length === 0 ? placeholder : value.length === 1 ? labelFor(options, value[0]) : `已选 ${value.length} 项`

  return (
    <div ref={rootRef} className={cn('relative', className)}>
      {/* 用 div[role=combobox] 而非 <button> 作触发器：清空控件需要是真实
          <button>，嵌套在 <button> 里是非法 HTML（修复 W1）。键盘可达：
          Enter/Space/↓ 打开（W3）。 */}
      <div
        role="combobox"
        tabIndex={0}
        aria-expanded={open}
        aria-haspopup="listbox"
        onClick={() => setOpen((v) => !v)}
        onKeyDown={(e) => {
          if (e.key === 'Enter' || e.key === ' ' || e.key === 'ArrowDown') {
            e.preventDefault()
            setOpen(true)
          }
        }}
        className={cn(
          'flex h-10 w-full cursor-pointer items-center justify-between gap-2 rounded-lg border border-input bg-background/60 px-3 py-2 text-sm shadow-sm',
          'focus:outline-none focus:ring-2 focus:ring-ring focus:border-primary',
        )}
      >
        <span className={cn('line-clamp-1 text-left', value.length === 0 && 'text-muted-foreground')}>
          {caption}
        </span>
        <span className="flex items-center gap-1">
          {value.length > 0 && (
            <button
              type="button"
              aria-label="清空选择"
              className="rounded p-0.5 text-muted-foreground hover:text-foreground focus:outline-none focus-visible:ring-2 focus-visible:ring-ring"
              onClick={(e) => {
                e.stopPropagation()
                onChange([])
              }}
            >
              <X className="h-3.5 w-3.5" />
            </button>
          )}
          <ChevronDown className="h-4 w-4 opacity-60" />
        </span>
      </div>

      {open && (
        <div className="absolute z-50 mt-1 w-full min-w-[12rem] overflow-hidden rounded-lg border border-border bg-popover text-popover-foreground shadow-glow">
          <div className="flex items-center gap-2 border-b border-border px-2.5 py-2">
            <Search className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
            <input
              ref={searchRef}
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              placeholder={searchPlaceholder}
              className="w-full bg-transparent text-sm outline-none placeholder:text-muted-foreground"
            />
          </div>
          <div role="listbox" aria-multiselectable className="max-h-60 overflow-auto p-1">
            {filtered.length === 0 ? (
              <p className="px-3 py-4 text-center text-xs text-muted-foreground">{emptyText}</p>
            ) : (
              filtered.map((option) => {
                const checked = selected.has(option.value)
                return (
                  <button
                    key={option.value}
                    type="button"
                    role="option"
                    aria-selected={checked}
                    onClick={() => toggle(option.value)}
                    className={cn(
                      'flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left text-sm outline-none',
                      'hover:bg-accent hover:text-accent-foreground focus-visible:bg-accent',
                    )}
                  >
                    <span
                      className={cn(
                        'flex h-4 w-4 shrink-0 items-center justify-center rounded border',
                        checked ? 'border-primary bg-primary text-primary-foreground' : 'border-input',
                      )}
                    >
                      {checked && <Check className="h-3 w-3" />}
                    </span>
                    <span className="flex-1 truncate">
                      {option.label}
                      {option.hint && (
                        <span className="ml-1.5 text-xs text-muted-foreground">{option.hint}</span>
                      )}
                    </span>
                  </button>
                )
              })
            )}
          </div>
        </div>
      )}
    </div>
  )
}

function labelFor(options: MultiSelectOption[], value: string): string {
  return options.find((o) => o.value === value)?.label ?? value
}
