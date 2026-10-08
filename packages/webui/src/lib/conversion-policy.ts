import { request } from './api'
import type { ConversionIssue } from './protocol-v2'

export type ConversionPhase = 'ingress' | 'request' | 'response' | 'event' | 'wire'
export interface ConversionMatch { sourceId?: string; targetId?: string; sourceFamily?: string; targetFamily?: string; sourceWire?: string; targetWire?: string; model?: string; operation?: string; transport?: string; nodeKind?: string; path?: string; present?: boolean }
export interface ConversionRule { id: string; order: number; enabled: boolean; phase: ConversionPhase; match: ConversionMatch; action: string; path?: string; value?: unknown; expression?: unknown; reason?: string }
export interface ConversionPolicy { schemaVersion: 1; id: string; name: string; mode?: 'compatible' | 'strict'; rules: ConversionRule[]; continuation?: { clientCarrier: boolean; persist: boolean; retentionSeconds: number; turnsPerSession: number; maxBytes: number; recordBytes: number }; usage?: { defaultIncludeUsage: boolean; collectUpstreamUsage: boolean } }
export interface ConversionSelection { policyId?: string; revisionHash?: string; overrides?: ConversionPolicy }
export interface ConversionRecord { id: string; hash: string; activeHash: string; policy: ConversionPolicy; selector: ConversionMatch; compilerVersion?: string; reports?: { passed: boolean; sourceHash: string; targetHash: string; issues: ConversionIssue[] }[] }
export interface ConversionRegistry { phases: ConversionPhase[]; actions: string[]; policy: unknown }
const base = '/protocols/conversion-policies'
const id = encodeURIComponent
export const newConversionPolicy = (): ConversionPolicy => ({ schemaVersion: 1, id: 'my-conversion', name: '转换行为', mode: 'compatible', rules: [], continuation: { clientCarrier: true, persist: true, retentionSeconds: 604800, turnsPerSession: 64, maxBytes: 536870912, recordBytes: 8388608 }, usage: { defaultIncludeUsage: false, collectUpstreamUsage: true } })
export const conversionAPI = {
  list: () => request<{ items: ConversionRecord[] }>(base).then((r) => r.items),
  save: (policy: ConversionPolicy, expectedHash: string) => request<ConversionRecord>(`${base}/${id(policy.id)}/draft`, { method: 'PUT', body: { policy, expectedHash } }),
  verify: (policyId: string, hash: string) => request<ConversionRecord>(`${base}/${id(policyId)}/verify`, { method: 'POST', body: { hash } }),
  revisions: (policyId: string) => request<{ items: ConversionRecord[] }>(`${base}/${id(policyId)}/revisions`).then((r) => r.items),
  activate: (policyId: string, hash: string, expectedActive: string, selector: ConversionMatch) => request(`${base}/${id(policyId)}/activate`, { method: 'POST', body: { hash, expectedActive, selector } }),
  preview: (policy: ConversionPolicy, phase: ConversionPhase, context: unknown, input: unknown) => request<{ output: unknown; issues: ConversionIssue[]; effective: { hash: string; origins: Record<string, string>; policy: ConversionPolicy }; persistentWrites: false }>(`${base}/preview`, { method: 'POST', body: { policy, phase, context, input } }),
  probeSignature: (policyId: string, input: { hash: string; ruleId: string; ingressId: string; sourceId: string; modelId: string; keyIndex?: number }) => request<{ verified: boolean; policyHash: string; targetRevision: string; modelId: string; expiresAt: number }>(`${base}/${id(policyId)}/probe-signature`, { method: 'POST', body: input }),
  stats: () => request<Record<string, number>>('/protocols/continuations'),
  clear: (session: string) => request('/protocols/continuations', { method: 'DELETE', query: { session } }),
}
