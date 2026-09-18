import { useRef, useState } from 'react'
import { Plus, Trash2 } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { SettingRow, SettingSection } from '@/components/ui/setting-card'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import type { CustomProtocolRequest, MaheshvaraFieldSpec } from '@/lib/types'
import { RequestBodyTreeEditor } from './body-tree-editor'
import { AUTH_MODES } from './schema'

const METHODS = ['POST', 'GET', 'PUT', 'PATCH', 'DELETE']

/** 键值对编辑器（headers / query）。 */
function KeyValueEditor({
  title,
  entries,
  onChange,
  keyPlaceholder,
  valuePlaceholder,
}: {
  title: string
  entries: Record<string, string>
  onChange: (next: Record<string, string>) => void
  keyPlaceholder: string
  valuePlaceholder: string
}) {
  const items = Object.entries(entries)
  // 行 key 用自增行号:键名即数据身份,onChange 即改 key 会让整行每敲一键
  // 重挂载、输入框失焦;键名经 RowKeyInput 本地缓冲、blur/Enter 提交。
  const nextRowId = useRef(0)
  const rowIds = useRef(new Map<string, number>())
  return (
    <div className="space-y-1.5">
      {items.map(([key, value]) => {
        let rowId = rowIds.current.get(key)
        if (rowId === undefined) {
          rowId = nextRowId.current++
          rowIds.current.set(key, rowId)
        }
        return (
        <div key={rowId} className="flex items-center gap-1.5">
          <RowKeyInput
            rowKey={key}
            placeholder={keyPlaceholder}
            exists={(candidate) => items.some(([k]) => k === candidate)}
            onCommit={(newKey) => {
              if (newKey === key || newKey === '') return
              const next: Record<string, string> = {}
              for (const [k, v] of items) next[k === key ? newKey : k] = v
              onChange(next)
            }}
          />
          <Input
            className="h-7 flex-1 font-mono text-xs"
            value={value}
            placeholder={valuePlaceholder}
            onChange={(event) => onChange({ ...entries, [key]: event.target.value })}
          />
          <Button
            type="button"
            variant="ghost"
            size="iconSm"
            aria-label={`删除${title}`}
            onClick={() => {
              const next = { ...entries }
              delete next[key]
              onChange(next)
            }}
          >
            <Trash2 className="h-3.5 w-3.5" />
          </Button>
        </div>
        )
      })}
      <Button type="button" variant="outline" size="sm" onClick={() => onChange({ ...entries, '': '' })}>
        <Plus className="mr-1 h-3 w-3" /> 添加{title}
      </Button>
    </div>
  )
}

export function RequestBuilder({
  request,
  onChange,
  requestFields,
  mappingMode = 'inline',
}: {
  request: CustomProtocolRequest
  onChange: (next: CustomProtocolRequest) => void
  requestFields: MaheshvaraFieldSpec[]
  /** badge:映射位只读徽标,分配集中在「映射关系」页签。 */
  mappingMode?: 'inline' | 'badge'
}) {
  const auth = request.auth ?? {}
  const authMode = auth.mode || 'bearer'
  return (
    <div className="space-y-8">

      <SettingSection title="HTTP 请求">
        <SettingRow label="Method" description="默认 POST；GET/DELETE 可无请求体">
          <Select value={request.method || 'POST'} onValueChange={(value) => onChange({ ...request, method: value })}>
            <SelectTrigger className="w-28" aria-label="HTTP 方法">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {METHODS.map((method) => (
                <SelectItem key={method} value={method}>
                  {method}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </SettingRow>
        <SettingRow label="Path" description="相对 baseUrl，支持 {{maheshvara.*}} 插值">
          <Input
            className="font-mono text-xs sm:max-w-md"
            value={request.path ?? ''}
            placeholder="/chat/completions"
            onChange={(event) => onChange({ ...request, path: event.target.value })}
          />
        </SettingRow>
        <SettingRow label="流式 Path" description="流式请求的路径覆盖（如 Gemini :streamGenerateContent?alt=sse）；留空同 Path">
          <Input
            className="font-mono text-xs sm:max-w-md"
            value={request.pathStream ?? ''}
            placeholder="留空 = 同 Path"
            onChange={(event) => onChange({ ...request, pathStream: event.target.value })}
          />
        </SettingRow>
        <SettingRow label="Content-Type">
          <Input
            className="font-mono text-xs sm:max-w-md"
            value={request.contentType ?? ''}
            placeholder="application/json"
            onChange={(event) => onChange({ ...request, contentType: event.target.value })}
          />
        </SettingRow>
        <SettingRow label="Headers" description="认证头须在 Auth 中配置" inline={false}>
          <KeyValueEditor
            title="Header"
            entries={request.headers ?? {}}
            onChange={(headers) => onChange({ ...request, headers })}
            keyPlaceholder="X-Custom-Header"
            valuePlaceholder='{{maheshvara.model}} 或固定值'
          />
        </SettingRow>
        <SettingRow label="Query 参数" inline={false}>
          <KeyValueEditor
            title="Query 参数"
            entries={request.query ?? {}}
            onChange={(query) => onChange({ ...request, query })}
            keyPlaceholder="api-version"
            valuePlaceholder="2026-01-01"
          />
        </SettingRow>
      </SettingSection>

      <SettingSection title="认证（Auth）">
        <SettingRow label="模式">
          <Select
            value={authMode}
            onValueChange={(value) =>
              onChange({ ...request, auth: { ...auth, mode: value === 'bearer' ? '' : value } })
            }
          >
            <SelectTrigger className="w-44" aria-label="认证模式">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {AUTH_MODES.map((mode) => (
                <SelectItem key={mode.value} value={mode.value}>
                  {mode.label}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </SettingRow>
        {authMode === 'header' && (
          <>
            <SettingRow label="Header 名称" description="默认 x-api-key">
              <Input
                className="font-mono text-xs sm:max-w-xs"
                value={auth.header ?? ''}
                placeholder="x-api-key"
                onChange={(event) => onChange({ ...request, auth: { ...auth, header: event.target.value } })}
              />
            </SettingRow>
            <SettingRow label="前缀（prefix）">
              <Input
                className="font-mono text-xs sm:max-w-xs"
                value={auth.prefix ?? ''}
                onChange={(event) => onChange({ ...request, auth: { ...auth, prefix: event.target.value } })}
              />
            </SettingRow>
          </>
        )}
        {authMode === 'query' && (
          <SettingRow label="Query 参数名">
            <Input
              className="font-mono text-xs sm:max-w-xs"
              value={auth.query ?? ''}
              placeholder="api_key"
              onChange={(event) => onChange({ ...request, auth: { ...auth, query: event.target.value } })}
            />
          </SettingRow>
        )}
      </SettingSection>

      <SettingSection
        title="请求体结构"
        description="发往上游的 JSON 结构；映射位的字段分配集中在「映射关系」页签"
      >
        <RequestBodyTreeEditor
          value={request.body}
          onChange={(body) => onChange({ ...request, body })}
          fields={requestFields}
          mappingMode={mappingMode}
        />
      </SettingSection>

    </div>
  )
}

// RowKeyInput:键名本地缓冲编辑,blur/Enter 提交;撞车(目标键已存在)静默
// 回退——与 structure-tree 的 KeyInput 同一模式,适配 Record 键值对形态。
function RowKeyInput({
  rowKey,
  placeholder,
  exists,
  onCommit,
}: {
  rowKey: string
  placeholder: string
  exists: (candidate: string) => boolean
  onCommit: (next: string) => void
}) {
  const [buffer, setBuffer] = useState(rowKey)
  const [conflict, setConflict] = useState(false)
  const commit = () => {
    const next = buffer.trim()
    if (next === rowKey || next === '' || exists(next)) {
      if (next && exists(next)) setConflict(true)
      setBuffer(rowKey)
      return
    }
    setConflict(false)
    onCommit(next)
  }
  return (
    <Input
      className="h-7 flex-1 font-mono text-xs"
      value={buffer}
      placeholder={placeholder}
      aria-invalid={conflict || undefined}
      title={conflict ? '该键名已存在' : undefined}
      onChange={(event) => { setConflict(false); setBuffer(event.target.value) }}
      onBlur={commit}
      onKeyDown={(event) => {
        if (event.key === 'Enter' || event.key === 'Escape') {
          event.preventDefault()
          commit()
        }
      }}
    />
  )
}
