import { useCallback, useEffect, useRef, useState } from 'react'
import { streamAgentEvents } from './sse'
import type { AgentPendingAction, AgentStreamEvent, AgentTurnUsage } from './types'

export interface AgentToolCard {
  callId: string
  name: string
  status: 'running' | 'done' | 'failed'
  summary?: string
}

export interface AgentLiveState {
  running: boolean
  statusText: string
  text: string
  reasoning: string
  toolCards: AgentToolCard[]
  approvalPending: AgentPendingAction | null
  error: { text: string; retryable: boolean } | null
  turnUsage: AgentTurnUsage | null
}

const initialState: AgentLiveState = {
  running: false,
  statusText: '',
  text: '',
  reasoning: '',
  toolCards: [],
  approvalPending: null,
  error: null,
  turnUsage: null,
}

function reduce(state: AgentLiveState, event: AgentStreamEvent): AgentLiveState {
  switch (event.type) {
    case 'status':
      return { ...state, statusText: event.text ?? '' }
    case 'text_delta':
      return { ...state, text: state.text + (event.delta ?? '') }
    case 'reasoning_delta':
      return { ...state, reasoning: state.reasoning + (event.delta ?? '') }
    case 'tool_call': {
      const callId = event.callId ?? `live-${state.toolCards.length}`
      return {
        ...state,
        toolCards: [...state.toolCards, { callId, name: event.name ?? callId, status: 'running' }],
      }
    }
    case 'tool_result': {
      const callId = event.callId ?? event.result?.callId ?? ''
      return {
        ...state,
        toolCards: state.toolCards.map((card) =>
          card.callId === callId && card.status === 'running'
            ? {
                ...card,
                status: event.result?.ok === false ? 'failed' : 'done',
                summary: event.result?.summary,
              }
            : card,
        ),
      }
    }
    case 'approval_required':
      return { ...state, running: false, approvalPending: event.approval ?? null, statusText: '' }
    case 'message': {
      // 持久化消息已落库：清掉对应的现场（终稿或工具结果），避免双份展示。
      const role = event.message?.role
      if (role === 'assistant') return { ...state, text: '', reasoning: '' }
      if (role === 'tool_result') {
        const content = event.message?.content as { callId?: string } | undefined
        const callId = content?.callId ?? ''
        return { ...state, toolCards: state.toolCards.filter((card) => card.callId !== callId) }
      }
      return state
    }
    case 'turn_done': {
      const usage = event.usage
      return {
        ...state,
        running: false,
        statusText: '',
        turnUsage: usage
          ? {
              inputTokens: usage.input_tokens ?? 0,
              outputTokens: usage.output_tokens ?? 0,
              totalTokens: usage.total_tokens ?? (usage.input_tokens ?? 0) + (usage.output_tokens ?? 0),
            }
          : null,
      }
    }
    case 'error':
      return {
        ...state,
        running: false,
        statusText: '',
        error: { text: event.text ?? '未知错误', retryable: event.retryable ?? false },
      }
    default:
      return state
  }
}

export interface UseAgentStreamOptions {
  /** 持久化消息事件回调（Chat 负责并入消息列表）。 */
  onMessage?: (event: AgentStreamEvent) => void
  /** 草稿更新 / 轮次结束等需要刷新会话详情的时机。 */
  onSessionDirty?: () => void
}

/**
 * 单会话的流式轮次状态机：send/approve 启动 SSE，事件归约为现场态
 * （增量文本/思维链/工具卡片/审批卡/错误）。断流不取消服务端轮次，
 * stop() 才会真正停止。
 */
export function useAgentStream(sessionId: string | undefined, options: UseAgentStreamOptions = {}) {
  const [live, setLive] = useState<AgentLiveState>(initialState)
  const abortRef = useRef<AbortController | null>(null)
  const optionsRef = useRef(options)
  optionsRef.current = options

  useEffect(() => {
    // 会话切换重置现场。
    setLive(initialState)
    return () => {
      abortRef.current?.abort()
      abortRef.current = null
    }
  }, [sessionId])

  const runStream = useCallback(
    (path: string, body: unknown) => {
      if (!sessionId) return
      setLive((state) => ({
        ...initialState,
        running: true,
        turnUsage: state.turnUsage,
      }))
      const controller = new AbortController()
      abortRef.current = controller
      streamAgentEvents(`/api/admin/agent/sessions/${sessionId}${path}`, body, (event) => {
        if (event.type === 'message') optionsRef.current.onMessage?.(event)
        if (event.type === 'draft_updated' || event.type === 'plan_updated' || event.type === 'turn_done') {
          optionsRef.current.onSessionDirty?.()
        }
        setLive((state) => reduce(state, event))
      }, controller.signal).catch((err: unknown) => {
        if (err instanceof DOMException && err.name === 'AbortError') return
        setLive((state) =>
          state.running
            ? {
                ...state,
                running: false,
                statusText: '',
                error: { text: err instanceof Error ? err.message : '连接中断', retryable: true },
              }
            : state,
        )
        optionsRef.current.onSessionDirty?.()
      })
    },
    [sessionId],
  )

  const send = useCallback(
    (input: { content?: string; documents?: unknown[]; afterSeq?: number }) => {
      runStream('/messages', input)
    },
    [runStream],
  )

  const approve = useCallback(
    (decision: { approved: boolean; baseUrl?: string; apiKey?: string; note?: string }) => {
      setLive((state) => ({ ...state, approvalPending: null }))
      runStream('/approve', decision)
    },
    [runStream],
  )

  const stop = useCallback(async () => {
    if (!sessionId) return
    abortRef.current?.abort()
    abortRef.current = null
    try {
      const { stopAgentTurn } = await import('./api')
      await stopAgentTurn(sessionId)
    } finally {
      optionsRef.current.onSessionDirty?.()
    }
  }, [sessionId])

  const dismissError = useCallback(() => {
    setLive((state) => ({ ...state, error: null }))
  }, [])

  return { live, send, approve, stop, dismissError }
}
