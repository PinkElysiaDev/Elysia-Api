import { ArrowLeft, Eraser, PanelRightClose, PanelRightOpen, Sparkles } from 'lucide-react'
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { TonePill } from '@/components/badges'
import { Button } from '@/components/ui/button'
import { useConfirm } from '@/components/ui/confirm-dialog'
import { useToast } from '@/components/ui/use-toast'
import useSWR from 'swr'
import {
  clearAgentMessages,
  createAgentSession,
  deleteAgentSession,
  getAgentSession,
  listAgentSessions,
  restoreAgentDraft,
  updateAgentSession,
} from '@/lib/agent/api'
import {
  AGENT_CONTEXT_TAB_ORDER,
  type AgentContextTab,
  type AgentDocument,
  type AgentMessage,
  type AgentSession,
  type AgentSettings,
  type AgentStreamEvent,
} from '@/lib/agent/types'
import { useAgentStream } from '@/lib/agent/use-agent-stream'
import { cn } from '@/lib/utils'
import { ChatPanel } from './chat-panel'
import { ContextPanel } from './context-panel'
import { SessionOverview } from './session-overview'
import { TurnRail } from './turn-rail'

/** 侧栏宽度记忆键与范围（拖拽钳制，超范围回退默认 320）。 */
const PANEL_WIDTH_KEY = 'agent:panel-width'
const PANEL_WIDTH_MIN = 260
const PANEL_WIDTH_MAX = 560
const PANEL_WIDTH_DEFAULT = 320

/**
 * AI 助手页：总览（会话卡片网格）⇄ 工作区（轮数条 | 聊天 | 标签页侧栏）。
 * 点击卡片或新建任务以过渡动画进入工作区；返回总览不中断进行中的轮次。
 * 侧栏宽度可拖拽调整并记忆（localStorage）。
 */
export function AgentPage() {
  const { toast } = useToast()
  const { confirm, dialog: confirmDialog } = useConfirm()
  const navigate = useNavigate()
  const [searchParams] = useSearchParams()
  const [view, setView] = useState<'list' | 'chat'>('list')
  const [activeId, setActiveId] = useState<string | undefined>()
  const [session, setSession] = useState<AgentSession | undefined>()
  const [messages, setMessages] = useState<AgentMessage[]>([])
  const [panelOpen, setPanelOpen] = useState(true)
  const [bootstrapped, setBootstrapped] = useState(false)
  const [tabs, setTabs] = useState<AgentContextTab[]>([])
  const [activeTab, setActiveTab] = useState<AgentContextTab | null>(null)
  const [activeTurnSeq, setActiveTurnSeq] = useState<number | null>(null)
  const [jumpTarget, setJumpTarget] = useState<{ seq: number; nonce: number } | null>(null)
  const [panelW, setPanelW] = useState<number>(() => {
    const saved = Number(window.localStorage.getItem(PANEL_WIDTH_KEY))
    return Number.isFinite(saved) && saved >= PANEL_WIDTH_MIN && saved <= PANEL_WIDTH_MAX
      ? saved
      : PANEL_WIDTH_DEFAULT
  })
  const [panelDragging, setPanelDragging] = useState(false)
  const panelDragRef = useRef<{ startX: number; startW: number; latest: number } | null>(null)

  const onPanelHandleDown = (event: React.PointerEvent<HTMLDivElement>) => {
    if (!panelOpen) return
    panelDragRef.current = { startX: event.clientX, startW: panelW, latest: panelW }
    setPanelDragging(true)
    event.currentTarget.setPointerCapture(event.pointerId)
  }
  const onPanelHandleMove = (event: React.PointerEvent<HTMLDivElement>) => {
    const drag = panelDragRef.current
    if (!drag) return
    const next = Math.min(PANEL_WIDTH_MAX, Math.max(PANEL_WIDTH_MIN, drag.startW + (drag.startX - event.clientX)))
    drag.latest = next
    setPanelW(next)
  }
  const onPanelHandleUp = () => {
    const drag = panelDragRef.current
    if (!drag) return
    panelDragRef.current = null
    setPanelDragging(false)
    // 从 ref 取最新宽度：pointerup 可能先于最后一次 move 的 state 提交。
    window.localStorage.setItem(PANEL_WIDTH_KEY, String(Math.round(drag.latest)))
  }

  const { data: sessions, mutate: mutateSessions } = useSWRSessionList()

  const refreshSession = useCallback(
    async (id: string) => {
      try {
        const detail = await getAgentSession(id)
        setSession(detail.session)
        setMessages(detail.messages ?? [])
      } catch {
        /* 会话可能已删除 */
      }
    },
    [],
  )

  const { live, send, approve, stop } = useAgentStream(activeId, {
    onMessage: (event: AgentStreamEvent) => {
      if (event.message) {
        setMessages((current) => [...current, event.message as AgentMessage])
        if (event.message.role === 'user') {
          void mutateSessions()
        }
      }
    },
    onSessionDirty: () => {
      if (activeId) void refreshSession(activeId)
    },
  })

  /** 入口跳转：?mode=create | ?mode=edit&protocol=<id> 自动建会话并进入工作区。 */
  useEffect(() => {
    if (bootstrapped) return
    setBootstrapped(true)
    const mode = searchParams.get('mode')
    if (mode === 'create' || mode === 'edit') {
      const protocolId = searchParams.get('protocol') ?? ''
      if (mode === 'edit' && !protocolId) return
      void createSession({ mode, protocolId: protocolId || undefined })
      navigate('/agent', { replace: true })
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [bootstrapped])

  /** 会话列表轻轮询：运行中状态可感知（断连后回来能看到轮次结束）。 */
  useEffect(() => {
    if (!live.running) return
    const timer = window.setInterval(() => void mutateSessions(), 3000)
    return () => window.clearInterval(timer)
  }, [live.running, mutateSessions])

  /** 切换会话：标签页与轮次定位状态归零。 */
  useEffect(() => {
    setTabs([])
    setActiveTab(null)
    setActiveTurnSeq(null)
    setJumpTarget(null)
  }, [activeId])

  /** 打开（必要时追加）并激活一个侧栏标签；reveal 为 true 时同时展开侧栏。 */
  const openContextTab = useCallback((tab: AgentContextTab, reveal = true) => {
    setTabs((current) =>
      current.includes(tab)
        ? current
        : [...current, tab].sort((a, b) => AGENT_CONTEXT_TAB_ORDER.indexOf(a) - AGENT_CONTEXT_TAB_ORDER.indexOf(b)),
    )
    setActiveTab(tab)
    if (reveal) setPanelOpen(true)
  }, [])

  const closeContextTab = useCallback(
    (tab: AgentContextTab) => {
      const next = tabs.filter((item) => item !== tab)
      setTabs(next)
      setActiveTab((active) => (active === tab ? next[next.length - 1] ?? null : active))
    },
    [tabs],
  )

  const togglePanel = useCallback(() => {
    setPanelOpen((value) => !value)
    if (!panelOpen && activeTab == null && tabs.length > 0) {
      setActiveTab(tabs[tabs.length - 1] ?? null)
    }
  }, [activeTab, panelOpen, tabs])

  const createSession = useCallback(
    async (input?: { mode: 'create' | 'edit'; protocolId?: string }) => {
      try {
        const created = await createAgentSession(input ?? { mode: 'create' })
        await mutateSessions()
        setActiveId(created.id)
        setSession(created)
        setMessages([])
        setView('chat')
      } catch (error) {
        toast({ description: error instanceof Error ? error.message : '创建会话失败' })
      }
    },
    [mutateSessions, toast],
  )

  /** 总览卡片 → 进入工作区。 */
  const handleOpen = useCallback(
    (id: string) => {
      setView('chat')
      if (id === activeId) return
      setActiveId(id)
      void refreshSession(id)
    },
    [activeId, refreshSession],
  )

  const handleDelete = useCallback(
    async (id: string) => {
      const target = (sessions ?? []).find((item) => item.id === id)
      const ok = await confirm({
        title: `删除会话「${target?.title || '未命名'}」？`,
        description: '消息历史与方案进度将一并删除，无法恢复。',
        confirmText: '删除',
      })
      if (!ok) return
      try {
        await deleteAgentSession(id)
        if (id === activeId) {
          setActiveId(undefined)
          setSession(undefined)
          setMessages([])
        }
        await mutateSessions()
      } catch (error) {
        toast({ description: error instanceof Error ? error.message : '删除失败' })
      }
    },
    [activeId, confirm, mutateSessions, sessions, toast],
  )

  const handleSettingsChange = useCallback(
    async (patch: { settings?: Partial<AgentSettings>; title?: string }) => {
      if (!session) return
      try {
        const updated = await updateAgentSession(session.id, patch)
        setSession((current) => (current ? { ...current, settings: updated.settings } : updated))
        await mutateSessions()
      } catch (error) {
        toast({ description: error instanceof Error ? error.message : '保存设置失败' })
      }
    },
    [mutateSessions, session, toast],
  )

  /** 计划模式：确认执行 → 关闭计划模式并以用户消息通知助手开始执行。 */
  const handleConfirmPlan = useCallback(async () => {
    if (!session || live.running) return
    await handleSettingsChange({ settings: { planMode: false } })
    send({ content: '确认执行当前方案，请开始执行。' })
  }, [handleSettingsChange, live.running, send, session])

  /** 草稿还原：回滚到最近一轮修改前的还原点。 */
  const handleRestoreDraft = useCallback(async () => {
    if (!session || live.running) return
    const ok = await confirm({
      title: '还原到上一轮修改前？',
      description: '配置草稿将回滚到最近一轮对话修改前的状态，对话记录不受影响。',
      confirmText: '还原',
    })
    if (!ok) return
    try {
      const updated = await restoreAgentDraft(session.id)
      setSession(updated)
      toast({ description: '已还原到上一轮修改前的配置' })
    } catch (error) {
      toast({ description: error instanceof Error ? error.message : '还原失败' })
    }
  }, [confirm, live.running, session, toast])

  const handleClearHistory = useCallback(async () => {
    if (!session) return
    try {
      await clearAgentMessages(session.id, 0)
      setMessages([])
      await refreshSession(session.id)
      toast({ description: '已清空会话消息' })
    } catch (error) {
      toast({ description: error instanceof Error ? error.message : '清空失败' })
    }
  }, [refreshSession, session, toast])

  const handleStop = useCallback(() => {
    void stop()
  }, [stop])

  const handleApprove = useCallback(
    (decision: { approved: boolean }) => {
      approve(decision)
      // 批准后刷新列表让状态圆点进入 running。
      window.setTimeout(() => void mutateSessions(), 300)
    },
    [approve, mutateSessions],
  )

  const handleJumpTurn = useCallback((seq: number) => {
    setJumpTarget({ seq, nonce: Date.now() })
  }, [])

  const handleActiveTurn = useCallback((seq: number | null) => {
    setActiveTurnSeq(seq)
  }, [])

  const statusBadge = useMemo(() => {
    if (live.running) return { text: '进行中', color: 'var(--jade)' }
    if (session?.status === 'waiting_approval' && live.approvalPending) {
      return { text: '待审批', color: 'var(--amber)' }
    }
    if (session?.settings.planMode) return { text: '计划模式', color: 'var(--amber)' }
    return null
  }, [live.approvalPending, live.running, session?.settings.planMode, session?.status])

  if (view === 'list') {
    return (
      <div className="-mb-14 flex h-[max(560px,calc(100dvh-46px))] min-h-0">
        <div key="agent-overview" className="flex min-h-0 flex-1 animate-in fade-in duration-300 flex-col">
          <div className="flex items-center gap-2 px-6 pb-1 pt-1">
            <Sparkles className="h-4 w-4 text-rose" />
            <div className="min-w-0 flex-1">
              <p className="truncate text-sm font-semibold">AI 助手</p>
              <p className="truncate text-2xs text-muted-foreground">网关事务一句话：协议接入 · 模型配置 · 统计分析 · 报错诊断</p>
            </div>
          </div>
          <SessionOverview
            sessions={sessions ?? []}
            onOpen={handleOpen}
            onCreate={() => void createSession()}
            onDelete={(id) => void handleDelete(id)}
          />
        </div>
        {confirmDialog}
      </div>
    )
  }

  return (
    <div className="-mb-14 flex h-[max(560px,calc(100dvh-46px))] min-h-0">
      <div
        key={`agent-chat-${activeId ?? 'none'}`}
        className="flex min-h-0 flex-1 animate-in fade-in slide-in-from-bottom-2 duration-300 flex-col"
      >
        <div className="flex items-center gap-2 px-4 pb-2 pt-1">
          <Button variant="ghost" size="icon" className="h-8 w-8" title="返回会话总览" onClick={() => setView('list')}>
            <ArrowLeft className="h-4 w-4" />
          </Button>
          <Sparkles className="h-4 w-4 text-rose" />
          <div className="min-w-0 flex-1">
            <p className="truncate text-sm font-semibold">{session?.title || 'AI 助手'}</p>
            <p className="truncate text-2xs text-muted-foreground">
              {session
                ? session.mode === 'edit'
                  ? `编辑协议 ${session.protocolId ?? ''}`
                  : '网关运维 · 协议接入 · 统计分析'
                : '正在加载会话…'}
            </p>
          </div>
          {statusBadge ? (
            <TonePill color={statusBadge.color} className="text-2xs">{statusBadge.text}</TonePill>
          ) : null}
          {session ? (
            <Button
              variant="ghost"
              size="icon"
              className="h-8 w-8"
              title="清空本会话消息（保留草稿与设置）"
              disabled={live.running || messages.length === 0}
              onClick={() => void handleClearHistory()}
            >
              <Eraser className="h-4 w-4" />
            </Button>
          ) : null}
          <Button variant="ghost" size="icon" className="h-8 w-8" title={panelOpen ? '收起侧栏' : '展开侧栏'} onClick={togglePanel}>
            {panelOpen ? <PanelRightClose className="h-4 w-4" /> : <PanelRightOpen className="h-4 w-4" />}
          </Button>
        </div>

        {session ? (
          <div className="flex min-h-0 flex-1">
            <TurnRail messages={messages} live={live} activeSeq={activeTurnSeq} onJump={handleJumpTurn} />
            <ChatPanel
              session={session}
              messages={messages}
              live={live}
              onSettingsChange={handleSettingsChange}
              onSend={(input: { content?: string; documents?: AgentDocument[]; afterSeq?: number }) => {
                if (input.afterSeq != null) {
                  setMessages((current) => current.filter((message) => message.seq <= input.afterSeq!))
                }
                send(input)
              }}
              onApprove={handleApprove}
              onStop={handleStop}
              onOpenContextTab={(tab) => openContextTab(tab, true)}
              jumpTarget={jumpTarget}
              onActiveTurn={handleActiveTurn}
            />
            <div
              className={cn(
                'flex h-full shrink-0 overflow-hidden',
                !panelDragging && 'transition-[width] duration-300 ease-in-out',
              )}
              style={{ width: panelOpen ? panelW : 0 }}
            >
              <div
                role="separator"
                aria-orientation="vertical"
                aria-label="拖拽调整侧栏宽度"
                onPointerDown={onPanelHandleDown}
                onPointerMove={onPanelHandleMove}
                onPointerUp={onPanelHandleUp}
                onPointerCancel={onPanelHandleUp}
                className={cn(
                  'group relative w-1 shrink-0 cursor-col-resize touch-none select-none',
                  !panelOpen && 'pointer-events-none',
                )}
              >
                <span
                  className={cn(
                    'absolute inset-y-2 left-1/2 w-px -translate-x-1/2 bg-border transition-colors group-hover:bg-rose/50',
                    panelDragging && 'bg-rose/50',
                  )}
                />
              </div>
              <div
                className={cn(
                  'h-full min-w-0 flex-1 transition-opacity duration-300',
                  panelOpen ? 'opacity-100' : 'pointer-events-none opacity-0',
                )}
              >
                <ContextPanel
                  key={session.id}
                  session={session}
                  messages={messages}
                  live={live}
                  tabs={tabs}
                  activeTab={activeTab}
                  onTabSelect={setActiveTab}
                  onTabClose={closeContextTab}
                  onAutoOpen={(tab) => openContextTab(tab, false)}
                  onConfirmPlan={() => void handleConfirmPlan()}
                  onRestoreDraft={() => void handleRestoreDraft()}
                />
              </div>
            </div>
          </div>
        ) : (
          <div className="flex flex-1 items-center justify-center text-sm text-muted-foreground">
            正在加载会话…
          </div>
        )}
      </div>
      {confirmDialog}
    </div>
  )
}

/** 会话列表 SWR（本页专用）。 */
function useSWRSessionList() {
  return useSWR('agent-sessions', () => listAgentSessions(), {
    revalidateOnFocus: false,
    shouldRetryOnError: false,
    dedupingInterval: 2000,
    refreshInterval: 30_000,
  })
}
