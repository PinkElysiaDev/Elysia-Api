import { useState } from 'react'
import { Plus } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { ProtocolDocument } from '@/lib/protocol-document'
import type { ProtocolSchema } from '@/lib/protocol-v2'

const selectClass =
  'h-[34px] w-full rounded-md border border-input bg-card px-2.5 text-sm text-foreground transition-[border-color,box-shadow] duration-200 focus-visible:outline-none focus-visible:border-rose focus-visible:ring-[3px] focus-visible:ring-wash'

/** Form edits replace individual spans and retain session/task extensions. */
export function ProtocolOperationEditor({
  document,
  schema,
  onChange,
}: {
  document: ProtocolDocument
  schema: ProtocolSchema
  onChange: (source: string) => void
}) {
  const [name, setName] = useState('')
  const [selected, setSelected] = useState('')
  const names = [...(document.locate('/operations')?.children.keys() ?? [])]
  const pointer = `/operations/${selected.replace(/~/g, '~0').replace(/\//g, '~1')}`
  const read = (key: string) => {
    const raw = document.read(pointer + '/' + key)
    const value: unknown = raw ? JSON.parse(raw) : ''
    return typeof value === 'string' ? value : ''
  }
  return (
    <div className="space-y-4 border-b border-border/40 pb-4">
      <div className="flex flex-wrap items-end gap-3">
        <label className="space-y-1 text-xs font-medium text-foreground">
          <span>操作名称</span>
          <Input
            aria-label="新增操作名称"
            className="w-56 font-mono text-xs"
            placeholder="如 generate / models"
            value={name}
            onChange={(event) => setName(event.target.value)}
          />
        </label>
        <Button
          disabled={!/^[a-zA-Z0-9][a-zA-Z0-9._-]{0,127}$/.test(name) || names.includes(name)}
          onClick={() => {
            onChange(
              document.set(
                `/operations/${name}`,
                JSON.stringify(
                  {
                    kind: 'generate',
                    method: 'POST',
                    path: '/',
                    transport: 'http_json',
                    auth: { location: 'none' },
                  },
                  null,
                  2,
                ),
              ),
            )
            setSelected(name)
            setName('')
          }}
        >
          <Plus className="h-3.5 w-3.5" aria-hidden />
          添加操作
        </Button>
        <label className="space-y-1 text-xs font-medium text-foreground">
          <span className="block">编辑操作</span>
          <select
            aria-label="编辑操作"
            className={selectClass}
            value={selected}
            onChange={(event) => setSelected(event.target.value)}
          >
            <option value="">请选择</option>
            {names.map((value) => (
              <option key={value}>{value}</option>
            ))}
          </select>
        </label>
      </div>

      {names.includes(selected) && (
        <div className="grid gap-3 border-t border-border/50 pt-3 md:grid-cols-2 lg:grid-cols-4">
          <label className="space-y-1 text-xs font-medium text-foreground">
            <span className="block">操作类型</span>
            <select
              aria-label="操作类型"
              className={selectClass}
              value={read('kind')}
              onChange={(event) =>
                onChange(document.set(pointer + '/kind', JSON.stringify(event.target.value)))
              }
            >
              {schema.operationKinds.map((kind) => (
                <option key={kind}>{kind}</option>
              ))}
            </select>
          </label>
          <label className="space-y-1 text-xs font-medium text-foreground">
            <span className="block">传输方式</span>
            <select
              aria-label="传输方式"
              className={selectClass}
              value={read('transport')}
              onChange={(event) =>
                onChange(document.set(pointer + '/transport', JSON.stringify(event.target.value)))
              }
            >
              {schema.transports.map((transport) => (
                <option key={transport}>{transport}</option>
              ))}
            </select>
          </label>
          <label className="space-y-1 text-xs font-medium text-foreground">
            <span className="block">HTTP 方法</span>
            <select
              aria-label="HTTP 方法"
              className={selectClass}
              value={read('method')}
              onChange={(event) =>
                onChange(document.set(pointer + '/method', JSON.stringify(event.target.value)))
              }
            >
              {['GET', 'POST', 'PUT', 'PATCH', 'DELETE'].map((method) => (
                <option key={method}>{method}</option>
              ))}
            </select>
          </label>
          <label className="space-y-1 text-xs font-medium text-foreground">
            <span>操作路径</span>
            <Input
              aria-label="操作路径"
              className="font-mono text-xs"
              value={read('path')}
              onChange={(event) =>
                onChange(document.set(pointer + '/path', JSON.stringify(event.target.value)))
              }
            />
          </label>
        </div>
      )}
    </div>
  )
}

/** Directions remain independently authored; module choices come from schema. */
export function ProtocolDirectionEditor({
  document,
  schema,
  onChange,
}: {
  document: ProtocolDocument
  schema: ProtocolSchema
  onChange: (source: string) => void
}) {
  const [direction, setDirection] = useState('decode_request')
  const [module, setModule] = useState('')
  const exists = !!document.locate('/directions/' + direction)
  return (
    <div className="flex flex-wrap items-end gap-3 border-b border-border/40 pb-4">
      <label className="space-y-1 text-xs font-medium text-foreground">
        <span className="block">新增方向</span>
        <select
          className={selectClass}
          value={direction}
          onChange={(event) => {
            setDirection(event.target.value)
            setModule('')
          }}
        >
          {schema.directions.map((name) => (
            <option key={name}>{name}</option>
          ))}
        </select>
      </label>
      <label className="space-y-1 text-xs font-medium text-foreground">
        <span className="block">实现方式</span>
        <select
          className={selectClass}
          value={module}
          onChange={(event) => setModule(event.target.value)}
        >
          <option value="">从零编写映射</option>
          {schema.modules
            .filter((item) => item.directions.includes(direction))
            .map((item) => (
              <option key={item.name}>{item.name}</option>
            ))}
        </select>
      </label>
      <Button
        disabled={exists}
        onClick={() =>
          onChange(
            document.set(
              '/directions/' + direction,
              JSON.stringify(
                module ? { module } : { transform: { op: 'object', fields: {} } },
                null,
                2,
              ),
            ),
          )
        }
      >
        <Plus className="h-3.5 w-3.5" aria-hidden />
        添加方向
      </Button>
    </div>
  )
}
