import { Plus, Trash2 } from 'lucide-react'
import type { ReactNode } from 'react'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'

/**
 * 通用结构树编辑器骨架：容器（对象/数组）增删由本组件负责；叶子的类型
 * 切换与内容渲染由调用方注入（请求体叶子=映射位/常量；返回体叶子=映射位/
 * 示例占位）。映射属性统一在「映射配置」卡中编辑。
 */

export interface StructureNodeSpec {
  /** 叶子类型选项（value 需与叶子判别结果一致） */
  leafOptions: { value: string; label: string }[]
  /** 判别节点类型：'object' | 'array' | 叶子类型 value */
  kindOf: (node: unknown) => string
  /** 切换节点类型时的默认值 */
  convert: (kind: string) => unknown
  /** 渲染叶子内容（返回 null 表示该叶子无需额外控件） */
  renderLeaf: (node: any, kind: string, setLeaf: (next: unknown) => void) => ReactNode
  /** 新增字段/元素的默认叶子 */
  defaultLeaf: () => unknown
  /** 空对象/数组的展示文案 */
  objectLabel: string
  arrayLabel: string
}

export function StructureNode({
  value,
  onChange,
  spec,
  depth,
  label,
  onLabelChange,
  onRemove,
}: {
  value: unknown
  onChange: (next: unknown) => void
  spec: StructureNodeSpec
  depth: number
  label?: string
  onLabelChange?: (key: string) => void
  onRemove?: () => void
}) {
  const kind = spec.kindOf(value)
  const isLeaf = !Array.isArray(value) && (kind !== 'object' || typeof value !== 'object' || value === null)
  const isObject = kind === 'object'
  const isArray = kind === 'array'

  return (
    <div className="space-y-1.5" style={{ marginLeft: depth > 0 ? 18 : 0 }}>
      <div className="flex flex-wrap items-center gap-1.5">
        {label !== undefined && onLabelChange && (
          <Input
            className="h-7 w-36 shrink-0 font-mono text-xs"
            value={label}
            placeholder="字段名"
            onChange={(event) => onLabelChange(event.target.value)}
          />
        )}
        {label !== undefined && !onLabelChange && (
          <span className="w-9 shrink-0 font-mono text-xs text-muted-foreground">{label}</span>
        )}
        <Select value={kind} onValueChange={(next) => onChange(spec.convert(next))}>
          <SelectTrigger className="h-7 w-[118px] shrink-0 text-xs" aria-label="节点类型">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {spec.leafOptions.map((option) => (
              <SelectItem key={option.value} value={option.value} className="text-xs">
                {option.label}
              </SelectItem>
            ))}
            <SelectItem value="object" className="text-xs">对象</SelectItem>
            <SelectItem value="array" className="text-xs">数组</SelectItem>
          </SelectContent>
        </Select>
        {isLeaf && spec.renderLeaf(value as any, kind, (next) => onChange(next))}
        {onRemove && (
          <Button type="button" variant="ghost" size="iconSm" aria-label="删除节点" onClick={onRemove} className="shrink-0">
            <Trash2 className="h-3.5 w-3.5" />
          </Button>
        )}
      </div>
      {isObject && (
        <div className="space-y-1.5 rounded-md border border-border/60 bg-background/40 p-2">
          {Object.entries(value as Record<string, unknown>).map(([key, child], index) => (
            <StructureNode
              key={`${key}:${index}`}
              label={key}
              onLabelChange={(nextKey) => {
                if (nextKey === key || nextKey in (value as Record<string, unknown>)) return
                const rebuilt: Record<string, unknown> = {}
                Object.entries(value as Record<string, unknown>).forEach(([k, v], i) => {
                  rebuilt[i === index ? nextKey : k] = v
                })
                onChange(rebuilt)
              }}
              value={child}
              spec={spec}
              depth={depth + 1}
              onChange={(next) => onChange({ ...(value as Record<string, unknown>), [key]: next })}
              onRemove={() => {
                const rest = { ...(value as Record<string, unknown>) }
                delete rest[key]
                onChange(rest)
              }}
            />
          ))}
          <Button
            type="button"
            variant="outline"
            size="sm"
            onClick={() =>
              onChange({
                ...(value as Record<string, unknown>),
                [`field_${Object.keys(value as Record<string, unknown>).length + 1}`]: spec.defaultLeaf(),
              })
            }
          >
            <Plus className="mr-1 h-3 w-3" /> 添加字段
          </Button>
        </div>
      )}
      {isArray && (
        <div className="space-y-1.5 rounded-md border border-border/60 bg-background/40 p-2">
          {(value as unknown[]).map((item, index) => (
            <StructureNode
              key={index}
              label={`[${index}]`}
              value={item}
              spec={spec}
              depth={depth + 1}
              onChange={(next) => {
                const items = (value as unknown[]).slice()
                items[index] = next
                onChange(items)
              }}
              onRemove={() => onChange((value as unknown[]).filter((_, i) => i !== index))}
            />
          ))}
          <Button
            type="button"
            variant="outline"
            size="sm"
            onClick={() => onChange([...(value as unknown[]), spec.defaultLeaf()])}
          >
            <Plus className="mr-1 h-3 w-3" /> 添加元素
          </Button>
        </div>
      )}
    </div>
  )
}

/** 常量/示例值的标量输入：字符串直输，其余按 JSON 字面量。 */
export function ScalarValueInput({
  value,
  onChange,
  placeholder,
}: {
  value: unknown
  onChange: (next: unknown) => void
  placeholder: string
}) {
  const isString = typeof value === 'string'
  return (
    <Input
      className="h-7 min-w-40 flex-1 font-mono text-xs"
      value={isString ? (value as string) : JSON.stringify(value ?? null)}
      placeholder={placeholder}
      onChange={(event) => {
        const text = event.target.value
        try {
          onChange(JSON.parse(text))
        } catch {
          onChange(text)
        }
      }}
    />
  )
}
