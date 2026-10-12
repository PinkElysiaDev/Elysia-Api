import { request } from './api'
import { ProtocolDocument } from './protocol-document'
import type { ConversionIssue } from './protocol-v2'

export type ConversionPhase = 'ingress' | 'request' | 'response' | 'event' | 'wire'
export interface ConversionMatch { sourceId?: string; targetId?: string; sourceFamily?: string; targetFamily?: string; sourceWire?: string; targetWire?: string; model?: string; operation?: string; transport?: string; nodeKind?: string; path?: string; present?: boolean }
export interface ConversionRule { id: string; order: number; enabled: boolean; phase: ConversionPhase; match: ConversionMatch; action: string; path?: string; value?: unknown; expression?: unknown; reason?: string }
export interface ConversionPolicy { schemaVersion: 1; id: string; name: string; mode?: 'compatible' | 'strict'; rules: ConversionRule[]; continuation?: { clientCarrier: boolean; persist: boolean; retentionSeconds: number; turnsPerSession: number; maxBytes: number; recordBytes: number }; usage?: { defaultIncludeUsage: boolean; collectUpstreamUsage: boolean } }
export interface ConversionSelection { policyId?: string; revisionHash?: string; overrides?: ConversionPolicy; overridesJSON?: string }
export interface ConversionRecord { id: string; hash: string; activeHash: string; policy: ConversionPolicy; policyJSON: string; selector: ConversionMatch; compilerVersion?: string; reports?: { passed: boolean; sourceHash: string; targetHash: string; issues: ConversionIssue[] }[] }
export interface ConversionRegistry { phases: ConversionPhase[]; actions: string[]; actionSchemas?: Record<string, { description: string; phases: ConversionPhase[]; value?: { type: string; enum?: string[]; default?: unknown } }>; policy: unknown }
const base = '/protocols/conversion-policies'
const id = encodeURIComponent
export const newConversionPolicy = (): ConversionPolicy => ({ schemaVersion: 1, id: 'my-conversion', name: '转换行为', mode: 'compatible', rules: [], continuation: { clientCarrier: true, persist: true, retentionSeconds: 604800, turnsPerSession: 64, maxBytes: 536870912, recordBytes: 8388608 }, usage: { defaultIncludeUsage: false, collectUpstreamUsage: true } })
function readRecord(document: ProtocolDocument, pointer: string): ConversionRecord {
  const record = JSON.parse(document.read(pointer)!) as ConversionRecord
  return { ...record, policyJSON: document.read(`${pointer}/policy`)! }
}
async function readRecords(path: string): Promise<ConversionRecord[]> {
  const document = new ProtocolDocument(await request<string>(path, { rawResponse: true }))
  return [...document.locate('/data/items')!.children.keys()].map((key) => readRecord(document, `/data/items/${key}`))
}

/** Conversion documents travel as exact JSON; typed metadata is for form display only. */
export const conversionAPI = {
  list: () => readRecords(base),
  async save(policyJSON: string, expectedHash: string): Promise<ConversionRecord> {
    const policy = new ProtocolDocument(policyJSON)
    const policyId = JSON.parse(policy.read('/id')!) as string
    const document = new ProtocolDocument(await request<string>(`${base}/${id(policyId)}/draft`, { method: 'PUT', rawBody: `{"policy":${policyJSON},"expectedHash":${JSON.stringify(expectedHash)}}`, rawResponse: true }))
    return readRecord(document, '/data')
  },
  async verify(policyId: string, hash: string): Promise<ConversionRecord> {
    return readRecord(new ProtocolDocument(await request<string>(`${base}/${id(policyId)}/verify`, { method: 'POST', body: { hash }, rawResponse: true })), '/data')
  },
  revisions: (policyId: string) => readRecords(`${base}/${id(policyId)}/revisions`),
  activate: (policyId: string, hash: string, expectedActive: string, selector: ConversionMatch) => request(`${base}/${id(policyId)}/activate`, { method: 'POST', body: { hash, expectedActive, selector } }),
  async preview(policyJSON: string, phase: ConversionPhase, contextJSON: string, inputJSON: string) {
    for (const source of [policyJSON, contextJSON, inputJSON]) new ProtocolDocument(source)
    const document = new ProtocolDocument(await request<string>(`${base}/preview`, { method: 'POST', rawBody: `{"policy":${policyJSON},"phase":${JSON.stringify(phase)},"context":${contextJSON},"input":${inputJSON}}`, rawResponse: true }))
    const exactJSON = document.read('/data')!
    const metadata = JSON.parse(exactJSON) as { issues: ConversionIssue[]; effective: { origins: Record<string, string> } }
    return { exactJSON, issues: metadata.issues, origins: metadata.effective.origins }
  },
  probeSignature: (policyId: string, input: { hash: string; ruleId: string; ingressId: string; sourceId: string; modelId: string; keyIndex?: number }) => request<{ verified: boolean; policyHash: string; targetRevision: string; modelId: string; expiresAt: number }>(`${base}/${id(policyId)}/probe-signature`, { method: 'POST', body: input }),
  stats: () => request<Record<string, number>>('/protocols/continuations'),
  clear: (session: string) => request('/protocols/continuations', { method: 'DELETE', query: { session } }),
}
