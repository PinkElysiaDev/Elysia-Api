/** 协议 Agent 的前端类型（与后端 backend/agent 包及 /api/admin/agent/* 对齐）。 */

export type AgentSessionMode = 'create' | 'edit'
export type AgentSessionStatus = 'idle' | 'running' | 'waiting_approval'
export type AgentPermission = 'ask' | 'always' | 'never'
export type AgentThinkingEffort = '' | 'low' | 'medium' | 'high' | 'max' | 'adaptive'

export interface AgentSettings {
  modelSourceId: string
  modelName: string
  thinkingEnabled: boolean
  thinkingEffort?: AgentThinkingEffort
  allowLiveTest?: AgentPermission
  allowSave?: AgentPermission
  testBaseUrl?: string
  testApiKeySet?: boolean
}

export interface AgentToolCall {
  id?: string
  type: string
  name?: string
  arguments?: unknown
  arguments_text?: string
}

export interface AgentPlanStep {
  title: string
  status: 'pending' | 'in_progress' | 'done'
}

export interface AgentSession {
  id: string
  title: string
  mode: AgentSessionMode
  protocolId?: string
  seedConfig?: unknown
  draftConfig?: unknown
  plan?: AgentPlanStep[]
  settings: AgentSettings
  status: AgentSessionStatus
  pendingAction?: AgentPendingAction | null
  createdAt: string
  updatedAt: string
}

export interface AgentPendingAction {
  calls: AgentToolCall[]
  reason?: string
}

export type AgentMessageRole = 'user' | 'assistant' | 'tool_result' | 'approval' | 'system'

export interface AgentDocument {
  name?: string
  mime?: string
  text?: string
  dataUrl?: string
}

export interface AgentUserContent {
  text?: string
  documents?: AgentDocument[]
}

export interface AgentAssistantContent {
  text?: string
  reasoning?: string
  toolCalls?: AgentToolCall[]
}

export interface AgentToolResultContent {
  callId: string
  name: string
  input?: unknown
  ok: boolean
  summary?: string
  data?: unknown
  durationMs?: number
}

export interface AgentApprovalContent {
  decision: 'approved' | 'denied'
  names: string[]
  note?: string
}

export interface AgentSystemContent {
  text: string
  kind?: string
}

export interface AgentUsage {
  input_tokens?: number
  output_tokens?: number
  total_tokens?: number
  reasoning_tokens?: number
  [key: string]: unknown
}

export interface AgentMessage {
  seq: number
  role: AgentMessageRole
  content: unknown
  model?: string
  usage?: AgentUsage
  createdAt: string
}

export interface AgentSessionDetail {
  session: AgentSession
  messages: AgentMessage[]
}

/** SSE 事件（与后端 agent.Event 对齐，仅取前端关心的字段）。 */
export interface AgentStreamEvent {
  type:
    | 'status'
    | 'text_delta'
    | 'reasoning_delta'
    | 'tool_call'
    | 'tool_result'
    | 'draft_updated'
    | 'plan_updated'
    | 'approval_required'
    | 'message'
    | 'turn_done'
    | 'error'
  text?: string
  retryable?: boolean
  delta?: string
  callId?: string
  name?: string
  input?: unknown
  result?: AgentToolResultContent
  draft?: unknown
  plan?: AgentPlanStep[]
  approval?: AgentPendingAction
  message?: AgentMessage
  usage?: AgentUsage
  model?: string
  durationMs?: number
  rounds?: number
}

export interface AgentTurnUsage {
  inputTokens: number
  outputTokens: number
  totalTokens: number
}

/** 门控工具名 → 审批卡片文案。 */
export const AGENT_GATED_TOOL_LABELS: Record<string, string> = {
  test_upstream: '向真实上游发送测试请求',
  test_model_list: '向真实上游请求模型列表',
  save_protocol: '把当前草稿保存为正式协议',
  create_model_source: '创建模型源',
  update_model_source: '修改模型源',
  create_model_group: '创建模型组',
  update_model_group: '修改模型组',
  refresh_model_source: '从上游拉取模型列表',
}

export function agentToolLabel(name: string): string {
  return AGENT_GATED_TOOL_LABELS[name] ?? name
}

/** 从 tool_result 内容提取精简展示（工具卡片用）。 */
export function toolResultBrief(content: AgentToolResultContent): string {
  if (content.summary) return content.summary
  return content.ok ? '执行完成' : '执行失败'
}

/** 累计用量格式化。 */
export function formatUsage(usage: AgentUsage | undefined): string {
  if (!usage) return ''
  const inTok = usage.input_tokens ?? 0
  const outTok = usage.output_tokens ?? 0
  const total = usage.total_tokens ?? inTok + outTok
  return `↑${inTok} ↓${outTok} · ${total} tokens`
}
