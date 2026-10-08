import { useEffect, useState } from 'react'
import { Button } from '@/components/ui/button'
import { protocolAPI, type ProtocolBindingEntry } from '@/lib/protocol-v2'
import { conversionAPI, type ConversionPolicy, type ConversionRecord } from '@/lib/conversion-policy'

/** Conversion overrides are saved with the existing contract, never inferred
 * from the model name or used to expand its manually declared capabilities. */
export function BindingConversionEditor({ sourceId, modelId, disabled }: { sourceId: string; modelId?: string; disabled?: boolean }) {
  const [entry, setEntry] = useState<ProtocolBindingEntry>()
  const [policies, setPolicies] = useState<ConversionRecord[]>([])
  const [revisions, setRevisions] = useState<ConversionRecord[]>([])
  const [selected, setSelected] = useState('')
  const [revision, setRevision] = useState('')
  const [override, setOverride] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  useEffect(() => {
    let current = true
    void Promise.all([protocolAPI.bindings(), conversionAPI.list()]).then(async ([bindings, items]) => {
      const own = bindings.find((b) => b.sourceId === sourceId && b.kind === (modelId ? 'model' : 'source') && (!modelId || b.modelId === modelId))
      if (!current) return
      setEntry(own); setPolicies(items); setSelected(own?.conversion?.policyId ?? ''); setRevision(own?.conversion?.revisionHash ?? ''); setOverride(own?.conversion?.overrides ? JSON.stringify(own.conversion.overrides, null, 2) : '')
      if (own?.conversion?.policyId) { const rs = await conversionAPI.revisions(own.conversion.policyId); if (current) setRevisions(rs) }
    }).catch((e: unknown) => { if (current) setError(String(e)) })
    return () => { current = false }
  }, [sourceId, modelId])
  const save = async () => {
    if (!entry) return
    setBusy(true); setError(''); setNotice('')
    try {
      const overrides = override.trim() ? JSON.parse(override) as ConversionPolicy : undefined
      if (selected && !revision) throw new Error('请选择一个已经验证的修订')
      const result = await protocolAPI.saveBinding({ ...entry, conversion: selected || overrides ? { policyId: selected, revisionHash: revision, overrides } : undefined })
      setEntry(result); setNotice('转换覆盖已验证并保存')
    } catch (e) { setError(String(e)) } finally { setBusy(false) }
  }
  return <details className="space-y-2 rounded-md border p-3"><summary>转换行为覆盖</summary>
    <p className="text-xs text-muted-foreground">{entry ? '覆盖仅影响此绑定；同 ID 规则覆盖上层，显式禁用可停止继承的规则。' : '先保存此层级的协议绑定，再配置转换覆盖。'} <a href="#/protocols/conversions" className="underline">管理转换策略</a></p>
    <select aria-label="绑定转换策略" className="w-full rounded border bg-card p-2 text-sm" disabled={disabled || busy || !entry} value={selected} onChange={(e) => { const id = e.target.value; setSelected(id); setRevision(''); setRevisions([]); if (id) void conversionAPI.revisions(id).then(setRevisions).catch((e: unknown) => setError(String(e))) }}><option value="">继承上层</option>{policies.map((p) => <option key={p.id} value={p.id}>{p.policy.name || p.id}</option>)}</select>
    {selected && <select aria-label="绑定策略修订" className="w-full rounded border bg-card p-2 text-sm" value={revision} disabled={disabled || busy} onChange={(e) => setRevision(e.target.value)}><option value="">选择不可变修订</option>{revisions.map((r) => <option key={r.hash} value={r.hash}>{r.hash.slice(0, 12)}</option>)}</select>}
    <label className="block text-xs">覆盖 JSON（可留空）<textarea aria-label="绑定转换覆盖 JSON" className="mt-1 h-28 w-full rounded border bg-card p-2 font-mono" value={override} disabled={disabled || busy || !entry} onChange={(e) => setOverride(e.target.value)} placeholder={'{"schemaVersion":1,"id":"local-override","name":"覆盖","rules":[]}'} /></label>
    {error && <p role="alert" className="text-xs text-destructive">{error}</p>}{notice && <p role="status" className="text-xs">{notice}</p>}
    <Button size="sm" disabled={disabled || busy || !entry} onClick={() => void save()}>{busy ? '验证中…' : '验证并保存转换覆盖'}</Button>
  </details>
}
