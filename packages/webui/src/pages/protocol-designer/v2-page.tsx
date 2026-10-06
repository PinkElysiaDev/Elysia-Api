import { useCallback, useEffect, useState } from 'react'
import { useLocation, useNavigate } from 'react-router-dom'
import { PageHeader } from '@/components/page-header'
import { Button } from '@/components/ui/button'
import { protocolAPI, type ProtocolDraft, type ProtocolListing, type ProtocolSchema } from '@/lib/protocol-v2'
import { ProtocolV2Editor } from './v2-editor'

/** Versioned definitions are saved as drafts and activated only with evidence. */
export function ProtocolDesignerPage() {
  const navigate = useNavigate()
  const location = useLocation()
  const [schema, setSchema] = useState<ProtocolSchema>()
  const [listing, setListing] = useState<ProtocolListing>()
  const [error, setError] = useState('')
  const [selection, setSelection] = useState<{ draft?: ProtocolDraft; source?: string }>()
  const refresh = useCallback(async (id: string) => {
    const items = await protocolAPI.list()
    setListing(items)
    setSelection((current) => current && ({ ...current, draft: items.drafts.find((entry) => entry.protocolId === id) }))
  }, [])
  /** 预置只读：复制为派生 ID 的可编辑草稿（名称加副本后缀）。 */
  const copyPreset = useCallback(async (id: string) => {
    const revisions = await protocolAPI.revisions(id)
    const latest = revisions[revisions.length - 1]
    if (!latest) return
    const definition = JSON.parse(latest.definition) as { id: string; name?: string }
    const taken = new Set(listing?.drafts.map((draft) => draft.protocolId) ?? [])
    let candidate = `${id}-copy`
    for (let index = 1; taken.has(candidate); index += 1) candidate = `${id}-copy-${index}`
    definition.id = candidate
    definition.name = `${definition.name ?? id}（副本）`
    setSelection({ source: JSON.stringify(definition, null, 2) })
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
  const presetIDs = new Set(listing?.presets ?? [])
  const customDrafts = (listing?.drafts ?? []).filter((draft) => !presetIDs.has(draft.protocolId))
  return <div className="space-y-6">
    <PageHeader title="协议设计器" actions={!selection && <><Button variant="ghost" onClick={() => navigate('/protocols/legacy')}>历史配置</Button><Button onClick={() => navigate('/agent?mode=create')}>Agent 编写</Button><Button variant="primary" onClick={() => setSelection({})}>新建协议</Button></>} />
    {error && <p role="alert" className="text-destructive">{error}</p>}
    {!schema || !listing ? <p role="status">正在读取协议引擎契约…</p> : selection ? <ProtocolV2Editor schema={schema} draft={selection.draft} initialSource={selection.source} activeHash={listing.active.find((entry) => entry.protocolId === selection.draft?.protocolId)?.revisionHash ?? ''} onSaved={refresh} onClose={() => setSelection(undefined)} /> : <>
      {customDrafts.length === 0 ? <p>暂无版本化协议。新建协议开始编写，或从历史配置迁移。</p> : <div className="overflow-x-auto"><table className="w-full text-sm"><thead><tr className="border-b text-left"><th className="p-3">协议</th><th>草稿</th><th>启用状态</th><th>操作</th></tr></thead><tbody>{customDrafts.map((draft) => {
        const active = listing.active.find((entry) => entry.protocolId === draft.protocolId)
        return <tr key={draft.protocolId} className="border-b"><td className="p-3 font-mono">{draft.protocolId}</td><td className="font-mono text-xs">{draft.hash.slice(0, 12)}</td><td>{active ? listing.loaded[draft.protocolId] === active.revisionHash ? '已启用' : '需重新验证或修复' : '未启用'}</td><td><Button onClick={() => setSelection({ draft })}>编辑 {draft.protocolId}</Button></td></tr>
      })}</tbody></table></div>}
      {(listing.presets ?? []).length > 0 && <div className="space-y-2">
        <h2 className="text-sm font-medium">预置协议（只读，随版本自动更新）</h2>
        <div className="overflow-x-auto"><table className="w-full text-sm"><thead><tr className="border-b text-left"><th className="p-3">协议</th><th>启用修订</th><th>操作</th></tr></thead><tbody>{(listing.presets ?? []).map((id) => {
          const active = listing.active.find((entry) => entry.protocolId === id)
          return <tr key={id} className="border-b"><td className="p-3 font-mono">{id}</td><td className="font-mono text-xs">{active?.revisionHash.slice(0, 12) ?? '—'}</td><td><Button variant="ghost" onClick={() => void copyPreset(id)}>复制为新协议</Button></td></tr>
        })}</tbody></table></div>
      </div>}
    </>}
  </div>
}
