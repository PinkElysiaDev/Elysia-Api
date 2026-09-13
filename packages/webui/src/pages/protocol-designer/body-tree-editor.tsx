import { Link2, Sparkles } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Seg } from '@/components/ui/seg'
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
  CustomProtocolResponse,
  CustomProtocolResponseBodyTree,
  MaheshvaraFieldSpec,
} from '@/lib/types'
import { ScalarValueInput, StructureNode, type StructureNodeSpec } from './structure-tree'
import {
  isPlainObject,
  isRequestConstant,
  isRequestFieldRef,
  isResponseMappingLeaf,
  synthesizeResponseBodyFrom,
} from './tree-utils'

/**
 * 通用构造树编辑器：请求体与返回体共用同一界面（功能互为镜像）。
 * 容器为普通 JSON 对象/数组；叶子为映射位（对应 Maheshvara 的哪个字段，
 * 字段与 mode/default/omitIfEmpty 或 transform/示例值 就地编辑）或
 * 常量/示例值（本组件内联编辑）。direction 仅决定叶子选项与文案：
 * 请求侧=映射位/常量；响应侧=映射位/示例值。
 */

/** 映射位字段选择：datalist 补全 + 通俗解释 + 未知字段告警。 */
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
  // 单一 Input 形态（datalist 补全）：避免已知/未知字段间切换 Select↔Input
  // 导致组件卸载重建、输入中失焦。
  const spec = fields.find((field) => field.name === value)
  return (
    <div className="flex min-w-0 flex-1 flex-col gap-0.5">
      <div className="flex min-w-0 flex-1 items-center gap-1.5">
        <Link2 className="h-3 w-3 shrink-0 text-primary" />
        <Input
          list={datalistId}
          className="h-7 w-full min-w-40 font-mono text-xs"
          placeholder="映射到的大自在天字段"
          value={value}
          onChange={(event) => onChange(event.target.value.trim())}
        />
      </div>
      {spec ? (
        <span className="pl-[18px] text-2xs text-muted-foreground">{spec.label}</span>
      ) : (
        value !== '' && (
          <span className="pl-[18px] text-2xs text-amber-600 dark:text-amber-400">未知字段，保存时将被拒绝</span>
        )
      )}
    </div>
  )
}

export function BodyTreeEditor({
  direction,
  value,
  onChange,
  fields,
  /** 仅响应方向：transform 候选（来自 schema）。 */
  transforms = [],
  /** 仅响应方向：存在映射行表时提供「转为构造树」。 */
  response,
  onResponseChange,
}: {
  direction: 'request' | 'response'
  value: unknown
  onChange: (next: unknown) => void
  fields: MaheshvaraFieldSpec[]
  transforms?: string[]
  response?: CustomProtocolResponse
  onResponseChange?: (next: CustomProtocolResponse) => void
}) {
  const isRequest = direction === 'request'
  const tree: unknown = value === undefined || value === null ? {} : value
  const datalistId = isRequest ? 'maheshvara-request-fields-dl' : 'maheshvara-response-fields-dl'

  const spec: StructureNodeSpec = {
    leafOptions: isRequest
      ? [
          { value: 'mapped', label: '映射位' },
          { value: 'constant', label: '常量' },
        ]
      : [
          { value: 'mapped', label: '映射位' },
          { value: 'placeholder', label: '示例值' },
        ],
    kindOf: (node) => {
      if (isRequest ? isRequestFieldRef(node) : isResponseMappingLeaf(node)) return 'mapped'
      if (Array.isArray(node)) return 'array'
      // 请求侧常量在数据模型中是 {"value": X} 单键对象（与后端编译器一致），
      // 必须先于普通对象判定，否则编辑值时叶子会在常量/对象两种形态间
      // 切换，整行重挂载导致输入失焦。
      if (isRequest && isRequestConstant(node)) return 'constant'
      if (isPlainObject(node)) return 'object'
      return isRequest ? 'constant' : 'placeholder'
    },
    convert: (kind) => {
      if (kind === 'object') return {}
      if (kind === 'array') return []
      if (kind === 'mapped') return isRequest ? { field: '', mode: 'json' } : { field: '', value: '示例值' }
      return isRequest ? { value: '' } : '示例值'
    },
    renderLeaf: (node, kind, setLeaf) => {
      if (kind === 'mapped') {
        return (
          <FieldSelect
            value={String(node?.field ?? '')}
            onChange={(field) => setLeaf({ ...node, field })}
            fields={fields}
            datalistId={datalistId}
          />
        )
      }
      const current = isRequest
        ? isRequestConstant(node)
          ? node.value
          : typeof node === 'object' && node !== null
            ? null
            : node
        : typeof node === 'object' && node !== null && 'value' in node
          ? (node as { value: unknown }).value
          : typeof node === 'object'
            ? null
            : node
      return (
        <ScalarValueInput
          value={current}
          onChange={(next) => setLeaf(isRequest ? { value: next } : next)}
          placeholder={isRequest ? '固定值，如 2026-01-01 / true / 0.7' : '示例值'}
        />
      )
    },
    renderLeafDetail: (node, kind, setLeaf) => {
      if (kind !== 'mapped') return null
      if (isRequest) {
        return (
          <>
            <Seg
              size="sm"
              value={node.mode ?? 'json'}
              options={[
                { value: 'json', label: '原生 JSON' },
                { value: 'string', label: '字符串' },
              ]}
              onChange={(mode) => setLeaf({ ...node, mode: mode as 'json' | 'string' })}
            />
            <Input
              className="h-7 w-28 font-mono text-xs"
              placeholder="default 可选"
              value={
                node.default === undefined
                  ? ''
                  : typeof node.default === 'string'
                    ? node.default
                    : JSON.stringify(node.default)
              }
              onChange={(event) => {
                const text = event.target.value.trim()
                if (!text) {
                  setLeaf({ ...node, default: undefined })
                  return
                }
                try {
                  setLeaf({ ...node, default: JSON.parse(text) })
                } catch {
                  setLeaf({ ...node, default: text })
                }
              }}
            />
            <label className="flex items-center gap-1 text-2xs text-muted-foreground">
              <Switch
                checked={!!node.omitIfEmpty}
                onCheckedChange={(checked) => setLeaf({ ...node, omitIfEmpty: checked })}
              />
              空值省略
            </label>
          </>
        )
      }
      return (
        <>
          <Select value={node.transform ?? ''} onValueChange={(transform) => setLeaf({ ...node, transform })}>
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
          <ScalarValueInput
            value={node.value ?? ''}
            onChange={(next) => setLeaf({ ...node, value: next })}
            placeholder="示例值"
          />
        </>
      )
    },
    defaultLeaf: () => (isRequest ? { value: '' } : '示例值'),
    objectLabel: '',
    arrayLabel: '',
  }

  const hasLegacyFields = !isRequest && (response?.fields ?? []).length > 0
  const synthesize = () => {
    if (!response || !onResponseChange) return
    const next: CustomProtocolResponse = {
      ...response,
      body: synthesizeResponseBodyFrom(response.fields ?? [], response.sample),
    }
    delete next.fields
    onResponseChange(next)
  }

  return (
    <div className="space-y-2">
      {hasLegacyFields && (
        <div className="flex flex-wrap items-center gap-2 rounded-md border border-border bg-card p-2.5 text-xs text-muted-foreground">
          <span>当前使用映射行表（{response!.fields!.length} 行）。</span>
          <Button type="button" variant="outline" size="sm" onClick={synthesize}>
            <Sparkles className="mr-1 h-3 w-3" /> 转为构造树
          </Button>
        </div>
      )}
      <datalist id={datalistId}>
        {fields.map((field) => (
          <option key={field.name} value={field.name}>
            {field.label}
          </option>
        ))}
      </datalist>
      <div className="rounded-md border border-border bg-card p-3">
        <StructureNode value={tree} spec={spec} depth={0} onChange={onChange} />
      </div>
      <p className="text-2xs text-muted-foreground">
        {isRequest
          ? '常量在此编辑值；映射位就地选择对应的大自在天字段，并可在其下方调整输出形态。'
          : '示例值在此编辑；映射位就地选择对应的大自在天字段，并可在其下方调整 transform 与示例值。'}
      </p>
    </div>
  )
}

/** 便捷包装：请求体构造树（value/onChange 直接对接 request.body）。 */
export function RequestBodyTreeEditor({
  value,
  onChange,
  fields,
}: {
  value: CustomProtocolBodyTree | undefined
  onChange: (next: CustomProtocolBodyTree) => void
  fields: MaheshvaraFieldSpec[]
}) {
  return (
    <BodyTreeEditor
      direction="request"
      value={value}
      onChange={(next) => onChange(next as CustomProtocolBodyTree)}
      fields={fields}
    />
  )
}

/** 便捷包装：返回体构造树（对接 response.body，并带行表转换工具条）。 */
export function ResponseBodyTreeEditor({
  response,
  onChange,
  fields,
  transforms,
}: {
  response: CustomProtocolResponse
  onChange: (next: CustomProtocolResponse) => void
  fields: MaheshvaraFieldSpec[]
  transforms?: string[]
}) {
  return (
    <BodyTreeEditor
      direction="response"
      value={response.body}
      onChange={(next) => onChange({ ...response, body: next as CustomProtocolResponseBodyTree })}
      fields={fields}
      transforms={transforms}
      response={response}
      onResponseChange={onChange}
    />
  )
}
