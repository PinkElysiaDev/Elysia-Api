import { PanelRightClose, PanelRightOpen, Sparkles } from 'lucide-react'
import { useCallback, useEffect, useMemo, useState } from 'react'
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
  updateAgentSession,
} from '@/lib/agent/api'
import type { AgentDocument, AgentMessage, AgentSession, AgentSettings, AgentStreamEvent } from '@/lib/agent/types'
import { useAgentStream } from '@/lib/agent/use-agent-stream'
import { ChatPanel } from './chat-panel'
import { ContextPanel } from './context-panel'
import { SessionList } from './session-list'

/** AI 助手页：会话列表 | 聊天 | 多用途侧栏（方案/草稿/进展）。 */
export function AgentPage() {
  const { toast } = useToast()
  const { confirm, dialog: confirmDialog } = useConfirm()
  const navigate = useNavigate()
  const [searchParams] = useSearchParams()
  const [activeId, setActiveId] = useState<string | undefined>()
  const [session, setSession] = useState<AgentSession | undefined>()
  const [messages, setMessages] = useState<AgentMessage[]>([])
  const [draftOpen, setDraftOpen] = useState(true)
  const [bootstrapped, setBootstrapped] = useState(false)

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

  /** 入口跳转：?mode=create | ?mode=edit&protocol=<id> 自动建会话。 */
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

  const createSession = useCallback(
    async (input?: { mode: 'create' | 'edit'; protocolId?: string }) => {
      try {
        const created = await createAgentSession(input ?? { mode: 'create' })
        await mutateSessions()
        setActiveId(created.id)
        setSession(created)
        setMessages([])
      } catch (error) {
        toast({ description: error instanceof Error ? error.message : '创建会话失败' })
      }
    },
    [mutateSessions, toast],
  )

  const handleSelect = useCallback(
    (id: string) => {
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

  const statusBadge = useMemo(() => {
    if (live.running) return { text: '进行中', color: 'var(--jade)' }
    if (session?.status === 'waiting_approval' && live.approvalPending) {
      return { text: '待审批', color: 'var(--amber)' }
    }
    return null
  }, [live.approvalPending, live.running, session?.status])

  return (
    <div className="flex h-[max(560px,calc(100dvh-102px))] min-h-0">
      <SessionList
        sessions={sessions ?? []}
        activeId={activeId}
        onSelect={handleSelect}
        onCreate={() => void createSession()}
        onDelete={(id) => void handleDelete(id)}
      />

      <div className="flex min-w-0 flex-1 flex-col">
        <div className="flex items-center gap-2 px-4 pb-2 pt-1">
          <Sparkles className="h-4 w-4 text-rose" />
          <div className="min-w-0 flex-1">
            <p className="truncate text-sm font-semibold">{session?.title || 'AI 助手'}</p>
            <p className="truncate text-2xs text-muted-foreground">
              {session
                ? session.mode === 'edit'
                  ? `编辑协议 ${session.protocolId ?? ''}`
                  : '网关运维 · 协议接入 · 统计分析'
                : '选择或创建一个会话开始'}
            </p>
          </div>
          {statusBadge ? (
            <TonePill color={statusBadge.color} className="text-2xs">{statusBadge.text}</TonePill>
          ) : null}
          <Button variant="ghost" size="icon" className="h-8 w-8" title={draftOpen ? '收起侧栏' : '展开侧栏'} onClick={() => setDraftOpen((value) => !value)}>
            {draftOpen ? <PanelRightClose className="h-4 w-4" /> : <PanelRightOpen className="h-4 w-4" />}
          </Button>
        </div>

        {session ? (
          <>
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
              onClearHistory={() => void handleClearHistory()}
            />
          </>
        ) : (
          <div className="flex flex-1 items-center justify-center text-sm text-muted-foreground">
            左侧选择一个会话，或点击「新会话」开始
          </div>
        )}
      </div>

      {session && draftOpen ? <ContextPanel session={session} messages={messages} live={live} /> : null}
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
