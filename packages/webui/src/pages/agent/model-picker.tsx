import { Bot, Check, ChevronDown, Eye, Search } from 'lucide-react'
import { useEffect, useMemo, useRef, useState } from 'react'
import { Z_INDEX } from '@/lib/z-index'
import { useModels, useSources } from '@/lib/hooks'
import type { Model, ModelSource } from '@/lib/types'
import { cn } from '@/lib/utils'

/**
 * 合并式模型选择器：胶囊触发器 + 浮层面板——按模型源分组、可搜索、
 * 视觉模型标注；选中即确定 源+模型 两项设置。仿 MultiSelect 的浮层模式。
 */
export function ModelPicker({
  sourceId,
  modelName,
  disabled,
  onSelect,
}: {
  sourceId?: string
  modelName?: string
  disabled?: boolean
  onSelect: (source: ModelSource, model: Model) => void
}) {
  const [open, setOpen] = useState(false)
  const [query, setQuery] = useState('')
  const [cursor, setCursor] = useState(0)
  const rootRef = useRef<HTMLDivElement>(null)
  const searchRef = useRef<HTMLInputElement>(null)
  const listRef = useRef<HTMLDivElement>(null)

  const { data: sources } = useSources()
  const { data: models } = useModels()

  const enabledSources = useMemo(
    () => new Map((sources ?? []).filter((source) => source.enabled).map((source) => [source.id, source])),
    [sources],
  )

  /** 分组视图：启用源 → 可调度的 LLM 模型，按源分组（源序）。 */
  const groups = useMemo(() => {
    const keyword = query.trim().toLowerCase()
    const result: { source: ModelSource; models: Model[] }[] = []
    for (const source of enabledSources.values()) {
      const items = (models ?? []).filter(
        (model) =>
          model.sourceId === source.id &&
          model.enabled &&
          (model.type === 'llm' || !model.type) &&
          (!keyword ||
            model.name.toLowerCase().includes(keyword) ||
            (model.sourceName ?? '').toLowerCase().includes(keyword)),
      )
      if (items.length > 0) {
        result.push({ source, models: items })
      }
    }
    return result
  }, [enabledSources, models, query])

  const flat = useMemo(() => groups.flatMap((group) => group.models.map((model) => ({ source: group.source, model }))), [groups])

  // 打开时聚焦搜索、光标复位到当前选中。
  useEffect(() => {
    if (!open) return
    setQuery('')
    const index = flat.findIndex(
      (entry) => entry.source.id === sourceId && entry.model.name === modelName,
    )
    setCursor(index >= 0 ? index : 0)
    window.setTimeout(() => searchRef.current?.focus(), 0)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open])

  // 点击外部关闭。
  useEffect(() => {
    if (!open) return
    const onPointerDown = (event: PointerEvent) => {
      if (rootRef.current && !rootRef.current.contains(event.target as Node)) {
        setOpen(false)
      }
    }
    document.addEventListener('pointerdown', onPointerDown)
    return () => document.removeEventListener('pointerdown', onPointerDown)
  }, [open])

  const pick = (index: number) => {
    const entry = flat[index]
    if (!entry) return
    onSelect(entry.source, entry.model)
    setOpen(false)
  }

  const onKeyDown = (event: React.KeyboardEvent) => {
    if (event.key === 'Escape') {
      setOpen(false)
      return
    }
    if (flat.length === 0) return
    if (event.key === 'ArrowDown') {
      event.preventDefault()
      setCursor((current) => (current + 1) % flat.length)
    } else if (event.key === 'ArrowUp') {
      event.preventDefault()
      setCursor((current) => (current - 1 + flat.length) % flat.length)
    } else if (event.key === 'Enter') {
      event.preventDefault()
      pick(cursor)
    }
  }

  // 光标行滚动进可视区。
  useEffect(() => {
    const active = listRef.current?.querySelector('[data-active="true"]')
    active?.scrollIntoView({ block: 'nearest' })
  }, [cursor, open, query])

  const selected = (models ?? []).find((model) => model.sourceId === sourceId && model.name === modelName)

  let flatIndex = -1
  return (
    <div ref={rootRef} className="relative" onKeyDown={onKeyDown}>
      <button
        type="button"
        disabled={disabled}
        aria-haspopup="listbox"
        aria-expanded={open}
        onClick={() => setOpen((value) => !value)}
        className={cn(
          'inline-flex h-7 max-w-[220px] items-center gap-1.5 rounded-md border border-input bg-card px-2.5 text-xs transition-colors',
          'hover:bg-wash disabled:cursor-not-allowed disabled:opacity-50',
          open && 'border-border bg-wash',
        )}
        title={selected ? `${selected.sourceName ?? sourceId} / ${modelName}` : '按模型源分组选择模型'}
      >
        <Bot className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
        <span className={cn('truncate', !modelName && 'text-muted-foreground')}>
          {modelName || '选择模型'}
        </span>
        {selected?.visionCapable ? <Eye className="h-3 w-3 shrink-0 text-jade" /> : null}
        <ChevronDown className="h-3.5 w-3.5 shrink-0 opacity-60" />
      </button>

      {open ? (
        <div
          role="dialog"
          aria-label="选择模型"
          className={cn(
            Z_INDEX.multiSelectPanel,
            'absolute bottom-[calc(100%+6px)] left-0 flex max-h-80 w-[min(320px,86vw)] flex-col overflow-hidden rounded-xl border border-border bg-popover shadow-lg',
          )}
        >
          <div className="mx-2 mt-2 flex items-center gap-2 rounded-full bg-[var(--well)] px-3 py-1.5">
            <Search className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
            <input
              ref={searchRef}
              value={query}
              onChange={(event) => {
                setQuery(event.target.value)
                setCursor(0)
              }}
              placeholder="搜索模型或源名…"
              className="w-full bg-transparent text-xs outline-none focus-visible:outline-none placeholder:text-muted-foreground"
            />
          </div>
          <div ref={listRef} role="listbox" aria-label="模型列表" className="min-h-0 flex-1 overflow-y-auto overscroll-contain p-1 pt-1.5">
            {flat.length === 0 ? (
              <p role="status" className="px-3 py-5 text-center text-xs text-muted-foreground">
                {(models ?? []).length === 0 ? '暂无模型：先在「模型源」页添加并拉取' : '没有匹配的模型'}
              </p>
            ) : (
              groups.map((group) => (
                <div key={group.source.id} className="mb-1">
                  <p className="flex items-baseline gap-1.5 px-2 pb-0.5 pt-1.5 text-2xs text-muted-foreground">
                    <span className="font-medium">{group.source.name || group.source.id}</span>
                    <span className="opacity-60">{group.source.platform}</span>
                  </p>
                  {group.models.map((model) => {
                    flatIndex += 1
                    const index = flatIndex
                    const active = index === cursor
                    const isSelected = model.sourceId === sourceId && model.name === modelName
                    return (
                      <button
                        key={`${model.sourceId}/${model.id}`}
                        type="button"
                        role="option"
                        aria-selected={isSelected}
                        data-active={active}
                        onClick={() => pick(index)}
                        onMouseEnter={() => setCursor(index)}
                        className={cn(
                          'flex w-full items-center gap-1.5 rounded-md px-2 py-1.5 text-left text-xs transition-colors',
                          active ? 'bg-wash' : 'hover:bg-wash/60',
                          isSelected && 'font-medium text-rose',
                        )}
                      >
                        <Check className={cn('h-3 w-3 shrink-0', isSelected ? 'opacity-100' : 'opacity-0')} />
                        <span className="min-w-0 flex-1 truncate">{model.name}</span>
                        {model.visionCapable ? (
                          <span className="inline-flex shrink-0 items-center gap-0.5 rounded-full border border-jade/30 bg-jade/10 px-1.5 py-px text-[10px] text-jade">
                            <Eye className="h-2.5 w-2.5" />视觉
                          </span>
                        ) : null}
                      </button>
                    )
                  })}
                </div>
              ))
            )}
          </div>
        </div>
      ) : null}
    </div>
  )
}
