import { Zap, type LucideIcon } from 'lucide-react'
import type { Platform, GroupStrategy } from '@/lib/types'
import { protocolLabel } from '@/lib/protocol'
import { cn, isSuccessStatus } from '@/lib/utils'

/* ---------- 状态点 ---------- */

export function Dot({ state, className }: { state: 'ok' | 'err' | 'off'; className?: string }) {
  return <span aria-hidden className={cn('dot', `dot-${state}`, className)} />
}

/* ---------- 状态码胶囊 ---------- */

export function CodePill({ code, className }: { code: number; className?: string }) {
  if (code === 0) return <span className={cn('font-mono text-xs text-muted-foreground', className)}>—</span>
  const isSuccess = isSuccessStatus(code)
  return (
    <span
      className={cn(
        'tnum inline-flex items-center rounded-[5px] border px-[7px] py-0.5 font-mono text-xs font-medium',
        isSuccess
          ? 'border-[color-mix(in_srgb,var(--jade)_26%,transparent)] bg-[color-mix(in_srgb,var(--jade)_9%,transparent)] text-jade'
          : 'border-[color-mix(in_srgb,var(--ember)_28%,transparent)] bg-[color-mix(in_srgb,var(--ember)_9%,transparent)] text-ember',
        className,
      )}
    >
      {code}
    </span>
  )
}

export function PlatformBadge({ platform }: { platform: Platform | string }) {
  return (
    <span className="inline-flex whitespace-nowrap font-mono text-2xs text-muted-foreground">
      {protocolLabel(platform, 'short')}
    </span>
  )
}

/* ---------- 能力 / 策略 chip ---------- */

export function CapChip({
  icon: Icon,
  children,
  className,
  title,
}: {
  icon?: LucideIcon
  children: React.ReactNode
  className?: string
  title?: string
}) {
  return (
    <span
      title={title}
      className={cn(
        'inline-flex items-center gap-1 rounded border border-border px-1.5 py-px text-2xs text-muted-foreground',
        className,
      )}
    >
      {Icon && <Icon className="h-[11px] w-[11px]" aria-hidden />}
      {children}
    </span>
  )
}

/* ---------- 流式图标态 ---------- */

export function StreamIcon({
  streaming,
  className,
}: {
  streaming?: boolean
  className?: string
}) {
  return (
    <span
      className={cn(
        'inline-flex items-center gap-1 text-2xs',
        streaming ? 'text-jade' : 'text-muted-foreground',
        className,
      )}
      title={streaming ? '流式' : '非流式'}
    >
      <Zap className="h-[11px] w-[11px]" aria-hidden strokeWidth={2} />
    </span>
  )
}

const STRATEGY_LABEL: Record<GroupStrategy, string> = {
  'round-robin': '轮询',
  sequential: '顺序',
  random: '随机',
}

export function StrategyBadge({ strategy }: { strategy: GroupStrategy | string }) {
  return <CapChip>{STRATEGY_LABEL[strategy as GroupStrategy] ?? strategy}</CapChip>
}
