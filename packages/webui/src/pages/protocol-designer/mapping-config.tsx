import { ArrowRight, ArrowRightLeft, Plus, Trash2 } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Seg } from '@/components/ui/seg'
import { SettingSection } from '@/components/ui/setting-card'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Switch } from '@/components/ui/switch'
import type {
  CustomProtocolBodyTree,
  CustomProtocolConfig,
  CustomProtocolResponseFieldMapping,
  CustomProtocolResponseBodyTree,
  CustomProtocolSchema,
} from '@/lib/types'
import { collectRequestMappings, collectResponseMappings } from './tree-utils'

/**
 * 映射配置卡：构造出的请求体 / 返回体对大自在天字段的映射集中在此配置。
 * 每行 = 树内路径 → Maheshvara 字段（下拉 + 通俗解释），请求侧附
 * mode / default / omitIfEmpty，响应侧附 transform。
 */

function FieldSelect({
  value,
  onChange,
  fields,
  datalistId,
}: {
  value: string
  onChange: (next: string) => void
  fields: { name: string; label: string }[]
  datalistId: string
}) {
  const known = fields.some((field) => field.name === value)
  const spec = fields.find((field) => field.name === value)
  return (
    <div className="flex min-w-0 flex-1 flex-col gap-0.5">
      {known ? (
        <Select value={value} onValueChange={onChange}>
          <SelectTrigger className="h-7 w-full min-w-40 font-mono text-xs" aria-label="Maheshvara 字段">
            <SelectValue />
          </SelectTrigger>
          <SelectContent className="max-h-72">
            {fields.map((field) => (
              <SelectItem key={field.name} value={field.name} className="font-mono text-xs">
                {field.name}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      ) : (
        <Input
          list={datalistId}
          className="h-7 w-full min-w-40 font-mono text-xs"
          placeholder="选择或输入字段"
          value={value}
          onChange={(event) => onChange(event.target.value.trim())}
        />
      )}
      {spec ? (
        <span className="text-2xs text-muted-foreground">{spec.label}</span>
      ) : (
        value !== '' && <span className="text-2xs text-amber-600 dark:text-amber-400">未知字段，保存时将被拒绝</span>
      )}
    </div>
  )
}

export function MappingConfig({
  draft,
  onChange,
  schema,
}: {
  draft: CustomProtocolConfig
  onChange: (next: CustomProtocolConfig) => void
  schema: CustomProtocolSchema | undefined
}) {
  const requestFields = schema?.requestFields ?? []
  const responseFields = schema?.responseFields ?? []
  const transforms = schema?.transforms ?? []

  const requestSlots = collectRequestMappings(draft.request.body, (next) =>
    onChange({ ...draft, request: { ...draft.request, body: next as CustomProtocolBodyTree } }),
  )

  const response = draft.response ?? {}
  const usesBodyTree = response.body !== undefined && response.body !== null

  // 两种形态归一为同一槽位接口：树槽位就地更新叶子，行表槽位更新行。
  interface ResponseSlot {
    path: string
    field: string
    transform: string
    setField: (field: string) => void
    setTransform: (transform: string) => void
    remove?: () => void
  }
  const responseSlots: ResponseSlot[] = usesBodyTree
    ? collectResponseMappings(response.body as CustomProtocolResponseBodyTree, (next) =>
        onChange({ ...draft, response: { ...response, body: next as CustomProtocolResponseBodyTree } }),
      ).map((slot) => ({
        path: slot.path,
        field: slot.leaf.field ?? '',
        transform: slot.leaf.transform ?? '',
        setField: (field) => slot.setLeaf({ ...slot.leaf, field }),
        setTransform: (transform) => slot.setLeaf({ ...slot.leaf, transform }),
      }))
    : (response.fields ?? []).map((field, index) => {
        const update = (patch: Partial<CustomProtocolResponseFieldMapping>) => {
          const rows = (response.fields ?? []).slice()
          rows[index] = { ...rows[index], ...patch }
          onChange({ ...draft, response: { ...response, fields: rows } })
        }
        return {
          path: field.path,
          field: field.field,
          transform: field.transform ?? '',
          setField: (next: string) => update({ field: next }),
          setTransform: (next: string) => update({ transform: next }),
          remove: () =>
            onChange({
              ...draft,
              response: { ...response, fields: (response.fields ?? []).filter((_, i) => i !== index) },
            }),
        }
      })

  return (
    <div className="space-y-8">
      <datalist id="maheshvara-request-fields-dl">
        {requestFields.map((field) => (
          <option key={field.name} value={field.name}>
            {field.label}
          </option>
        ))}
      </datalist>
      <datalist id="maheshvara-response-fields-dl">
        {responseFields.map((field) => (
          <option key={field.name} value={field.name}>
            {field.label}
          </option>
        ))}
      </datalist>

      <SettingSection
        icon={ArrowRight}
        title="请求体映射"
        description="构造树中的每个映射位 → 大自在天请求字段"
      >
        {requestSlots.length === 0 ? (
          <p className="text-xs text-muted-foreground">请求体中还没有映射位。在「请求体」标签页添加映射位后再来这里配置字段。</p>
        ) : (
          <div className="space-y-2">
            {requestSlots.map((slot, index) => (
              <div
                key={`${slot.path}:${index}`}
                className="flex flex-wrap items-start gap-2 rounded-md border border-border/60 bg-background/40 p-2.5"
              >
                <span className="w-40 shrink-0 pt-1 font-mono text-xs text-muted-foreground">{slot.path}</span>
                <span className="pt-1 text-xs text-muted-foreground">→</span>
                <FieldSelect
                  value={slot.leaf.field}
                  onChange={(field) => slot.setLeaf({ ...slot.leaf, field })}
                  fields={requestFields}
                  datalistId="maheshvara-request-fields-dl"
                />
                <Seg
                  size="sm"
                  value={slot.leaf.mode ?? 'json'}
                  options={[
                    { value: 'json', label: '原生 JSON' },
                    { value: 'string', label: '字符串' },
                  ]}
                  onChange={(mode) => slot.setLeaf({ ...slot.leaf, mode: mode as 'json' | 'string' })}
                />
                <Input
                  className="h-7 w-28 font-mono text-xs"
                  placeholder="default 可选"
                  value={slot.leaf.default === undefined ? '' : JSON.stringify(slot.leaf.default)}
                  onChange={(event) => {
                    const text = event.target.value.trim()
                    if (!text) {
                      slot.setLeaf({ ...slot.leaf, default: undefined })
                      return
                    }
                    try {
                      slot.setLeaf({ ...slot.leaf, default: JSON.parse(text) })
                    } catch {
                      slot.setLeaf({ ...slot.leaf, default: text })
                    }
                  }}
                />
                <label className="flex items-center gap-1 pt-1 text-2xs text-muted-foreground">
                  <Switch
                    checked={!!slot.leaf.omitIfEmpty}
                    onCheckedChange={(checked) => slot.setLeaf({ ...slot.leaf, omitIfEmpty: checked })}
                  />
                  空值省略
                </label>
              </div>
            ))}
          </div>
        )}
      </SettingSection>

      <SettingSection
        icon={ArrowRightLeft}
        title="返回体映射"
        description={usesBodyTree ? '构造树中的每个映射位 ← 大自在天响应字段' : '映射行表（可返回「返回体」标签页转为构造树）'}
      >
        {responseSlots.length === 0 ? (
          <p className="text-xs text-muted-foreground">还没有响应映射。</p>
        ) : (
          <div className="space-y-2">
            {responseSlots.map((slot, index) => (
              <div
                key={`${slot.path}:${index}`}
                className="flex flex-wrap items-start gap-2 rounded-md border border-border/60 bg-background/40 p-2.5"
              >
                {usesBodyTree ? (
                  <span className="w-40 shrink-0 pt-1 font-mono text-xs text-muted-foreground">{slot.path}</span>
                ) : (
                  <Input
                    className="h-7 w-40 shrink-0 font-mono text-xs"
                    placeholder="result.text"
                    value={slot.path}
                    onChange={(event) => {
                      const rows = (response.fields ?? []).slice()
                      rows[index] = { ...rows[index], path: event.target.value }
                      onChange({ ...draft, response: { ...response, fields: rows } })
                    }}
                  />
                )}
                <span className="pt-1 text-xs text-muted-foreground">→</span>
                <FieldSelect
                  value={slot.field}
                  onChange={slot.setField}
                  fields={responseFields}
                  datalistId="maheshvara-response-fields-dl"
                />
                <Select value={slot.transform} onValueChange={slot.setTransform}>
                  <SelectTrigger className="h-7 w-40 shrink-0 text-xs" aria-label="transform">
                    <SelectValue placeholder="（不变）" />
                  </SelectTrigger>
                  <SelectContent className="max-h-72">
                    {transforms.map((transform) => (
                      <SelectItem key={transform} value={transform} className="text-xs">
                        {transform === '' ? '（不变）' : transform}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
                {!usesBodyTree && slot.remove && (
                  <Button
                    type="button"
                    variant="ghost"
                    size="iconSm"
                    aria-label="删除映射"
                    className="ml-auto shrink-0"
                    onClick={slot.remove}
                  >
                    <Trash2 className="h-3.5 w-3.5" />
                  </Button>
                )}
              </div>
            ))}
            {!usesBodyTree && (
              <Button
                type="button"
                variant="outline"
                size="sm"
                onClick={() =>
                  onChange({
                    ...draft,
                    response: { ...response, fields: [...(response.fields ?? []), { path: '', field: 'text' }] },
                  })
                }
              >
                <Plus className="mr-1 h-3 w-3" /> 添加映射
              </Button>
            )}
          </div>
        )}
      </SettingSection>
    </div>
  )
}
