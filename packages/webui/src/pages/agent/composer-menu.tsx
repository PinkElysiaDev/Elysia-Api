import { Check, ChevronDown, Gauge, Shield } from 'lucide-react'
import { type ReactNode, useEffect, useRef, useState } from 'react'
import type { AgentPermission, AgentSettings, AgentThinkingEffort } from '@/lib/agent/types'
import { cn } from '@/lib/utils'
import { Z_INDEX } from '@/lib/z-index'

/** composer 控制条的浮层菜单外壳：胶囊触发器 + 向上弹出面板（点击外部/Escape 关闭）。 */
export interface MenuShellProps {
  icon: ReactNode
  label: ReactNode
  title?: string
  disabled?: boolean
  panelLabel: string
  panelClassName?: string
  children: (close: () => void) => ReactNode
}

export function MenuShell({ icon, label, title, disabled, panelLabel, panelClassName, children }: MenuShellProps) {
  const [open, setOpen] = useState(false)
  const rootRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (!open) return
    const onPointerDown = (event: PointerEvent) => {
      if (!rootRef.current?.contains(event.target as Node)) setOpen(false)
    }
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') setOpen(false)
    }
    document.addEventListener('pointerdown', onPointerDown)
    document.addEventListener('keydown', onKeyDown)
    return () => {
      document.removeEventListener('pointerdown', onPointerDown)
      document.removeEventListener('keydown', onKeyDown)
    }
  }, [open])

  return (
    <div ref={rootRef} className="relative">
      <button
        type="button"
        disabled={disabled}
        aria-haspopup="menu"
        aria-expanded={open}
        onClick={() => setOpen((value) => !value)}
        title={title}
        className={cn(
          'inline-flex h-7 items-center gap-1.5 rounded-md border border-input bg-card px-2.5 text-2xs transition-colors',
          'hover:bg-wash disabled:cursor-not-allowed disabled:opacity-50',
          open && 'border-rose ring-[3px] ring-wash',
        )}
      >
        {icon}
        <span className="max-w-[130px] truncate">{label}</span>
        <ChevronDown className="h-3.5 w-3.5 shrink-0 opacity-60" />
      </button>

      {open ? (
        <div
          role="menu"
          aria-label={panelLabel}
          className={cn(
            Z_INDEX.multiSelectPanel,
            'absolute bottom-[calc(100%+6px)] left-0 w-[min(280px,86vw)] overflow-hidden rounded-xl border border-border bg-popover p-1 shadow-lg',
            panelClassName,
          )}
        >
          {children(() => setOpen(false))}
        </div>
      ) : null}
    </div>
  )
}

function MenuOption({
  selected,
  label,
  hint,
  onClick,
}: {
  selected: boolean
  label: string
  hint?: string
  onClick: () => void
}) {
  return (
    <button
      type="button"
      role="menuitemradio"
      aria-checked={selected}
      onClick={onClick}
      className="flex w-full items-start gap-2 rounded-lg px-2.5 py-1.5 text-left transition-colors hover:bg-wash"
    >
      <Check className={cn('mt-0.5 h-3.5 w-3.5 shrink-0 text-rose', !selected && 'opacity-0')} />
      <span className="min-w-0">
        <span className={cn('block text-xs leading-5', selected && 'font-medium text-rose')}>{label}</span>
        {hint ? <span className="block text-2xs leading-4 text-muted-foreground">{hint}</span> : null}
      </span>
    </button>
  )
}

/** ---- 权限控制：计划模式（并行开关）+ 三档审批频繁程度 ---- */

export type PermissionLevel = 'confirm' | 'auto' | 'full'

const PERMISSION_LEVELS: { value: PermissionLevel; label: string; hint: string; allowSave: AgentPermission; allowLiveTest: AgentPermission }[] = [
  { value: 'confirm', label: '变更前确认', hint: '每次写入与出站都先询问', allowSave: 'ask', allowLiveTest: 'ask' },
  { value: 'auto', label: '自动编辑', hint: '自动保存修改，真实出站仍需确认', allowSave: 'always', allowLiveTest: 'ask' },
  { value: 'full', label: '完全控制', hint: '全部自动执行，无需确认', allowSave: 'always', allowLiveTest: 'always' },
]

function levelFromSettings(settings: AgentSettings): PermissionLevel {
  if (settings.allowSave === 'always' && settings.allowLiveTest === 'always') return 'full'
  if (settings.allowSave === 'always') return 'auto'
  return 'confirm'
}

export function PermissionMenu({
  settings,
  disabled,
  onChange,
}: {
  settings: AgentSettings
  disabled?: boolean
  onChange: (patch: { settings: Partial<AgentSettings> }) => void
}) {
  const planMode = !!settings.planMode
  const level = levelFromSettings(settings)
  const levelLabel = PERMISSION_LEVELS.find((item) => item.value === level)?.label ?? '变更前确认'

  return (
    <MenuShell
      icon={<Shield className={cn('h-3.5 w-3.5 shrink-0', planMode ? 'text-amber' : 'text-muted-foreground')} />}
      label={planMode ? <span className="text-amber">计划模式</span> : levelLabel}
      title="权限控制：审批频繁程度与计划模式"
      panelLabel="权限控制"
      disabled={disabled}
    >
      {() => (
        <>
          <MenuOption
            selected={planMode}
            label="计划模式"
            hint="先形成方案，确认后才执行修改"
            onClick={() => onChange({ settings: { planMode: !planMode } })}
          />
          <div className="my-1 h-px bg-border/60" />
          {PERMISSION_LEVELS.map((item) => (
            <MenuOption
              key={item.value}
              selected={level === item.value}
              label={item.label}
              hint={item.hint}
              onClick={() => onChange({ settings: { allowSave: item.allowSave, allowLiveTest: item.allowLiveTest } })}
            />
          ))}
        </>
      )}
    </MenuShell>
  )
}

/** ---- 思考与推理强度二合一：关闭档 + 等级档 ---- */

const THINKING_EFFORTS: { value: AgentThinkingEffort; label: string }[] = [
  { value: '', label: '默认' },
  { value: 'low', label: '低' },
  { value: 'medium', label: '中' },
  { value: 'high', label: '高' },
  { value: 'max', label: '最高' },
  { value: 'adaptive', label: '自适应' },
]

function effortLabel(effort: AgentThinkingEffort | undefined): string {
  return THINKING_EFFORTS.find((item) => item.value === (effort ?? ''))?.label ?? '思考'
}

export function ThinkingMenu({
  settings,
  disabled,
  onChange,
}: {
  settings: AgentSettings
  disabled?: boolean
  onChange: (patch: { settings: Partial<AgentSettings> }) => void
}) {
  const enabled = settings.thinkingEnabled
  const effort = settings.thinkingEffort ?? ''

  return (
    <MenuShell
      icon={<Gauge className={cn('h-3.5 w-3.5 shrink-0', enabled ? 'text-amber' : 'text-muted-foreground')} />}
      label={enabled ? effortLabel(effort) : '思考关闭'}
      title="思考开关与推理强度"
      panelLabel="思考与推理强度"
      panelClassName="w-[min(220px,86vw)]"
      disabled={disabled}
    >
      {(close) => (
        <>
          <MenuOption
            selected={!enabled}
            label="关闭思考"
            onClick={() => {
              onChange({ settings: { thinkingEnabled: false } })
              close()
            }}
          />
          {THINKING_EFFORTS.map((item) => (
            <MenuOption
              key={item.value || 'default'}
              selected={enabled && effort === item.value}
              label={item.label}
              onClick={() => {
                onChange({ settings: { thinkingEnabled: true, thinkingEffort: item.value } })
                close()
              }}
            />
          ))}
        </>
      )}
    </MenuShell>
  )
}
