import {
  CheckCircle2,
  ChevronDown,
  Circle,
  ExternalLink,
  FileCode2,
  History,
  ListChecks,
  Loader2,
  Play,
  Wrench,
  X,
} from 'lucide-react'
import { useEffect, useRef, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { colorize } from '@/lib/json-highlight'
import { toolArgsPreview } from '@/lib/agent/mask'
import {
  agentToolLabel,
  type AgentContextTab,
  type AgentMessage,
  type AgentSession,
  type AgentToolResultContent,
} from '@/lib/agent/types'
import type { AgentLiveState } from '@/lib/agent/use-agent-stream'
import { cn } from '@/lib/utils'

/**
 * 通用标签页侧栏：不预设分类——方案更新开「方案」页、草稿变化开「配置」页、
 * 工具活动可点开「动态」页；每页都是窗口内一个可关闭的标签，模型打开什么
 * 就展示什么。标签状态由页面持有（跨组件联动）。
 */

const TAB_META: Record<AgentContextTab, { label: string }> = {
  plan: { label: '方案' },
  draft: { label: '配置' },
  activity: { label: '动态' },
}

export interface ContextPanelProps {
  session: AgentSession
  messages: AgentMessage[]
  live: AgentLiveState
  tabs: AgentContextTab[]
  activeTab: AgentContextTab | null
  onTabSelect: (tab: AgentContextTab) => void
  onTabClose: (tab: AgentContextTab) => void
  onAutoOpen: (tab: AgentContextTab) => void
  /** 计划模式下确认执行当前方案（关闭计划模式并开始执行）。 */
  onConfirmPlan: () => void
  /** 把草稿回滚到最近一轮修改前的还原点。 */
  onRestoreDraft: () => void
}

export function ContextPanel({
  session,
  messages,
  live,
  tabs,
  activeTab,
  onTabSelect,
  onTabClose,
  onAutoOpen,
  onConfirmPlan,
  onRestoreDraft,
}: ContextPanelProps) {
  const pinned = useRef(false)
  const wasRunning = useRef(false)
  const activityAutoOpened = useRef(false)

  const plan = session.plan ?? []
  const planJSON = JSON.stringify(plan)
  const draftJSON = session.draftConfig == null ? '' : JSON.stringify(session.draftConfig)
  const lastPlan = useRef(planJSON)
  const lastDraft = useRef(draftJSON)

  // 新 turn 开始：解除手动固定，恢复自动跟随；动态页自动开闸重置。
  useEffect(() => {
    if (live.running && !wasRunning.current) {
      pinned.current = false
      activityAutoOpened.current = false
    }
    wasRunning.current = live.running
  }, [live.running])

  // 方案变化 → 打开并激活方案页。
  useEffect(() => {
    if (planJSON !== lastPlan.current) {
      lastPlan.current = planJSON
      if (!pinned.current) onAutoOpen('plan')
    }
  }, [planJSON, onAutoOpen])

  // 草稿变化 → 打开并激活配置页。
  useEffect(() => {
    if (draftJSON !== lastDraft.current) {
      lastDraft.current = draftJSON
      if (!pinned.current) onAutoOpen('draft')
    }
  }, [draftJSON, onAutoOpen])

  // 工具开始执行且当前没有任何激活标签 → 每轮至多自动打开一次动态页。
  useEffect(() => {
    const running = live.toolCards.some((card) => card.name !== 'update_plan')
    if (running && !activityAutoOpened.current) {
      activityAutoOpened.current = true
      if (!pinned.current && activeTab == null) onAutoOpen('activity')
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [live.toolCards])

  const doneCount = plan.filter((step) => step.status === 'done').length

  return (
    <div className="flex h-full w-80 shrink-0 flex-col pl-4">
      <div role="tablist" aria-label="侧栏标签页" className="flex items-center gap-3 px-1 pb-2 pt-1">
        {tabs.map((tab) => (
          <div key={tab} className="group flex items-center">
            <button
              type="button"
              role="tab"
              aria-selected={activeTab === tab}
              onClick={() => {
                pinned.current = true
                onTabSelect(tab)
              }}
              className={cn(
                'relative pb-1.5 text-xs transition-colors',
                activeTab === tab
                  ? 'font-medium text-rose after:absolute after:inset-x-0 after:bottom-0 after:h-[2px] after:rounded-full after:bg-rose'
                  : 'text-muted-foreground hover:text-foreground',
              )}
            >
              {TAB_META[tab].label}
              {tab === 'plan' && plan.length > 0 ? (
                <span className="tnum ml-1 text-2xs text-muted-foreground">
                  {doneCount}/{plan.length}
                </span>
              ) : null}
            </button>
            <button
              type="button"
              className="ml-0.5 rounded p-0.5 text-muted-foreground/60 opacity-0 transition-opacity hover:text-foreground focus-visible:opacity-100 group-hover:opacity-100"
              title={`关闭「${TAB_META[tab].label}」`}
              onClick={() => onTabClose(tab)}
            >
              <X className="h-3 w-3" />
            </button>
          </div>
        ))}
      </div>
      <div className="min-h-0 flex-1 overflow-y-auto pb-2">
        {tabs.length === 0 ? (
          <p className="px-2 py-10 text-center text-2xs text-muted-foreground">
            方案、配置详情与工具动态会以标签页在这里打开
          </p>
        ) : activeTab === 'plan' ? (
          <PlanView steps={plan} planMode={!!session.settings.planMode} busy={live.running} onConfirm={onConfirmPlan} />
        ) : activeTab === 'draft' ? (
          <DraftView session={session} busy={live.running} onRestore={onRestoreDraft} />
        ) : activeTab === 'activity' ? (
          <ActivityView messages={messages} live={live} />
        ) : null}
      </div>
    </div>
  )
}

/** 方案页：update_plan 维护的步骤清单；计划模式下提供「确认执行」。 */
function PlanView({
  steps,
  planMode,
  busy,
  onConfirm,
}: {
  steps: { title: string; status: string }[]
  planMode: boolean
  busy: boolean
  onConfirm: () => void
}) {
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
    <div className="flex h-full min-h-0 flex-col">
      <div className="min-h-0 flex-1 space-y-1 overflow-y-auto px-1 py-1">
        <p className="tnum px-1 pb-1 text-2xs text-muted-foreground">{done}/{steps.length} 已完成</p>
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
      {planMode ? (
        <div className="space-y-1.5 border-t border-border/50 px-1 pt-2.5">
          <Button size="sm" className="w-full gap-1.5 text-xs" disabled={busy} onClick={onConfirm}>
            <Play className="h-3.5 w-3.5" /> 确认执行方案
          </Button>
          <p className="text-2xs leading-relaxed text-muted-foreground">
            计划模式已开启：可在对话中对方案提出修改意见，确认后才会执行修改。
          </p>
        </div>
      ) : null}
    </div>
  )
}

/** 配置页：协议草稿的结构化详情（请求/响应/流式映射 + 完整 JSON）+ 跳转设计器。 */
const DRAFT_SECTIONS: { key: string; label: string }[] = [
  { key: 'request', label: '请求构造（网关 → 上游）' },
  { key: 'response', label: '响应解析（上游 → 网关）' },
  { key: 'stream', label: '流式解析（SSE 帧）' },
]

function DraftView({ session, busy, onRestore }: { session: AgentSession; busy: boolean; onRestore: () => void }) {
  const navigate = useNavigate()
  const draft = session.draftConfig
  const draftText = draft == null ? '' : typeof draft === 'string' ? draft : JSON.stringify(draft, null, 2)
  const draftObject = draft && typeof draft === 'object' && !Array.isArray(draft)
    ? (draft as Record<string, unknown>)
    : null
  const protocolId = draftObject ? String(draftObject.id ?? '') : ''
  const protocolName = draftObject ? String(draftObject.name ?? '') : ''
  const restoreText = session.draftRestore == null ? '' : JSON.stringify(session.draftRestore)
  const canRestore =
    restoreText !== '' && draftText !== '' && restoreText !== JSON.stringify(draft ?? null) && !busy

  if (!draftText) {
    return (
      <EmptyHint
        icon={<FileCode2 className="h-4 w-4" />}
        text="助手提交协议草稿后，可在这里查看配置详情与映射关系。"
      />
    )
  }

  const sections = DRAFT_SECTIONS.filter((section) => draftObject && draftObject[section.key] != null)
  const basicEntries = draftObject
    ? Object.entries(draftObject).filter(([key]) => !DRAFT_SECTIONS.some((section) => section.key === key))
    : []

  return (
    <div className="flex h-full min-h-0 flex-col">
      <div className="flex min-w-0 items-center gap-2 px-1 py-1.5">
        {protocolId ? <Badge variant="outline" className="shrink-0 font-mono text-2xs">{protocolId}</Badge> : null}
        {protocolName ? <span className="min-w-0 truncate text-xs text-muted-foreground">{protocolName}</span> : null}
      </div>
      <div className="min-h-0 flex-1 space-y-2 overflow-y-auto px-1 pb-2">
        {basicEntries.length > 0 ? (
          <SectionBlock title="基本信息" defaultOpen>
            <JsonBlock value={Object.fromEntries(basicEntries)} maxHeight="max-h-44" />
          </SectionBlock>
        ) : null}
        {sections.map((section) => (
          <SectionBlock key={section.key} title={section.label} defaultOpen>
            <JsonBlock value={(draftObject as Record<string, unknown>)[section.key]} />
          </SectionBlock>
        ))}
        <SectionBlock title="完整 JSON">
          <JsonBlock value={draftText} />
        </SectionBlock>
      </div>
      <div className="space-y-1.5 px-1 pt-2">
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
        {canRestore ? (
          <Button
            size="sm"
            variant="outline"
            className="w-full gap-1.5 text-xs"
            title="把配置回滚到最近一轮对话修改前的状态"
            onClick={onRestore}
          >
            <History className="h-3.5 w-3.5" /> 还原到上一轮修改前
          </Button>
        ) : null}
      </div>
    </div>
  )
}

function SectionBlock({
  title,
  defaultOpen = false,
  children,
}: {
  title: string
  defaultOpen?: boolean
  children: React.ReactNode
}) {
  const [open, setOpen] = useState(defaultOpen)
  return (
    <div className="rounded-md border border-border bg-muted/40 text-xs">
      <button
        type="button"
        className="flex w-full items-center gap-1.5 px-2.5 py-1.5 text-left text-muted-foreground hover:text-foreground"
        onClick={() => setOpen((value) => !value)}
      >
        <span className="flex-1 truncate">{title}</span>
        <ChevronDown className={cn('h-3.5 w-3.5 transition-transform', open && 'rotate-180')} />
      </button>
      {open && <div className="border-t border-border/60 px-2.5 py-2">{children}</div>}
    </div>
  )
}

function JsonBlock({ value, maxHeight = 'max-h-72' }: { value: unknown; maxHeight?: string }) {
  const text = typeof value === 'string' ? value : JSON.stringify(value, null, 2) ?? ''
  return (
    <pre
      className={cn('overflow-auto whitespace-pre rounded-[7px] border border-border bg-code px-3 py-2.5 font-mono text-2xs leading-[1.7]', maxHeight)}
      dangerouslySetInnerHTML={{ __html: colorize(text) }}
    />
  )
}

/** 动态页：运行中卡片 + 可选中的执行详情（参数/结果）+ 近期执行列表。 */
function ActivityView({ messages, live }: { messages: AgentMessage[]; live: AgentLiveState }) {
  const [selectedSeq, setSelectedSeq] = useState<number | null>(null)
  const history = messages
    .filter((message) => message.role === 'tool_result')
    .map((message) => ({ seq: message.seq, content: message.content as AgentToolResultContent }))
    .filter((entry) => entry.content)

  const runningCard = live.toolCards.find((card) => card.status === 'running')
  const selected = history.find((entry) => entry.seq === selectedSeq) ?? history[history.length - 1]

  if (!selected && !runningCard && live.toolCards.length === 0) {
    return (
      <EmptyHint
        icon={<Wrench className="h-4 w-4" />}
        text="点击对话中的工具执行行，可在这里查看参数与结果详情。"
      />
    )
  }

  const recent = [...history].reverse().slice(0, 12)

  return (
    <div className="space-y-3 px-1 py-1">
      {runningCard ? (
        <div className="rounded-lg bg-wash px-3 py-2.5">
          <div className="flex items-center gap-1.5 text-xs font-medium">
            <Loader2 className="h-3.5 w-3.5 animate-spin text-jade" />
            {agentToolLabel(runningCard.name)}
            <span className="ml-auto text-2xs font-normal text-muted-foreground">执行中…</span>
          </div>
        </div>
      ) : null}

      {selected ? (
        <div className="space-y-2">
          <div className="flex items-center gap-1.5 text-xs">
            <Wrench className="h-3.5 w-3.5 text-muted-foreground/60" />
            <span className="font-medium">{selected.content.name}</span>
            <span className={cn('text-2xs', selected.content.ok ? 'text-jade' : 'text-ember')}>
              {selected.content.ok ? '成功' : '失败'}
            </span>
            {selected.content.durationMs ? (
              <span className="tnum text-2xs text-muted-foreground">{selected.content.durationMs}ms</span>
            ) : null}
          </div>
          {selected.content.summary ? (
            <p className="text-2xs leading-relaxed text-muted-foreground">{selected.content.summary}</p>
          ) : null}
          {selected.content.input != null && toolArgsPreview(selected.content.input) ? (
            <div>
              <p className="mb-1 text-2xs font-medium text-muted-foreground">参数</p>
              <pre className="max-h-32 overflow-auto whitespace-pre-wrap rounded-[7px] border border-border bg-code px-2.5 py-2 font-mono text-2xs leading-relaxed">
                {toolArgsPreview(selected.content.input)}
              </pre>
            </div>
          ) : null}
          {selected.content.data != null ? (
            <div>
              <p className="mb-1 text-2xs font-medium text-muted-foreground">结果</p>
              <JsonBlock value={selected.content.data} maxHeight="max-h-64" />
            </div>
          ) : null}
        </div>
      ) : null}

      {recent.length > 0 ? (
        <div className="space-y-1 border-t border-border/50 pt-2">
          <p className="px-0.5 text-2xs font-medium text-muted-foreground">近期执行</p>
          {recent.map((entry) => (
            <button
              key={entry.seq}
              type="button"
              className={cn(
                'flex w-full items-center gap-1.5 rounded-md px-0.5 py-1 text-left text-2xs transition-colors hover:text-foreground',
                selected?.seq === entry.seq ? 'text-rose' : 'text-muted-foreground',
              )}
              onClick={() => setSelectedSeq(entry.seq)}
            >
              <span className={cn('h-1.5 w-1.5 shrink-0 rounded-full', entry.content.ok ? 'bg-jade' : 'bg-ember')} />
              <span className="min-w-0 flex-1 truncate">{agentToolLabel(entry.content.name)}</span>
              {entry.content.durationMs ? (
                <span className="tnum shrink-0 text-muted-foreground">{entry.content.durationMs}ms</span>
              ) : null}
            </button>
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
