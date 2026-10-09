import { request } from './api'
import type { ConversionRegistry, ConversionSelection } from './conversion-policy'
import { ProtocolDocument } from './protocol-document'

export interface ConversionIssue { code: string; severity: string; direction?: string; stage?: string; path: string; capability?: string; reason: string; suggestion: string; evidence?: string }
export interface ProtocolSchema { conversion: ConversionRegistry; compilerVersion: string; transports: string[]; operationKinds: string[]; directions: string[]; capabilities: string[]; engineFeatures: string[]; modules: { name: string; directions: string[] }[]; events: string[]; definitionSchema: unknown; semanticSchema: unknown; mappingOperations: { name: string; description: string }[] }
export interface ProtocolDraft { protocolId: string; hash: string; definition: string; updatedAt: string }
export interface ProtocolRevision { protocolId: string; hash: string; definition: string; createdAt: string }
export interface Activation { protocolId: string; revisionHash: string }
export interface ProtocolHistoryItem { id: string; protocolId: string; hash: string; name: string; version: string; reason: 'preset_replaced' | 'custom_deleted'; isDraft: boolean; createdAt: string; archivedAt: string; definition?: string }
export interface ProtocolReference { kind: string; id: string; sourceId?: string; name?: string }
export interface ProtocolBindingEntry { conversion?: ConversionSelection; kind: 'source' | 'model' | 'group'; sourceId: string; modelId?: string; groupId?: string; unbound?: boolean; binding: { protocolId?: string; revisionHash?: string; capabilities?: Record<string, boolean>; transports?: string[]; operation?: string } }
export interface ProtocolReferences { baseline: string; references: ProtocolReference[]; affectedModels: ProtocolReference[] }
export interface ProtocolHistoryDetail { item: ProtocolHistoryItem & { definition: string }; report?: VerificationReport; references: ProtocolReference[]; currentHash?: string; changes?: unknown[] }
export interface VerificationReport { definitionHash: string; compilerVersion: string; samplesHash: string; kind: string; passed: boolean; covered: string[]; checks: { sampleId: string; direction?: string; passed: boolean; capabilities?: string[] }[]; issues: ConversionIssue[] }
export interface Preview { exactJSON: string; issues: ConversionIssue[] }
export interface ProtocolListing { drafts: ProtocolDraft[]; active: Activation[]; loaded: Record<string, string>; presets?: string[]; runtimeFailures?: Record<string, string> }
/** Capabilities of an enabled, compiled revision; drafts are excluded. */
export interface EnabledProtocol { id: string; name: string; revision: string; preset?: boolean; directions: string[]; capabilities: Record<string, boolean>; canGenerate: boolean; hasModelDiscovery: boolean; hasAgentPolicy: boolean }
const base = '/protocols'
const identifier = (id: string) => encodeURIComponent(id)

function responseData(text: string) { const document = new ProtocolDocument(text); return { document, data: JSON.parse(document.read('/data') ?? 'null') } }
function preserveDefinition<T extends { definition: string }>(item: T, document: ProtocolDocument, pointer: string): T { return { ...item, definition: document.read(pointer) ?? '{}' } }
async function previewRequest(rawBody: string): Promise<Preview> {
  const { document, data } = responseData(await request<string>(`${base}/preview`, { method: 'POST', rawBody, rawResponse: true }))
  return { exactJSON: document.read('/data') ?? '{}', issues: data.issues ?? [] }
}

/** Protocol authoring shares server compilation and verification with forwarding. */
export const protocolAPI = {
  bindings: () => request<ProtocolBindingEntry[]>(`${base}/bindings`),
  saveBinding: (entry: ProtocolBindingEntry) => request<ProtocolBindingEntry>(`${base}/bindings`, { method: 'PUT', body: entry }),
  history: () => request<{ items: ProtocolHistoryItem[] }>(`${base}/history`).then((result) => result.items),
  async historyDetail(id: string): Promise<ProtocolHistoryDetail> {
    const { document, data } = responseData(await request<string>(`${base}/history/${identifier(id)}`, { rawResponse: true }))
    return { ...data, item: preserveDefinition(data.item, document, '/data/item/definition') }
  },
  restore: (archiveId: string, id: string, name: string) => request<{ protocolId: string; activated: boolean; issues: ConversionIssue[] }>(`${base}/history/${identifier(archiveId)}/restore`, { method: 'POST', body: { id, name } }),
  deleteHistory: (id: string) => request(`${base}/history/${identifier(id)}`, { method: 'DELETE' }),
  references: (id: string) => request<ProtocolReferences>(`${base}/${identifier(id)}/references`),
  archive: (id: string, baseline: string, mode: 'block' | 'replace' | 'unbind', targetProtocolId?: string) => request(`${base}/${identifier(id)}/archive`, { method: 'POST', body: { baseline, mode, targetProtocolId } }),
  enabled: () => request<{ items: EnabledProtocol[] }>(`${base}/enabled`).then((result) => result.items),
  schema: () => request<ProtocolSchema>(`${base}/schema`),
  async list(): Promise<ProtocolListing> {
    const { document, data } = responseData(await request<string>(base, { rawResponse: true }))
    return { ...data, drafts: (data.drafts ?? []).map((draft: ProtocolDraft, index: number) => preserveDefinition(draft, document, `/data/drafts/${index}/definition`)), active: data.active ?? [] }
  },
  async save(id: string, definition: string, previous: string): Promise<{ draft: ProtocolDraft; issues: ConversionIssue[] }> {
    const { document, data } = responseData(await request<string>(`${base}/${identifier(id)}/draft`, { method: 'PUT', rawBody: definition, headers: { 'If-Match': previous }, rawResponse: true }))
    return { ...data, draft: preserveDefinition(data.draft, document, '/data/draft/definition') }
  },
  validate: (definition: string) => request<{ valid: boolean; hash?: string; issues: ConversionIssue[] }>(`${base}/validate`, { method: 'POST', rawBody: definition }),
  verify: (id: string, draftHash: string) => request<{ revision: { hash: string }; report: VerificationReport }>(`${base}/${identifier(id)}/verify`, { method: 'POST', body: { draftHash } }),
  activate: (id: string, revisionHash: string, expectedActive: string, isRollback = false) => request<Activation>(`${base}/${identifier(id)}/${isRollback ? 'rollback' : 'activate'}`, { method: 'POST', body: { revisionHash, expectedActive } }),
  async revisions(id: string): Promise<ProtocolRevision[]> {
    const { document, data } = responseData(await request<string>(`${base}/${identifier(id)}/revisions`, { rawResponse: true }))
    return (data.items ?? []).map((revision: ProtocolRevision, index: number) => preserveDefinition(revision, document, `/data/items/${index}/definition`))
  },
  report: (id: string, hash: string) => request<{ report: VerificationReport }>(`${base}/${identifier(id)}/revisions/${identifier(hash)}`),
  verifyRevision: (id: string, hash: string) => request<VerificationReport>(`${base}/${identifier(id)}/revisions/${identifier(hash)}/verify`, { method: 'POST' }),
  diff: (id: string, from: string, to: string) => request<{ changes: { path: string; before?: unknown; after?: unknown }[] }>(`${base}/${identifier(id)}/diff`, { query: { from, to } }),
  preview: (definition: string, direction: string, input: string, sequence: boolean) => previewRequest(`{"definition":${definition},"direction":${JSON.stringify(direction)},"input":${input},"sequence":${sequence}}`),
  workflow: (definition: string, options: { mode: string; sample?: string; operation?: string; kind?: string; purpose?: string }, input: string) => previewRequest(`{"definition":${definition},"input":${input},${JSON.stringify(options).slice(1)}`),
  combine: (ingress: string, upstream: string) => request<{ passed: boolean; checks: unknown[]; issues: ConversionIssue[] }>(`${base}/combinations`, { method: 'POST', rawBody: `{"ingress":${ingress},"upstream":${upstream}}` }),
  probe: (definition: string, semanticRequest: string, target: { operation: string; baseUrl: string; apiKey: string }) => request<{ report: VerificationReport }>(`${base}/test`, { method: 'POST', rawBody: `{"definition":${definition},"request":${semanticRequest},${JSON.stringify(target).slice(1)}` }),
  upstreamReports: (id: string, hash: string) => request<VerificationReport[]>(`${base}/${identifier(id)}/revisions/${identifier(hash)}/upstream-reports`),
}
