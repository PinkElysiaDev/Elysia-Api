import { useEffect, useMemo, useRef, useState } from 'react'
import { ChevronDown, Search, X } from 'lucide-react'
import { cn } from '@/lib/utils'

export interface MultiSelectOption {
  value: string
  label: string
  /** 可选副标题，例如模型所属的模型组，便于区分同名项。 */
  hint?: string
}

/**
 * 筛选胶囊下拉：紧凑触发器（「模型 · 2」）+ 搜索复选弹层，受控组件。
 * 触发器是筛选工具栏的统一形态：未选时中性灰，激活时 wash 底 + 玫红字 +
 * 已选计数与快捷清除。自实现弹层（而非 Radix DropdownMenu），以便内嵌
 * 搜索框不被菜单的焦点管理抢走输入。
 */
export function MultiSelect({
  label,
  options,
  value,
  onChange,
  searchPlaceholder = '搜索…',
  emptyText = '暂无选项',
}: {
  /** 触发器上展示的维度名（如「模型组」）。 */
  label: string
  /** 支持纯字符串数组（value=label）或带 hint 的完整选项对象。 */
  options: (string | MultiSelectOption)[]
  value: string[]
  onChange: (value: string[]) => void
  searchPlaceholder?: string
  emptyText?: string
}) {
  // 归一化为 MultiSelectOption[]：string 项即 value=label。
  const normalized = useMemo(
    () => options.map((o) => (typeof o === 'string' ? { value: o, label: o } : o)),
    [options],
  )
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
    if (!q) return normalized
    return normalized.filter(
      (o) => o.label.toLowerCase().includes(q) || (o.hint?.toLowerCase().includes(q) ?? false),
    )
  }, [normalized, query])

  const selected = useMemo(() => new Set(value), [value])

  function toggle(optionValue: string) {
    if (selected.has(optionValue)) {
      onChange(value.filter((v) => v !== optionValue))
    } else {
      onChange([...value, optionValue])
    }
    // 选项按钮获得焦点后键盘输入不再进搜索框——点选后立即还焦。
    if (open) {
      window.setTimeout(() => searchRef.current?.focus(), 0)
    }
  }

  const active = value.length > 0

  // 右缘溢出时弹层改为右对齐（body overflow-x: clip 会静默裁掉溢出部分）。
  const [flipRight, setFlipRight] = useState(false)
  useEffect(() => {
    if (!open || !rootRef.current) return
    const rect = rootRef.current.getBoundingClientRect()
    setFlipRight(rect.left + 288 > window.innerWidth)
  }, [open])

  return (
    <div ref={rootRef} className="relative">
      {/* 用 div[role=combobox] 而非 <button> 作触发器：清空控件需要是真实
          <button>，嵌套在 <button> 里是非法 HTML。键盘可达：Enter/Space/↓ 打开。 */}
      <div
        role="combobox"
        tabIndex={0}
        aria-expanded={open}
        aria-haspopup="listbox"
        aria-label={`${label}筛选${active ? `（已选 ${value.length} 项）` : ''}`}
        onClick={() => setOpen((v) => !v)}
        onKeyDown={(e) => {
          // 只响应触发器自身的按键：内部清空按钮的 Enter/Space 冒泡到此处
          // 会被 preventDefault 吞掉（浏览器不再合成 click），键盘无法清空。
          if (e.target !== e.currentTarget) return
          if (e.key === 'Enter' || e.key === ' ' || e.key === 'ArrowDown') {
            e.preventDefault()
            setOpen(true)
          }
        }}
        className={cn(
          'flex h-[34px] cursor-pointer select-none items-center gap-1.5 rounded-md border px-3 text-sm transition-colors duration-150',
          'focus:outline-none focus-visible:border-rose focus-visible:ring-[3px] focus-visible:ring-wash',
          active
            ? 'border-rose/30 bg-wash text-rose'
            : 'border-input bg-card text-muted-foreground hover:text-foreground',
        )}
      >
        <span className="whitespace-nowrap">
          {active ? (
            <>
              <span className="font-medium">{label}</span>
              <span className="ml-1 font-mono text-xs">· {value.length}</span>
            </>
          ) : (
            label
          )}
        </span>
        <span className="flex items-center gap-0.5">
          {active && (
            <button
              type="button"
              aria-label="清空选择"
              className="rounded p-0.5 hover:text-foreground focus:outline-none focus-visible:ring-2 focus-visible:ring-ring"
              onClick={(e) => {
                e.stopPropagation()
                onChange([])
              }}
            >
              <X className="h-3 w-3" />
            </button>
          )}
          <ChevronDown className="h-3.5 w-3.5 opacity-60" />
        </span>
      </div>

      {open && (
        <div
          className={cn(
            'absolute z-50 mt-1.5 w-max min-w-[11rem] max-w-[16rem] overflow-hidden rounded-md border border-border bg-popover text-popover-foreground shadow-soft',
            flipRight ? 'right-0' : 'left-0',
          )}
        >
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
          <div role="listbox" aria-multiselectable className="hide-scrollbar max-h-60 overflow-auto p-1">
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
                      'flex w-full items-center gap-2 rounded px-2.5 py-1.5 text-left text-sm outline-none transition-colors duration-100',
                      checked
                        ? 'bg-wash font-medium text-rose'
                        : 'text-foreground hover:bg-wash focus:bg-wash',
                    )}
                  >
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
