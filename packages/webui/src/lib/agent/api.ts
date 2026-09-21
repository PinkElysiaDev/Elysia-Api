import { request } from '../api'
import type {
  AgentDocument,
  AgentSession,
  AgentSessionDetail,
  AgentSettings,
} from './types'

/** 会话 CRUD（非流式部分）。SSE 端点见 sse.ts。 */

export async function listAgentSessions(): Promise<AgentSession[]> {
  const data = await request<{ items: AgentSession[] }>('/agent/sessions')
  return data.items ?? []
}

export async function createAgentSession(input: {
  title?: string
  mode?: 'create' | 'edit'
  protocolId?: string
  settings?: Partial<AgentSettings>
}): Promise<AgentSession> {
  return request<AgentSession>('/agent/sessions', { method: 'POST', body: input })
}

export async function getAgentSession(id: string): Promise<AgentSessionDetail> {
  return request<AgentSessionDetail>(`/agent/sessions/${id}`)
}

export async function updateAgentSession(
  id: string,
  input: {
    title?: string
    settings?: Partial<AgentSettings>
    apiKey?: string
    clearApiKey?: boolean
  },
): Promise<AgentSession> {
  return request<AgentSession>(`/agent/sessions/${id}`, { method: 'PATCH', body: input })
}

export async function deleteAgentSession(id: string): Promise<void> {
  await request(`/agent/sessions/${id}`, { method: 'DELETE' })
}

/** 把草稿回滚到最近一轮修改前的还原点（返回更新后的会话）。 */
export async function restoreAgentDraft(id: string): Promise<AgentSession> {
  return request<AgentSession>(`/agent/sessions/${id}/restore-draft`, { method: 'POST' })
}

/** 清空会话消息（afterSeq=0 全清，保留会话与草稿）。 */
export async function clearAgentMessages(id: string, afterSeq = 0): Promise<void> {
  await request(`/agent/sessions/${id}/messages`, { method: 'DELETE', query: { afterSeq } })
}

export interface AgentSendMessageInput {
  content?: string
  documents?: AgentDocument[]
  /** 截断 seq > afterSeq 后再发送：编辑重发/失败重试/重新生成共用。 */
  afterSeq?: number
}

export async function stopAgentTurn(id: string): Promise<boolean> {
  const data = await request<{ stopped: boolean }>(`/agent/sessions/${id}/stop`, { method: 'POST' })
  return data.stopped
}
