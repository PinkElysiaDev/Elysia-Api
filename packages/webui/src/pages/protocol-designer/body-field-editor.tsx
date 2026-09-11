import { Link2 } from 'lucide-react'
import type { CustomProtocolBodyTree, MaheshvaraFieldSpec } from '@/lib/types'
import { ScalarValueInput, StructureNode, type StructureNodeSpec } from './structure-tree'
import { isPlainObject, isRequestConstant, isRequestFieldRef } from './tree-utils'

/**
 * 请求体构造树编辑器（结构视图）：容器增删、常量叶子内联编辑；映射位叶子
 * 只显示当前映射的字段徽标——映射属性（字段选择/mode/default/omitIfEmpty）
 * 统一在「映射配置」卡中编辑。
 */

export function BodyFieldEditor({
  value,
  onChange,
  fields,
}: {
  value: CustomProtocolBodyTree | undefined
  onChange: (next: CustomProtocolBodyTree) => void
  fields: MaheshvaraFieldSpec[]
}) {
  const tree: unknown = value === undefined || value === null ? {} : value
  const spec: StructureNodeSpec = {
    leafOptions: [
      { value: 'mapped', label: '映射位' },
      { value: 'constant', label: '常量' },
    ],
    kindOf: (node) => {
      if (isRequestFieldRef(node)) return 'mapped'
      if (Array.isArray(node)) return 'array'
      if (isPlainObject(node)) return 'object'
      return 'constant'
    },
    convert: (kind) => {
      if (kind === 'object') return {}
      if (kind === 'array') return []
      if (kind === 'mapped') return { field: '', mode: 'json' }
      return { value: '' }
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
            </span>
            {fieldSpec && <span className="truncate text-2xs text-muted-foreground">{fieldSpec.label}</span>}
          </span>
        )
      }
      if (kind === 'constant') {
        const current = isRequestConstant(node) ? node.value : typeof node === 'object' && node !== null ? null : node
        return (
          <ScalarValueInput
            value={current}
            onChange={(next) => setLeaf({ value: next })}
            placeholder="固定值，如 2026-01-01 / true / 0.7"
          />
        )
      }
      return null
    },
    defaultLeaf: () => ({ value: '' }),
    objectLabel: '',
    arrayLabel: '',
  }

  return (
    <div className="space-y-2">
      <div className="rounded-md border border-border bg-card p-3">
        <StructureNode
          value={tree}
          spec={spec}
          depth={0}
          onChange={(next) => onChange(next as CustomProtocolBodyTree)}
        />
      </div>
      <p className="text-2xs text-muted-foreground">
        常量叶子在此编辑值；映射位在「映射配置」中选择对应的大自在天字段。
      </p>
    </div>
  )
}
