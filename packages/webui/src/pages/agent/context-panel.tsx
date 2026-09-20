import {
  CheckCircle2,
  Circle,
  ExternalLink,
  FileCode2,
  ListChecks,
  Loader2,
  Wrench,
} from 'lucide-react'
import { useEffect, useRef, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { colorize } from '@/lib/json-highlight'
import { toolArgsPreview } from '@/lib/agent/mask'
import {
  agentToolLabel,
  type AgentMessage,
  type AgentSession,
  type AgentToolResultContent,
} from '@/lib/agent/types'
import type { AgentLiveState } from '@/lib/agent/use-agent-stream'
import { cn } from '@/lib/utils'

/**
 * 多用途侧栏：跟随模型当前打开的内容——方案更新跟方案、草稿变化跟草稿、
 * 其他工具活动跟动态；文本页签允许快速切换（点击后本 turn 固定，新 turn
 * 恢复自动跟随）。
 */

type PanelTab = 'activity' | 'plan' | 'draft'

const TABS: { value: PanelTab; label: string }[] = [
  { value: 'activity', label: '动态' },
  { value: 'plan', label: '方案' },
  { value: 'draft', label: '草稿' },
]

export function ContextPanel({
  session,
  messages,
  live,
}: {
  session: AgentSession
  messages: AgentMessage[]
  live: AgentLiveState
}) {
  const [tab, setTab] = useState<PanelTab>('activity')
  const pinned = useRef(false)
  const wasRunning = useRef(false)

  const plan = session.plan ?? []
  const planJSON = JSON.stringify(plan)
  const draftJSON = session.draftConfig == null ? '' : JSON.stringify(session.draftConfig)
  const lastPlan = useRef(planJSON)
  const lastDraft = useRef(draftJSON)
  const lastToolCount = useRef(0)

  const follow = (next: PanelTab) => {
    if (!pinned.current) setTab(next)
  }

  // 新 turn 开始解除固定。
  useEffect(() => {
    if (live.running && !wasRunning.current) {
      pinned.current = false
    }
    wasRunning.current = live.running
  }, [live.running])

  // 方案变化 → 跟方案。
  useEffect(() => {
    if (planJSON !== lastPlan.current) {
      lastPlan.current = planJSON
      follow('plan')
    }
  }, [planJSON])

  // 草稿变化 → 跟草稿。
  useEffect(() => {
    if (draftJSON !== lastDraft.current) {
      lastDraft.current = draftJSON
      follow('draft')
    }
  }, [draftJSON])

  // 其他工具活动（现场卡片数量变化）→ 跟动态（update_plan 除外，归方案）。
  useEffect(() => {
    const otherTools = live.toolCards.filter((card) => card.name !== 'update_plan').length
    if (otherTools !== lastToolCount.current) {
      lastToolCount.current = otherTools
      if (otherTools > 0) follow('activity')
    }
  }, [live.toolCards])

  return (
    <div className="flex h-full w-80 shrink-0 flex-col pl-4">
      <div role="tablist" aria-label="侧栏内容" className="flex items-center gap-4 px-1 pb-2 pt-1">
        {TABS.map((item) => (
          <button
            key={item.value}
            type="button"
            role="tab"
            aria-selected={tab === item.value}
            onClick={() => {
              pinned.current = true
              setTab(item.value)
            }}
            className={cn(
              'relative pb-1.5 text-xs transition-colors',
              tab === item.value
                ? 'font-medium text-rose after:absolute after:inset-x-0 after:bottom-0 after:h-[2px] after:rounded-full after:bg-rose'
                : 'text-muted-foreground hover:text-foreground',
            )}
          >
            {item.label}
            {item.value === 'plan' && plan.length > 0 ? (
              <span className="tnum ml-1 text-2xs text-muted-foreground">
                {plan.filter((step) => step.status === 'done').length}/{plan.length}
              </span>
            ) : null}
          </button>
        ))}
      </div>
      <div className="min-h-0 flex-1 overflow-y-auto pb-2">
        {tab === 'plan' ? <PlanView steps={plan} /> : null}
        {tab === 'draft' ? <DraftView session={session} /> : null}
        {tab === 'activity' ? <ActivityView messages={messages} live={live} /> : null}
      </div>
    </div>
  )
}

/** 方案页：update_plan 维护的步骤清单。 */
function PlanView({ steps }: { steps: { title: string; status: string }[] }) {
  if (steps.length === 0) {
    return (
      <EmptyHint
        icon={<ListChecks className="h-4 w-4" />}
        text="多步任务开始时，助手会在这里给出方案步骤并随进度更新。"
      />
    )
  }
  const done = steps.filter((step) => step.status === 'done').length
  return (
    <div className="space-y-1 px-1 py-1">
      <p className="px-1 pb-1 tnum text-2xs text-muted-foreground">{done}/{steps.length} 已完成</p>
      {steps.map((step, index) => (
        <div
          key={index}
          className={cn(
            'flex items-start gap-2 rounded-lg px-2.5 py-2 text-xs',
            step.status === 'in_progress' && 'bg-wash',
            step.status === 'done' && 'text-muted-foreground',
          )}
        >
          {step.status === 'done' ? (
            <CheckCircle2 className="mt-0.5 h-3.5 w-3.5 shrink-0 text-jade" />
          ) : step.status === 'in_progress' ? (
            <Loader2 className="mt-0.5 h-3.5 w-3.5 shrink-0 animate-spin text-jade" />
          ) : (
            <Circle className="mt-0.5 h-3.5 w-3.5 shrink-0 text-muted-foreground/50" />
          )}
          <span className={cn('leading-relaxed', step.status === 'done' && 'line-through decoration-border')}>{step.title}</span>
        </div>
      ))}
    </div>
  )
}

/** 草稿页：协议 JSON + 跳转设计器。 */
function DraftView({ session }: { session: AgentSession }) {
  const navigate = useNavigate()
  const draft = session.draftConfig
  const draftText = draft == null ? '' : typeof draft === 'string' ? draft : JSON.stringify(draft, null, 2)
  const protocolId =
    draft && typeof draft === 'object' && 'id' in (draft as Record<string, unknown>)
      ? String((draft as Record<string, unknown>).id)
      : ''

  if (!draftText) {
    return (
      <EmptyHint
        icon={<FileCode2 className="h-4 w-4" />}
        text="助手调用 update_protocol_draft 后，最新协议草稿会实时显示在这里。"
      />
    )
  }
  return (
    <div className="flex h-full min-h-0 flex-col">
      <div className="flex items-center gap-2 px-1 py-1.5">
        {protocolId ? <Badge variant="outline" className="font-mono text-2xs">{protocolId}</Badge> : null}
      </div>
      <div className="min-h-0 flex-1 overflow-y-auto px-1 pb-2">
        <pre
          className="rounded-[7px] border border-border bg-code px-3 py-2.5 font-mono text-xs leading-[1.7]"
          dangerouslySetInnerHTML={{ __html: colorize(draftText) }}
        />
      </div>
      <div className="px-1 pt-2">
        <Button
          size="sm"
          variant="outline"
          className="w-full gap-1.5 text-xs"
          onClick={() =>
            navigate('/protocols', {
              state: { draft: typeof draft === 'string' ? safeParse(draft) : draft },
            })
          }
        >
          <ExternalLink className="h-3.5 w-3.5" /> 在协议设计器中打开
        </Button>
        <p className="mt-1.5 text-2xs leading-relaxed text-muted-foreground">
          草稿经校验才会写入；保存为正式协议需经助手请求并获你批准。
        </p>
      </div>
    </div>
  )
}

/** 动态页：最新工具执行详情 + 近期执行列表。 */
function ActivityView({ messages, live }: { messages: AgentMessage[]; live: AgentLiveState }) {
  const history = messages
    .filter((message) => message.role === 'tool_result')
    .map((message) => message.content as AgentToolResultContent)
    .filter(Boolean)

  const latest = history.length > 0 ? history[history.length - 1] : undefined
  const runningCard = live.toolCards.find((card) => card.status === 'running')

  if (!latest && !runningCard && live.toolCards.length === 0) {
    return (
      <EmptyHint
        icon={<Wrench className="h-4 w-4" />}
        text="助手执行的工具（写草稿、查询、测试……）会在这里展示最新动态。"
      />
    )
  }

  return (
    <div className="space-y-3 px-1 py-1">
      {/* 正在执行 / 最新完成 */}
      {runningCard ? (
        <div className="rounded-lg bg-wash px-3 py-2.5">
          <div className="flex items-center gap-1.5 text-xs font-medium">
            <Loader2 className="h-3.5 w-3.5 animate-spin text-jade" />
            {agentToolLabel(runningCard.name)}
            <span className="ml-auto text-2xs font-normal text-muted-foreground">执行中…</span>
          </div>
        </div>
      ) : latest ? (
        <div className="space-y-2">
          <div className="flex items-center gap-1.5 text-xs">
            <Wrench className="h-3.5 w-3.5 text-muted-foreground/60" />
            <span className="font-medium">{latest.name}</span>
            <span className={cn('text-2xs', latest.ok ? 'text-jade' : 'text-ember')}>{latest.ok ? '成功' : '失败'}</span>
            {latest.durationMs ? <span className="tnum text-2xs text-muted-foreground">{latest.durationMs}ms</span> : null}
          </div>
          {latest.summary ? <p className="text-2xs leading-relaxed text-muted-foreground">{latest.summary}</p> : null}
          {latest.input != null && toolArgsPreview(latest.input) ? (
            <div>
              <p className="mb-1 text-2xs font-medium text-muted-foreground">参数</p>
              <pre className="max-h-32 overflow-auto whitespace-pre-wrap rounded-[7px] border border-border bg-code px-2.5 py-2 font-mono text-2xs leading-relaxed">
                {toolArgsPreview(latest.input)}
              </pre>
            </div>
          ) : null}
          {latest.data != null ? (
            <div>
              <p className="mb-1 text-2xs font-medium text-muted-foreground">结果</p>
              <pre
                className="max-h-64 overflow-auto whitespace-pre rounded-[7px] border border-border bg-code px-2.5 py-2 font-mono text-2xs leading-[1.7]"
                dangerouslySetInnerHTML={{
                  __html: colorize(typeof latest.data === 'string' ? latest.data : JSON.stringify(latest.data, null, 2) ?? ''),
                }}
              />
            </div>
          ) : null}
        </div>
      ) : null}

      {/* 近期执行（新→旧，不含最新一条） */}
      {history.length > 1 ? (
        <div className="space-y-1 border-t border-border/50 pt-2">
          <p className="px-0.5 text-2xs font-medium text-muted-foreground">近期执行</p>
          {[...history.slice(0, -1)].reverse().slice(0, 12).map((entry, index) => (
            <div key={index} className="flex items-center gap-1.5 px-0.5 py-1 text-2xs">
              <span className={cn('h-1.5 w-1.5 shrink-0 rounded-full', entry.ok ? 'bg-jade' : 'bg-ember')} />
              <span className="min-w-0 flex-1 truncate">{agentToolLabel(entry.name)}</span>
              {entry.durationMs ? <span className="tnum shrink-0 text-muted-foreground">{entry.durationMs}ms</span> : null}
            </div>
          ))}
        </div>
      ) : null}
    </div>
  )
}

function EmptyHint({ icon, text }: { icon: React.ReactNode; text: string }) {
  return (
    <div className="flex flex-col items-center gap-2 px-6 py-12 text-center text-2xs text-muted-foreground">
      <span className="flex h-9 w-9 items-center justify-center rounded-xl bg-primary/10 text-primary">{icon}</span>
      <p className="leading-relaxed">{text}</p>
    </div>
  )
}

function safeParse(text: string): unknown {
  try {
    return JSON.parse(text)
  } catch {
    return text
  }
}
