import { useEffect, useRef, useState } from 'react'
import { Button } from '@/components/ui/button'
import { protocolAPI, type ProtocolBindingEntry, type ReasoningCapabilities } from '@/lib/protocol-v2'
import { conversionAPI, type ConversionRecord } from '@/lib/conversion-policy'

/** Conversion overrides are saved with the existing contract, never inferred
 * from the model name or used to expand its manually declared capabilities. */
type BindingConversionProps = { sourceId: string; modelId?: string; disabled?: boolean; onSaved?: (entry: ProtocolBindingEntry) => void; onBusyChange?: (busy: boolean) => void }

export function BindingConversionEditor(props: BindingConversionProps) {
  return <BindingConversionForm key={JSON.stringify([props.sourceId, props.modelId])} {...props} />
}

function BindingConversionForm({ sourceId, modelId, disabled, onSaved, onBusyChange }: BindingConversionProps) {
  const [entry, setEntry] = useState<ProtocolBindingEntry>()
  const [policies, setPolicies] = useState<ConversionRecord[]>([])
  const [revisions, setRevisions] = useState<ConversionRecord[]>([])
  const [selected, setSelected] = useState('')
  const [revision, setRevision] = useState('')
  const [override, setOverride] = useState('')
  const [reasoning, setReasoning] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const generation = useRef(0)
  const revisionGeneration = useRef(0)
  const busyCallback = useRef(onBusyChange)
  busyCallback.current = onBusyChange
  useEffect(() => {
    const requestId = ++generation.current
    void Promise.all([protocolAPI.bindings(), conversionAPI.list()]).then(([bindings, items]) => {
      if (generation.current !== requestId) return
      const own = bindings.find((binding) => binding.sourceId === sourceId && binding.kind === (modelId ? 'model' : 'source') && (!modelId || binding.modelId === modelId))
      setEntry(own); setReasoning(own?.binding.reasoning ? JSON.stringify(own.binding.reasoning, null, 2) : ''); setPolicies(items); setSelected(own?.conversion?.policyId ?? ''); setRevision(own?.conversion?.revisionHash ?? ''); setOverride(own?.conversion?.overridesJSON ?? '')
    }).catch((error: unknown) => { if (generation.current === requestId) setError(String(error)) })
    return () => { generation.current = requestId + 1; busyCallback.current?.(false) }
  }, [sourceId, modelId])
  useEffect(() => {
    const requestId = ++revisionGeneration.current
    setRevisions([])
    if (selected) void conversionAPI.revisions(selected).then((items) => {
      if (revisionGeneration.current === requestId) setRevisions(items)
    }).catch((error: unknown) => { if (revisionGeneration.current === requestId) setError(String(error)) })
    return () => { revisionGeneration.current = requestId + 1 }
  }, [selected])
  const save = async () => {
    if (!entry || disabled || busy) return
    const requestId = generation.current
    setBusy(true); onBusyChange?.(true); setError(''); setNotice('')
    try {
      const overridesJSON = override.trim() || undefined
      if (selected && !revision) throw new Error('请选择一个已经验证的修订')
      const result = await protocolAPI.saveBinding({ ...entry, binding: { ...entry.binding, reasoning: reasoning.trim() ? JSON.parse(reasoning) as ReasoningCapabilities : undefined }, conversion: selected || overridesJSON ? { policyId: selected, revisionHash: revision, overridesJSON } : undefined })
      if (generation.current !== requestId) return
      setEntry(result); onSaved?.(result); setNotice('转换覆盖已验证并保存')
    } catch (error) {
      if (generation.current === requestId) setError(String(error))
    } finally {
      if (generation.current === requestId) { setBusy(false); onBusyChange?.(false) }
    }
  }
  return <details className="space-y-2 rounded-md border p-3"><summary>转换行为覆盖</summary>
    <p className="text-xs text-muted-foreground">{entry ? '覆盖仅影响此绑定；同 ID 规则覆盖上层，显式禁用可停止继承的规则。' : '先保存此层级的协议绑定，再配置转换覆盖。'} <a href="#/protocols/conversions" className="underline">管理转换策略</a></p>
    <select aria-label="绑定转换策略" className="w-full rounded border bg-card p-2 text-sm" disabled={disabled || busy || !entry} value={selected} onChange={(e) => { const id = e.target.value; setSelected(id); setRevision(''); setRevisions([]); revisionGeneration.current++ }}><option value="">继承上层</option>{policies.map((p) => <option key={p.id} value={p.id}>{p.policy.name || p.id}</option>)}</select>
    {selected && <select aria-label="绑定策略修订" className="w-full rounded border bg-card p-2 text-sm" value={revision} disabled={disabled || busy} onChange={(e) => setRevision(e.target.value)}><option value="">选择不可变修订</option>{revisions.map((r) => <option key={r.hash} value={r.hash}>{r.hash.slice(0, 12)}</option>)}</select>}
    <label className="block text-xs">覆盖 JSON（可留空）<textarea aria-label="绑定转换覆盖 JSON" className="mt-1 h-28 w-full rounded border bg-card p-2 font-mono" value={override} disabled={disabled || busy || !entry} onChange={(e) => setOverride(e.target.value)} placeholder={'{"schemaVersion":1,"id":"local-override","name":"覆盖","rules":[]}'} /></label>
    <label className="block text-xs">模型思考能力 JSON（可留空）<textarea aria-label="模型思考能力 JSON" className="mt-1 h-28 w-full rounded border bg-card p-2 font-mono" value={reasoning} disabled={disabled || busy || !entry} onChange={(e) => setReasoning(e.target.value)} /></label>
    <p className="text-xs text-muted-foreground">留空使用模型系列规则；填写时完整替换思考能力。maxOutputTokens 是已确认的输出硬上限，不是上下文窗口。保存后重新验证此绑定。</p>
    {error && <p role="alert" className="text-xs text-destructive">{error}</p>}{notice && <p role="status" className="text-xs">{notice}</p>}
    <Button size="sm" disabled={disabled || busy || !entry} onClick={() => void save()}>{busy ? '验证中…' : '验证并保存转换覆盖'}</Button>
  </details>
}
