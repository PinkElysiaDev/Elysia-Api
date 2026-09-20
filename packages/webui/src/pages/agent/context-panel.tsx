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
import { Seg } from '@/components/ui/seg'
import { colorize } from '@/lib/json-highlight'
import {
  agentToolLabel,
  type AgentMessage,
  type AgentSession,
  type AgentToolResultContent,
} from '@/lib/agent/types'
import type { AgentLiveState } from '@/lib/agent/use-agent-stream'
import { cn } from '@/lib/utils'

/**
 * 多用途侧边栏：方案（update_plan 维护）/ 草稿（协议 JSON）/ 进展（工具执行
 * 时间线）。有更新时自动切换标签；用户手动选过则本 turn 不抢焦点。
 */

type PanelTab = 'plan' | 'draft' | 'progress'

const TAB_OPTIONS: { value: PanelTab; label: string }[] = [
  { value: 'plan', label: '方案' },
  { value: 'draft', label: '草稿' },
  { value: 'progress', label: '进展' },
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
  const [tab, setTab] = useState<PanelTab>('plan')
  const userPinned = useRef(false)
  const runningRef = useRef(false)

  // 轮次开始重置手动锁定。
  useEffect(() => {
    if (live.running) {
      userPinned.current = false
      runningRef.current = true
    } else if (runningRef.current) {
      runningRef.current = false
    }
  }, [live.running])

  const plan = session.plan ?? []
  const planDirty = live.toolCards.some((card) => card.name === 'update_plan')
  useEffect(() => {
    if (planDirty && !userPinned.current) setTab('plan')
  }, [planDirty])

  // 首次出现草稿自动切到草稿页。
  const hasDraft = session.draftConfig != null
  const hadDraft = useRef(hasDraft)
  useEffect(() => {
    if (hasDraft && !hadDraft.current && !userPinned.current) setTab('draft')
    hadDraft.current = hasDraft
  }, [hasDraft])

  return (
    <div className="flex h-full w-80 shrink-0 flex-col border-l border-border bg-card/40">
      <div className="flex items-center gap-2 border-b border-border px-3 py-2">
        <Seg<PanelTab>
          size="sm"
          aria-label="侧栏内容"
          options={TAB_OPTIONS}
          value={tab}
          onChange={(value) => {
            userPinned.current = true
            setTab(value)
          }}
        />
      </div>
      <div className="min-h-0 flex-1 overflow-y-auto">
        {tab === 'plan' ? <PlanView steps={plan} /> : null}
        {tab === 'draft' ? <DraftView session={session} /> : null}
        {tab === 'progress' ? <ProgressView messages={messages} live={live} /> : null}
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
    <div className="space-y-1 px-3 py-3">
      <p className="px-1 pb-1 text-2xs text-muted-foreground tnum">
        {done}/{steps.length} 已完成
      </p>
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
      <div className="flex items-center gap-2 px-3 py-2.5">
        {protocolId ? <Badge variant="outline" className="font-mono text-2xs">{protocolId}</Badge> : null}
      </div>
      <div className="min-h-0 flex-1 overflow-y-auto px-3 pb-3">
        <pre
          className="rounded-[7px] border border-border bg-code px-3 py-2.5 font-mono text-xs leading-[1.7]"
          dangerouslySetInnerHTML={{ __html: colorize(draftText) }}
        />
      </div>
      <div className="border-t border-border px-3 py-2.5">
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
        <p className="mt-2 text-2xs leading-relaxed text-muted-foreground">
          草稿经校验才会写入；保存为正式协议需经助手请求并获你批准。
        </p>
      </div>
    </div>
  )
}

/** 进展页：工具执行时间线（历史 + 现场聚合）。 */
function ProgressView({ messages, live }: { messages: AgentMessage[]; live: AgentLiveState }) {
  const history = messages
    .filter((message) => message.role === 'tool_result')
    .map((message) => message.content as AgentToolResultContent)
    .filter(Boolean)

  if (history.length === 0 && live.toolCards.length === 0) {
    return (
      <EmptyHint
        icon={<Wrench className="h-4 w-4" />}
        text="助手执行的工具（写草稿、查询、测试……）会在这里按时间线汇总。"
      />
    )
  }

  const liveEntries = live.toolCards.map((card) => ({
    name: card.name,
    ok: card.status !== 'failed',
    running: card.status === 'running',
    summary: card.summary,
  }))

  return (
    <div className="space-y-1.5 px-3 py-3">
      {[...liveEntries].reverse().map((entry, index) => (
        <TimelineRow
          key={`live-${index}`}
          name={agentToolLabel(entry.name)}
          ok={entry.ok}
          running={entry.running}
          summary={entry.summary}
        />
      ))}
      {[...history].reverse().map((entry, index) => (
        <TimelineRow
          key={`hist-${index}`}
          name={agentToolLabel(entry.name)}
          ok={entry.ok}
          summary={entry.summary}
          durationMs={entry.durationMs}
        />
      ))}
    </div>
  )
}

function TimelineRow({
  name,
  ok,
  running,
  summary,
  durationMs,
}: {
  name: string
  ok: boolean
  running?: boolean
  summary?: string
  durationMs?: number
}) {
  return (
    <div className="rounded-lg border border-border/60 bg-card px-2.5 py-2 text-2xs">
      <div className="flex items-center gap-1.5">
        {running ? (
          <Loader2 className="h-3 w-3 shrink-0 animate-spin text-jade" />
        ) : (
          <span className={cn('h-1.5 w-1.5 shrink-0 rounded-full', ok ? 'bg-jade' : 'bg-ember')} />
        )}
        <span className="min-w-0 flex-1 truncate font-medium">{name}</span>
        {durationMs ? <span className="tnum text-muted-foreground">{durationMs}ms</span> : null}
      </div>
      {summary ? <p className="mt-1 line-clamp-2 text-muted-foreground">{summary}</p> : null}
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
