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
  /**
   * inline（默认）：映射位就地编辑字段/格式/transform——单页即完稿的旧形态。
   * badge：映射位只读徽标（字段 · 格式），映射分配集中在「映射关系」页签
   * ——结构页签保持纯结构编辑,页面更整齐。
   */
  mappingMode = 'inline',
}: {
  direction: 'request' | 'response'
  value: unknown
  onChange: (next: unknown) => void
  fields: MaheshvaraFieldSpec[]
  transforms?: string[]
  response?: CustomProtocolResponse
  onResponseChange?: (next: CustomProtocolResponse) => void
  mappingMode?: 'inline' | 'badge'
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
        if (mappingMode === 'badge') {
          const field = String(node?.field ?? '')
          const detail = isRequest
            ? String(node?.mode ?? 'json') === 'string'
              ? '字符串'
              : '原生 JSON'
            : node?.transform
              ? `转换:${node.transform}`
              : ''
          return (
            <span
              className={`inline-flex items-center gap-1 rounded border px-1.5 py-0.5 font-mono text-2xs ${
                field
                  ? 'border-border bg-secondary/40 text-foreground'
                  : 'border-amber-500/40 bg-amber-500/10 text-amber-600 dark:text-amber-400'
              }`}
              title="映射分配集中在「映射关系」页签"
            >
              {field ? `↦ ${field}` : '未映射'}
              {detail && <span className="text-muted-foreground">· {detail}</span>}
            </span>
          )
        }
        return (
          <FieldSelect
            value={String(node?.field ?? '')}
            onChange={(field) => setLeaf({ ...node, field })}
            fields={fields}
            datalistId={datalistId}
          />
        )
      }
      // kindOf 已保证:request 的 constant 只可能是 {value:X} 包装或标量;
      // response 的 placeholder 只可能是标量。此前的多分支防御不可达。
      const editableValue = isRequest
        ? isRequestConstant(node)
          ? node.value
          : node
        : node
      return (
        <ScalarValueInput
          value={editableValue}
          onChange={(next) => setLeaf(isRequest ? { value: next } : next)}
          placeholder={isRequest ? '固定值，如 2026-01-01 / true / 0.7' : '示例值'}
        />
      )
    },
    renderLeafDetail: (node, kind, setLeaf) => {
      if (kind !== 'mapped' || mappingMode === 'badge') return null
      if (isRequest) {
        // 配置项纵向栈:每项一行、带固定标签,任何分辨率都不折行成多列。
        return (
          <>
            <div className="flex items-center gap-2">
              <span className="w-14 shrink-0 text-2xs text-muted-foreground">格式</span>
              <Seg
                size="sm"
                value={node.mode ?? 'json'}
                options={[
                  { value: 'json', label: '原生 JSON' },
                  { value: 'string', label: '字符串' },
                ]}
                onChange={(mode) => setLeaf({ ...node, mode: mode as 'json' | 'string' })}
              />
            </div>
            <div className="flex items-center gap-2">
              <span className="w-14 shrink-0 text-2xs text-muted-foreground">缺省值</span>
              <Input
              className="h-7 w-full max-w-56 font-mono text-xs"
              placeholder="缺省时使用，可留空"
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
            </div>
            <label className="flex items-center gap-2 text-2xs text-muted-foreground">
              <span className="w-14 shrink-0"></span>
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
          <div className="flex items-center gap-2">
            <span className="w-14 shrink-0 text-2xs text-muted-foreground">转换</span>
            <Select value={node.transform ?? ''} onValueChange={(transform) => setLeaf({ ...node, transform })}>
              <SelectTrigger className="h-7 w-full max-w-56 text-xs" aria-label="transform">
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
          </div>
          <div className="flex items-center gap-2">
            <span className="w-14 shrink-0 text-2xs text-muted-foreground">示例值</span>
            <ScalarValueInput
              value={node.value ?? ''}
              onChange={(next) => setLeaf({ ...node, value: next })}
              placeholder="示例值"
            />
          </div>
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
  mappingMode = 'inline',
}: {
  value: CustomProtocolBodyTree | undefined
  onChange: (next: CustomProtocolBodyTree) => void
  fields: MaheshvaraFieldSpec[]
  mappingMode?: 'inline' | 'badge'
}) {
  return (
    <BodyTreeEditor
      direction="request"
      value={value}
      onChange={(next) => onChange(next as CustomProtocolBodyTree)}
      fields={fields}
      mappingMode={mappingMode}
    />
  )
}

/** 便捷包装：返回体构造树（对接 response.body，并带行表转换工具条）。 */
export function ResponseBodyTreeEditor({
  response,
  onChange,
  fields,
  transforms,
  mappingMode = 'inline',
}: {
  response: CustomProtocolResponse
  onChange: (next: CustomProtocolResponse) => void
  fields: MaheshvaraFieldSpec[]
  transforms?: string[]
  mappingMode?: 'inline' | 'badge'
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
      mappingMode={mappingMode}
    />
  )
}
