import { useState } from 'react'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { ProtocolDocument } from '@/lib/protocol-document'
import type { ProtocolSchema } from '@/lib/protocol-v2'

/** Form edits replace individual spans and retain session/task extensions. */
export function ProtocolOperationEditor({ document, schema, onChange }: { document: ProtocolDocument; schema: ProtocolSchema; onChange: (source: string) => void }) {
  const [name, setName] = useState('')
  const [selected, setSelected] = useState('')
  const names = [...(document.locate('/operations')?.children.keys() ?? [])]
  const pointer = `/operations/${selected.replace(/~/g, '~0').replace(/\//g, '~1')}`
  const read = (key: string) => { const raw = document.read(pointer + '/' + key); const value = raw && JSON.parse(raw); return typeof value === 'string' ? value : '' }
  const set = (key: string, value: string) => onChange(document.set(pointer + '/' + key, JSON.stringify(value)))
  return <div className="space-y-3 rounded border p-3">
    <div className="flex flex-wrap gap-2"><label className="text-sm">操作名称<Input aria-label="新增操作名称" value={name} onChange={(event) => setName(event.target.value)} /></label><Button disabled={!/^[a-zA-Z0-9][a-zA-Z0-9._-]{0,127}$/.test(name) || names.includes(name)} onClick={() => { onChange(document.set(`/operations/${name}`, JSON.stringify({ kind: 'generate', method: 'POST', path: '/', transport: 'http_json', auth: { location: 'none' } }, null, 2))); setSelected(name); setName('') }}>添加操作</Button></div>
    <label className="block text-sm">编辑操作 <select aria-label="编辑操作" className="rounded border bg-card p-2" value={selected} onChange={(event) => setSelected(event.target.value)}><option value="">请选择</option>{names.map((value) => <option key={value}>{value}</option>)}</select></label>
    {names.includes(selected) && <div className="grid gap-3 md:grid-cols-2">
      <label className="text-sm">操作类型<select aria-label="操作类型" className="block w-full rounded border bg-card p-2" value={read('kind')} onChange={(event) => set('kind', event.target.value)}>{schema.operationKinds.map((kind) => <option key={kind}>{kind}</option>)}</select></label>
      <label className="text-sm">传输方式<select aria-label="传输方式" className="block w-full rounded border bg-card p-2" value={read('transport')} onChange={(event) => set('transport', event.target.value)}>{schema.transports.map((transport) => <option key={transport}>{transport}</option>)}</select></label>
      <label className="text-sm">HTTP 方法<select aria-label="HTTP 方法" className="block w-full rounded border bg-card p-2" value={read('method')} onChange={(event) => set('method', event.target.value)}>{['GET', 'POST', 'PUT', 'PATCH', 'DELETE'].map((method) => <option key={method}>{method}</option>)}</select></label>
      <label className="text-sm">操作路径<Input aria-label="操作路径" value={read('path')} onChange={(event) => set('path', event.target.value)} /></label>
    </div>}
  </div>
}

/** Directions remain independently authored; module choices come from schema. */
export function ProtocolDirectionEditor({ document, schema, onChange }: { document: ProtocolDocument; schema: ProtocolSchema; onChange: (source: string) => void }) {
  const [direction, setDirection] = useState('decode_request')
  const [module, setModule] = useState('')
  const exists = !!document.locate('/directions/' + direction)
  return <div className="flex flex-wrap items-end gap-3 rounded border p-3">
    <label className="text-sm">新增方向<select className="block rounded border bg-card p-2" value={direction} onChange={(event) => { setDirection(event.target.value); setModule('') }}>{schema.directions.map((name) => <option key={name}>{name}</option>)}</select></label>
    <label className="text-sm">实现方式<select className="block rounded border bg-card p-2" value={module} onChange={(event) => setModule(event.target.value)}><option value="">从零编写映射</option>{schema.modules.filter((item) => item.directions.includes(direction)).map((item) => <option key={item.name}>{item.name}</option>)}</select></label>
    <Button disabled={exists} onClick={() => onChange(document.set('/directions/' + direction, JSON.stringify(module ? { module } : { transform: { op: 'object', fields: {} } }, null, 2)))}>添加方向</Button>
  </div>
}
