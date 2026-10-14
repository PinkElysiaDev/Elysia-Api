import { BindingConversionEditor } from './binding-conversion'
import { useEffect, useRef, useState } from 'react'
import { useSWRConfig } from 'swr'
import { Button } from '@/components/ui/button'
import { protocolAPI, type EnabledProtocol, type ProtocolBindingEntry } from '@/lib/protocol-v2'
import type { Model } from '@/lib/types'

/** Explicit model binding remains independent of source selection and catalog refresh. */
export function ModelProtocolBinding(props: { model: Model; disabled: boolean }) {
  return <ModelBindingForm key={JSON.stringify([props.model.sourceId, props.model.id])} {...props} />
}

function ModelBindingForm({ model, disabled }: { model: Model; disabled: boolean }) {
  const [protocols, setProtocols] = useState<EnabledProtocol[]>([])
  const [current, setCurrent] = useState<ProtocolBindingEntry>()
  const [selected, setSelected] = useState('')
  const [loaded, setLoaded] = useState(false)
  const [busy, setBusy] = useState(false)
  const [conversionBusy, setConversionBusy] = useState(false)
  const [conversionKey, setConversionKey] = useState(0)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const { mutate } = useSWRConfig()
  const generation = useRef(0)
  useEffect(() => {
    const requestId = ++generation.current
    void Promise.all([protocolAPI.enabled(), protocolAPI.bindings()]).then(([items, bindings]) => {
      if (generation.current !== requestId) return
      const entry = bindings.find((entry) => entry.kind === 'model' && entry.sourceId === (model.sourceId ?? '') && entry.modelId === model.id)
        ?? bindings.find((entry) => entry.kind === 'source' && entry.sourceId === (model.sourceId ?? ''))
      setProtocols(items.filter((item) => item.canGenerate)); setCurrent(entry)
      setSelected(entry?.unbound ? '' : entry?.binding.protocolId ?? ''); setLoaded(true)
    }).catch((err: unknown) => { if (generation.current === requestId) setError(String(err)) })
    return () => { generation.current = requestId + 1 }
  }, [model.id, model.sourceId])
  const save = async () => {
    if (!loaded || disabled || busy || conversionBusy) return
    const requestId = generation.current
    setBusy(true); setError(''); setNotice('')
    try {
      const entry: ProtocolBindingEntry = { kind: 'model', sourceId: model.sourceId ?? '', modelId: model.id, unbound: !selected, binding: {}, conversion: current?.kind === 'model' ? current.conversion : undefined }
      if (selected && current?.binding.protocolId === selected && !current.unbound) {
        // Saving the selection is not consent to widen a manually restricted contract.
        entry.binding = { ...current.binding }
      } else if (selected) {
        const target = protocols.find((item) => item.id === selected)
        if (!target) throw new Error('请选择当前已启用的协议')
        const revision = (await protocolAPI.revisions(target.id)).find((item) => item.hash === target.revision)
        if (!revision) throw new Error('协议已变化，请重新打开模型设置')
        const definition = JSON.parse(revision.definition) as { operations: Record<string, { kind: string; transport: string }> }
        const capabilities = Object.fromEntries(Object.entries(target.capabilities).filter(([key, supported]) => supported && (model.toolsCapable || !key.startsWith('tools.')) && (model.visionCapable || !['images', 'audio', 'video'].includes(key))))
        entry.binding = { protocolId: target.id, revisionHash: target.revision, capabilities, transports: [...new Set(Object.values(definition.operations).filter((operation) => operation.kind === 'generate').map((operation) => operation.transport))] }
      }
      if (generation.current !== requestId) return
      const saved = await protocolAPI.saveBinding(entry)
      if (generation.current !== requestId) return
      setCurrent(saved)
      setConversionKey((key) => key + 1)
      setNotice(selected ? '模型级绑定已验证并保存' : '已明确取消绑定，不会回退到模型源协议')
      await mutate((key) => typeof key === 'string' && (key.includes('models') || key === 'agent-model-readiness'))
    } catch (err) { if (generation.current === requestId) setError(String(err)) } finally { if (generation.current === requestId) setBusy(false) }
  }
  return <section className="space-y-2 rounded-md border p-3">
    <p className="text-sm font-medium">协议绑定</p>
    <p className="text-xs text-muted-foreground">{!loaded ? '正在检查绑定…' : current?.unbound || !current ? '未绑定协议' : `${current.kind === 'model' ? '模型级' : '继承模型源'} · ${current.binding.protocolId}`}</p>
    <select aria-label="模型协议" className="w-full rounded-md border bg-card p-2 text-sm" value={selected} disabled={!loaded || disabled || busy || conversionBusy} onChange={(event) => setSelected(event.target.value)}>
      <option value="">未绑定协议</option>
      {protocols.map((item) => <option key={item.id} value={item.id}>{item.name || item.id}</option>)}
    </select>
    <p className="text-xs text-muted-foreground">单独保存此模型的协议；模型源设置和目录刷新不会覆盖。能力以当前已保存的模型设置为准。</p>
    {error && <p role="alert" className="text-xs text-destructive">{error}</p>}
    {notice && <p role="status" className="text-xs">{notice}</p>}
    <Button size="sm" disabled={!loaded || disabled || busy || conversionBusy} onClick={() => void save()}>{busy ? '验证中…' : '验证并保存模型绑定'}</Button>
    {loaded && <BindingConversionEditor key={conversionKey} sourceId={model.sourceId ?? ''} modelId={model.id} disabled={disabled || busy} onSaved={setCurrent} onBusyChange={setConversionBusy} />}
  </section>
}
