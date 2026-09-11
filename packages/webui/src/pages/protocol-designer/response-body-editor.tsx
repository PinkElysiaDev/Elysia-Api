import { Link2, Sparkles } from 'lucide-react'
import { Button } from '@/components/ui/button'
import type {
  CustomProtocolResponse,
  CustomProtocolResponseBodyTree,
  MaheshvaraFieldSpec,
} from '@/lib/types'
import { ScalarValueInput, StructureNode, type StructureNodeSpec } from './structure-tree'
import { hasResponseBodyTree, isPlainObject, isResponseMappingLeaf } from './tree-utils'
import { synthesizeResponseBodyFrom } from './tree-utils'

/**
 * 返回体构造树编辑器：与请求体对称——从零构造上游响应的 JSON 结构，叶子
 * 为映射位（对应 Maheshvara 的哪个字段）或示例值占位；映射属性统一在
 * 「映射配置」卡中编辑。
 */
export function ResponseBodyEditor({
  response,
  onChange,
  fields,
}: {
  response: CustomProtocolResponse
  onChange: (next: CustomProtocolResponse) => void
  fields: MaheshvaraFieldSpec[]
}) {
  const hasTree = hasResponseBodyTree(response)
  const hasLegacyFields = (response.fields ?? []).length > 0

  const spec: StructureNodeSpec = {
    leafOptions: [
      { value: 'mapped', label: '映射位' },
      { value: 'placeholder', label: '示例值' },
    ],
    kindOf: (node) => {
      if (isResponseMappingLeaf(node)) return 'mapped'
      if (Array.isArray(node)) return 'array'
      if (isPlainObject(node)) return 'object'
      return 'placeholder'
    },
    convert: (kind) => {
      if (kind === 'object') return {}
      if (kind === 'array') return []
      if (kind === 'mapped') return { field: '', value: '示例值' }
      return '示例值'
    },
    renderLeaf: (node, kind, setLeaf) => {
      if (kind === 'mapped') {
        const field = String(node?.field ?? '')
        const fieldSpec = fields.find((candidate) => candidate.name === field)
        return (
          <span className="flex min-w-0 flex-1 flex-col">
            <span className="inline-flex items-center gap-1.5 text-xs">
              <Link2 className="h-3 w-3 shrink-0 text-primary" />
              <span className={field ? 'font-mono text-foreground' : 'text-amber-600 dark:text-amber-400'}>
                {field || '未配置映射'}
              </span>
              {node?.value !== undefined && node?.value !== '' && (
                <span className="truncate text-2xs text-muted-foreground">
                  示例：{typeof node.value === 'string' ? node.value : JSON.stringify(node.value)}
                </span>
              )}
            </span>
            {fieldSpec && <span className="truncate text-2xs text-muted-foreground">{fieldSpec.label}</span>}
          </span>
        )
      }
      if (kind === 'placeholder') {
        const current =
          typeof node === 'object' && node !== null && 'value' in node
            ? (node as { value: unknown }).value
            : typeof node === 'object'
              ? null
              : node
        return (
          <ScalarValueInput
            value={current}
            onChange={(next) => setLeaf(next)}
            placeholder="示例值"
          />
        )
      }
      return null
    },
    defaultLeaf: () => '示例值',
    objectLabel: '',
    arrayLabel: '',
  }

  const synthesize = () => {
    const tree = synthesizeResponseBodyFrom(response.fields ?? [], response.sample)
    const next = { ...response, body: tree }
    delete next.fields
    onChange(next)
  }

  return (
    <div className="space-y-2">
      {!hasTree && hasLegacyFields && (
        <div className="flex flex-wrap items-center gap-2 rounded-md border border-border bg-card p-2.5 text-xs text-muted-foreground">
          <span>当前使用映射行表（{response.fields!.length} 行）。</span>
          <Button type="button" variant="outline" size="sm" onClick={synthesize}>
            <Sparkles className="mr-1 h-3 w-3" /> 转为构造树
          </Button>
        </div>
      )}
      <div className="rounded-md border border-border bg-card p-3">
        <StructureNode
          value={hasTree ? response.body : {}}
          spec={spec}
          depth={0}
          onChange={(next) => onChange({ ...response, body: next as CustomProtocolResponseBodyTree })}
        />
      </div>
      <p className="text-2xs text-muted-foreground">
        按上游响应示例搭建结构；映射位在「映射配置」中选择对应的大自在天字段。
      </p>
    </div>
  )
}
