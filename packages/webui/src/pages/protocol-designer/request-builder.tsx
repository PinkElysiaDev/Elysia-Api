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
  return (
    <div className="space-y-1.5">
      {items.map(([key, value]) => (
        <div key={key} className="flex items-center gap-1.5">
          <Input
            className="h-7 flex-1 font-mono text-xs"
            value={key}
            placeholder={keyPlaceholder}
            onChange={(event) => {
              const next: Record<string, string> = {}
              for (const [k, v] of items) next[k === key ? event.target.value : k] = v
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
      ))}
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
}: {
  request: CustomProtocolRequest
  onChange: (next: CustomProtocolRequest) => void
  requestFields: MaheshvaraFieldSpec[]
}) {
  const auth = request.auth ?? {}
  const authMode = auth.mode || 'bearer'
  return (
    <div className="space-y-8">

      <SettingSection title="请求体" description="从零构造发往上游的 JSON，每个字段声明对应 Maheshvara 的哪个字段">
        <RequestBodyTreeEditor
          value={request.body}
          onChange={(body) => onChange({ ...request, body })}
          fields={requestFields}
        />
      </SettingSection>
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

    </div>
  )
}
