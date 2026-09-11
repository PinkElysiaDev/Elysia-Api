import { useMemo, useState } from 'react'
import { ChevronRight, Copy, FileJson } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { cn } from '@/lib/utils'

/**
 * 示例 JSON 路径点选器：粘贴/上传上游示例响应，树形浏览并点选节点生成
 * dot path（含数组下标），供响应映射字段一键回填。
 */

interface PickerNode {
  key: string
  path: string
  type: string
  value: unknown
  children: PickerNode[]
}

function pathSegment(key: string): string {
  return /^[A-Za-z_][A-Za-z0-9_-]*$/.test(key) ? `.${key}` : `['${key}']`
}

function buildNodes(value: unknown, key: string, parentPath: string): PickerNode {
  const path = parentPath ? `${parentPath}${pathSegment(key)}` : key
  const node: PickerNode = { key, path, type: typeName(value), value, children: [] }
  if (Array.isArray(value)) {
    node.children = value.map((item, index) => buildNodes(item, `[${index}]`, path))
  } else if (value !== null && typeof value === 'object') {
    node.children = Object.entries(value as Record<string, unknown>).map(([childKey, childValue]) =>
      buildNodes(childValue, childKey, path),
    )
  }
  return node
}

function typeName(value: unknown): string {
  if (value === null) return 'null'
  if (Array.isArray(value)) return `array[${value.length}]`
  if (typeof value === 'object') return 'object'
  return typeof value
}

function valuePreview(value: unknown): string {
  if (value === null) return 'null'
  if (typeof value === 'string') return value.length > 40 ? `${value.slice(0, 40)}…` : value
  if (typeof value === 'object') return ''
  return String(value)
}

function TreeNode({
  node,
  depth,
  selected,
  onSelect,
}: {
  node: PickerNode
  depth: number
  selected: string | null
  onSelect: (node: PickerNode) => void
}) {
  const [open, setOpen] = useState(depth < 2)
  const hasChildren = node.children.length > 0
  return (
    <div>
      <button
        type="button"
        className={cn(
          'flex w-full items-center gap-1.5 rounded px-1.5 py-1 text-left text-xs transition-colors',
          selected === node.path ? 'bg-wash text-rose' : 'hover:bg-wash',
        )}
        style={{ paddingLeft: `${depth * 14 + 6}px` }}
        onClick={() => onSelect(node)}
        title={node.path}
      >
        {hasChildren ? (
          <ChevronRight
            className={cn('h-3 w-3 shrink-0 text-muted-foreground transition-transform', open && 'rotate-90')}
            onClick={(event) => {
              event.stopPropagation()
              setOpen(!open)
            }}
          />
        ) : (
          <span className="w-3 shrink-0" />
        )}
        <span className="shrink-0 font-medium text-foreground">{node.key}</span>
        <span className="shrink-0 text-muted-foreground">{node.type}</span>
        {!hasChildren && valuePreview(node.value) && (
          <span className="truncate text-muted-foreground/80">· {valuePreview(node.value)}</span>
        )}
      </button>
      {hasChildren && open && (
        <div>
          {node.children.map((child) => (
            <TreeNode key={child.path} node={child} depth={depth + 1} selected={selected} onSelect={onSelect} />
          ))}
        </div>
      )}
    </div>
  )
}

export function PathPicker({
  sampleText,
  onSampleTextChange,
  onPick,
  selected,
}: {
  sampleText: string
  onSampleTextChange: (text: string) => void
  onPick: (path: string) => void
  selected: string | null
}) {
  const parsed = useMemo(() => {
    const text = sampleText.trim()
    if (!text) return { error: null as string | null, root: null as PickerNode | null }
    try {
      return { error: null, root: buildNodes(JSON.parse(text), '$', '') }
    } catch (error) {
      return { error: (error as Error).message, root: null }
    }
  }, [sampleText])

  const rootPath = (node: PickerNode): string =>
    node.path.startsWith('$') ? node.path.slice(1).replace(/^\['/, '[') : node.path

  return (
    <div className="space-y-2">
      <Input
        placeholder="粘贴或上传上游示例响应 JSON…"
        value={sampleText}
        onChange={(event) => onSampleTextChange(event.target.value)}
      />
      <div className="flex items-center gap-2">
        <label className="cursor-pointer text-xs text-muted-foreground underline-offset-2 hover:text-foreground hover:underline">
          选择文件
          <input
            type="file"
            accept=".json,.txt,.jsonl,application/json,text/plain"
            className="hidden"
            onChange={async (event) => {
              const file = event.target.files?.[0]
              if (!file) return
              onSampleTextChange((await file.text()).trim())
              event.target.value = ''
            }}
          />
        </label>
        {selected && (
          <span className="ml-auto flex items-center gap-1.5 font-mono text-xs text-rose">
            {selected}
            <Button
              type="button"
              variant="ghost"
              size="iconSm"
              aria-label="复制路径"
              onClick={() => void navigator.clipboard.writeText(selected)}
            >
              <Copy className="h-3 w-3" />
            </Button>
          </span>
        )}
      </div>
      <div className="max-h-72 overflow-auto rounded-md border border-border bg-card p-1">
        {parsed.error ? (
          <p className="p-3 text-xs text-destructive">JSON 解析失败：{parsed.error}</p>
        ) : parsed.root ? (
          <TreeNode
            node={parsed.root}
            depth={0}
            selected={selected}
            onSelect={(node) => onPick(rootPath(node) || '$')}
          />
        ) : (
          <p className="flex items-center gap-2 p-3 text-xs text-muted-foreground">
            <FileJson className="h-3.5 w-3.5" />
            粘贴示例后，点击树节点即可把路径填入当前聚焦的映射字段
          </p>
        )}
      </div>
    </div>
  )
}
