import { useCallback, useEffect, useState } from 'react'
import { useLocation, useNavigate } from 'react-router-dom'
import { PageHeader } from '@/components/page-header'
import { EmptyState } from '@/components/ui/states'
import { Button } from '@/components/ui/button'
import { protocolAPI, type ProtocolDraft, type ProtocolListing, type ProtocolSchema } from '@/lib/protocol-v2'
import { customPlatformValue, protocolLabel } from '@/lib/protocol'
import { ProtocolV2Editor } from './v2-editor'
import { ProtocolArchiveDialog } from './archive-dialog'
import { ProtocolDocument } from '@/lib/protocol-document'

/** Versioned definitions are saved as drafts and activated only with evidence. */
export function ProtocolDesignerPage() {
  const navigate = useNavigate()
  const location = useLocation()
  const [schema, setSchema] = useState<ProtocolSchema>()
  const [listing, setListing] = useState<ProtocolListing>()
  const [error, setError] = useState('')
  const [archiveID, setArchiveID] = useState('')
  const [selection, setSelection] = useState<{ draft?: ProtocolDraft; source?: string }>()
  const refresh = useCallback(async (id: string) => {
    const items = await protocolAPI.list()
    setListing(items)
    setSelection((current) => current && ({ ...current, draft: items.drafts.find((entry) => entry.protocolId === id) }))
  }, [])
  /** 预置只读：复制为派生 ID 的可编辑草稿（名称加副本后缀）。 */
  const copyPreset = useCallback(async (id: string) => {
    const revisions = await protocolAPI.revisions(id)
    const latest = revisions.find((revision) => revision.hash === listing?.active.find((entry) => entry.protocolId === id)?.revisionHash)
    if (!latest) return
    const document = new ProtocolDocument(latest.definition)
    const taken = new Set(listing?.drafts.map((draft) => draft.protocolId) ?? [])
    let candidate = `${id}-copy`
    for (let index = 1; taken.has(candidate); index += 1) candidate = `${id}-copy-${index}`
    const source = new ProtocolDocument(document.set('/id', JSON.stringify(candidate))).set('/name', JSON.stringify(`${JSON.parse(document.read('/name') ?? JSON.stringify(id))}（副本）`))
    setSelection({ source })
  }, [listing])
  useEffect(() => {
    let isCurrent = true
    void Promise.all([protocolAPI.schema(), protocolAPI.list()]).then(([contract, items]) => { if (isCurrent) { setSchema(contract); setListing(items) } }).catch((failure: unknown) => { if (isCurrent) setError(String(failure)) })
    return () => { isCurrent = false }
  }, [])
  useEffect(() => {
    const state = location.state as { draft?: unknown; definitionJSON?: string } | null
    if (state?.definitionJSON) { setSelection({ source: state.definitionJSON }); navigate(location.pathname, { replace: true, state: null }) }
    else if (state?.draft) navigate('/protocols/legacy', { replace: true, state })
  }, [location, navigate])
  useEffect(() => {
    const state = location.state as { protocolId?: string } | null
    if (state?.protocolId && listing) {
      const draft = listing.drafts.find((entry) => entry.protocolId === state.protocolId)
      if (draft) setSelection({ draft })
      navigate(location.pathname, { replace: true, state: null })
    }
  }, [listing, location, navigate])
  const presetIDs = new Set(listing?.presets ?? [])
  const customDrafts = (listing?.drafts ?? []).filter((draft) => !presetIDs.has(draft.protocolId))
  return <div className="space-y-6">
    <PageHeader title="协议设计器" actions={!selection && <><Button variant="ghost" onClick={() => navigate('/protocols/history')}>协议历史</Button><Button onClick={() => navigate('/agent?mode=create')}>Agent 编写</Button><Button variant="primary" onClick={() => setSelection({})}>新建协议</Button></>} />
    {archiveID && <ProtocolArchiveDialog id={archiveID} onClose={() => setArchiveID('')} onArchived={() => refresh(archiveID)} />}
    {error && <p role="alert" className="text-destructive">{error}</p>}
    {!schema || !listing ? <p role="status">正在读取协议引擎契约…</p> : selection ? <ProtocolV2Editor schema={schema} draft={selection.draft} initialSource={selection.source} activeHash={listing.active.find((entry) => entry.protocolId === selection.draft?.protocolId)?.revisionHash ?? ''} onSaved={refresh} onClose={() => setSelection(undefined)} /> : <>
      {(listing.presets ?? []).length > 0 && <div className="space-y-2">
        <h2 className="text-sm font-medium">预置协议（只读）</h2>
        <div className="overflow-x-auto"><table className="w-full text-sm"><thead><tr className="border-b text-left"><th className="p-3">协议</th><th>当前版本</th><th className="p-3 text-center">操作</th></tr></thead><tbody>{(listing.presets ?? []).map((id) => {
          const active = listing.active.find((entry) => entry.protocolId === id)
          return <tr key={id} className="border-b"><td className="p-3"><span className="font-medium">{protocolLabel(customPlatformValue(id), 'long')}</span><span className="ml-2 font-mono text-2xs text-muted-foreground">{id}</span></td><td className="p-3 font-mono text-xs">{active?.revisionHash.slice(0, 12) ?? '—'}</td><td className="p-3 text-center"><Button variant="ghost" onClick={() => void copyPreset(id)}>复制为新协议</Button></td></tr>
        })}</tbody></table></div>
      </div>}
      {customDrafts.length === 0 ? <EmptyState title="暂无自定义协议" /> : <div className="overflow-x-auto"><table className="w-full text-sm"><thead><tr className="border-b text-left"><th className="p-3">协议</th><th>草稿</th><th>启用状态</th><th className="p-3 text-center">操作</th></tr></thead><tbody>{customDrafts.map((draft) => {
        const active = listing.active.find((entry) => entry.protocolId === draft.protocolId)
        return <tr key={draft.protocolId} className="border-b"><td className="p-3 font-mono">{draft.protocolId}</td><td className="p-3 font-mono text-xs">{draft.hash.slice(0, 12)}</td><td className="p-3">{active ? listing.loaded[draft.protocolId] === active.revisionHash ? '已启用' : '需重新验证或修复' : '未启用'}</td><td className="p-3 text-center"><Button onClick={() => setSelection({ draft })}>编辑 {draft.protocolId}</Button><Button variant="danger" onClick={() => setArchiveID(draft.protocolId)}>删除 {draft.protocolId}</Button></td></tr>
      })}</tbody></table></div>}
    </>}
  </div>
}
