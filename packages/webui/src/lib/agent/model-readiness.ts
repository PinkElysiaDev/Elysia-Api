import { request } from '../api'
export interface AgentModelReadiness {
  sourceId: string; modelId: string; modelName: string; available: boolean
  modelTools: boolean; capabilitySource: string; bindingKind?: string; bindingTools: boolean
  protocolId?: string; revision?: string; canRepair: boolean; reasonCode?: string; reason?: string
}
export const agentModelAPI = {
  list: () => request<{ items: AgentModelReadiness[] }>('/agent/models').then((result) => result.items ?? []),
  verifyTools: (sourceId: string, modelId: string) => request<AgentModelReadiness>('/agent/models/verify-tools', { method: 'POST', body: { sourceId, modelId } }),
}
