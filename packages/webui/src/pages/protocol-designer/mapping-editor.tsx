import { Link2 } from 'lucide-react'
import { Input } from '@/components/ui/input'
import { Seg } from '@/components/ui/seg'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import type {
  CustomProtocolBodyTree,
  CustomProtocolConfig,
  CustomProtocolResponseBodyTree,
  MaheshvaraFieldSpec,
} from '@/lib/types'
import { ScalarValueInput } from './structure-tree'
import {
  collectRequestMappedLeaves,
  collectResponseMappedLeaves,
  replaceLeafAtPath,
  type MappedLeafLocator,
} from './tree-utils'

/**
 * 映射关系页签:请求体与响应体构造树里全部映射位的集中分配面。
 * 行集自动跟随两侧结构树(请求/响应体页签增删节点,此处即时增减);
 * 结构树是唯一事实源,本页所有编辑按路径写回树的映射标注。
 */

/** 映射位字段选择:datalist 补全 + 通俗解释 + 未知字段告警(与树编辑器同款语义)。 */
function FieldSelect({
  value,
  onChange,
  fields,
  datalistId,
}: {
  value: string
  onChange: (next: string) => void
  fields: MaheshvaraFieldSpec[]
  datalistId: string
}) {
  const spec = fields.find((field) => field.name === value)
  return (
    <div className="flex min-w-0 flex-1 flex-col gap-0.5">
      <div className="flex min-w-0 flex-1 items-center gap-1.5">
        <Link2 className="h-3 w-3 shrink-0 text-primary" />
        <Input
          list={datalistId}
          className="h-7 w-full min-w-36 font-mono text-xs"
          placeholder="映射到的 Maheshvara 字段"
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

function LeafPathLabel({ locator }: { locator: MappedLeafLocator }) {
  const unmapped = String(locator.node.field ?? '') === ''
  return (
    <span
      className={`font-mono text-xs ${unmapped ? 'text-amber-600 dark:text-amber-400' : 'text-foreground'}`}
      title={unmapped ? '未映射:字段留空,渲染时输出 null' : undefined}
    >
      {locator.displayPath}
      {unmapped && ' · 未映射'}
    </span>
  )
}

export function MappingEditor({
  protocol,
  onChange,
  requestFields,
  responseFields,
  transforms,
}: {
  protocol: CustomProtocolConfig
  onChange: (next: CustomProtocolConfig) => void
  requestFields: MaheshvaraFieldSpec[]
  responseFields: MaheshvaraFieldSpec[]
  transforms: string[]
}) {
  const requestLeaves = collectRequestMappedLeaves(protocol.request.body)
  const responseLeaves = collectResponseMappedLeaves(protocol.response?.body)

  const setRequestLeaf = (locator: MappedLeafLocator, patch: Record<string, unknown>) => {
    onChange({
      ...protocol,
      request: {
        ...protocol.request,
        body: replaceLeafAtPath(protocol.request.body, locator.segments, {
          ...locator.node,
          ...patch,
        }) as CustomProtocolBodyTree,
      },
    })
  }
  const setResponseLeaf = (locator: MappedLeafLocator, patch: Record<string, unknown>) => {
    if (!protocol.response) return
    onChange({
      ...protocol,
      response: {
        ...protocol.response,
        body: replaceLeafAtPath(protocol.response.body, locator.segments, {
          ...locator.node,
          ...patch,
        }) as CustomProtocolResponseBodyTree,
      },
    })
  }

  return (
    <div className="space-y-8">
      <datalist id="mapping-request-fields-dl">
        {requestFields.map((field) => (
          <option key={field.name} value={field.name}>
            {field.label}
          </option>
        ))}
      </datalist>
      <datalist id="mapping-response-fields-dl">
        {responseFields.map((field) => (
          <option key={field.name} value={field.name}>
            {field.label}
          </option>
        ))}
      </datalist>

      <section className="space-y-2">
        <h3 className="text-sm font-semibold">
          请求体映射 <span className="ml-1 text-2xs font-normal text-muted-foreground">({requestLeaves.length} 项)</span>
        </h3>
        {requestLeaves.length === 0 ? (
          <p className="rounded-md border border-dashed border-border p-3 text-xs text-muted-foreground">
            请求体构造树中还没有映射位——在「请求」页签的请求体结构里把节点设为「映射位」,映射项会自动出现在此处。
          </p>
        ) : (
          <div className="divide-y divide-border/60 rounded-md border border-border bg-card">
            {requestLeaves.map((locator, index) => (
              <div key={locator.displayPath} className={`grid items-center gap-x-4 gap-y-2 p-3 ${index > 0 ? '' : ''} sm:grid-cols-[minmax(140px,220px)_1fr_auto]`}>
                <LeafPathLabel locator={locator} />
                <FieldSelect
                  value={String(locator.node.field ?? '')}
                  onChange={(field) => setRequestLeaf(locator, { field })}
                  fields={requestFields}
                  datalistId="mapping-request-fields-dl"
                />
                <Seg
                  size="sm"
                  value={String(locator.node.mode ?? 'json')}
                  options={[
                    { value: 'json', label: '原生 JSON' },
                    { value: 'string', label: '字符串' },
                  ]}
                  onChange={(mode) => setRequestLeaf(locator, { mode })}
                />
              </div>
            ))}
          </div>
        )}
      </section>

      <section className="space-y-2">
        <h3 className="text-sm font-semibold">
          响应体映射 <span className="ml-1 text-2xs font-normal text-muted-foreground">({responseLeaves.length} 项)</span>
        </h3>
        {responseLeaves.length === 0 ? (
          <p className="rounded-md border border-dashed border-border p-3 text-xs text-muted-foreground">
            响应体构造树中还没有映射位——在「响应体」页签把节点设为「映射位」,映射项会自动出现在此处。
          </p>
        ) : (
          <div className="divide-y divide-border/60 rounded-md border border-border bg-card">
            {responseLeaves.map((locator) => (
              <div key={locator.displayPath} className="grid items-center gap-x-4 gap-y-2 p-3 sm:grid-cols-[minmax(140px,220px)_1fr_auto_auto]">
                <LeafPathLabel locator={locator} />
                <FieldSelect
                  value={String(locator.node.field ?? '')}
                  onChange={(field) => setResponseLeaf(locator, { field })}
                  fields={responseFields}
                  datalistId="mapping-response-fields-dl"
                />
                <Select
                  value={String(locator.node.transform ?? '')}
                  onValueChange={(transform) => setResponseLeaf(locator, { transform })}
                >
                  <SelectTrigger className="h-7 w-36 text-xs" aria-label="transform">
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
                  value={locator.node.value ?? ''}
                  onChange={(next) => setResponseLeaf(locator, { value: next })}
                  placeholder="示例值"
                />
              </div>
            ))}
          </div>
        )}
      </section>

      <p className="text-2xs text-muted-foreground">
        本页为映射分配面:结构(键、嵌套、常量/示例值)在「请求」「响应体」页签编辑,此处集中分配各映射位对应的
        Maheshvara 字段;映射行随两侧结构自动增减。缺省值/空值省略等高级项可在 JSON 页签按路径补充。
      </p>
    </div>
  )
}
