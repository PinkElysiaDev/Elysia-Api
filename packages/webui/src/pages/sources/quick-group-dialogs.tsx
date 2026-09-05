import { useEffect, useMemo, useState } from 'react'
import { Check } from 'lucide-react'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Badge } from '@/components/ui/badge'
import { useToast } from '@/components/ui/use-toast'
import { api } from '@/lib/api'
import { revalidate, useGroups } from '@/lib/hooks'
import { cn } from '@/lib/utils'
import type { Model, ModelGroup } from '@/lib/types'

/** 从选中成员推导组能力（方向1）：任一成员支持即开启（视觉与工具同语义）。 */
function deriveGroupCapabilities(models: Model[]): { visionCapable: boolean; toolsCapable: boolean } {
  return {
    visionCapable: models.some((m) => m.visionCapable),
    toolsCapable: models.some((m) => m.toolsCapable),
  }
}

// 从源内选中的模型一键创建模型组（方向3）：组名默认取源名，能力按成员推导预填，
// 策略/重试用默认值；完整参数仍可在「模型组」页编辑。
export function QuickCreateGroupDialog({
  open,
  onOpenChange,
  models,
  defaultName,
  onSuccess,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  models: Model[]
  defaultName: string
  /** 创建成功后回调（取消/失败不触发）：调用方借此清除这批模型的选择态。 */
  onSuccess?: () => void
}) {
  const toast = useToast()
  const [name, setName] = useState('')
  const [saving, setSaving] = useState(false)
  const derived = useMemo(() => deriveGroupCapabilities(models), [models])

  useEffect(() => {
    if (open) setName(defaultName)
  }, [open, defaultName])

  async function handleCreate() {
    if (!name.trim()) {
      toast.error('请填写组名', '组名即客户端请求时看到的模型 ID')
      return
    }
    setSaving(true)
    try {
      const payload: ModelGroup = {
        id: '',
        name: name.trim(),
        enabled: true,
        models: models.map((m) => `${m.sourceId ?? ''}:${m.id}`),
        strategy: 'round-robin',
        maxRetries: 3,
        retryInterval: 1000,
        maxConcurrency: 0,
        dailyLimitMaxRequests: 0,
        dailyLimitMaxTokens: 0,
        type: 'llm',
        maxTokens: 0,
        ...derived,
      }
      await api.createGroup(payload)
      await revalidate.groups()
      toast.success('模型组已创建', `${name} · ${models.length} 个成员`)
      onSuccess?.()
      onOpenChange(false)
    } catch (err) {
      toast.error('创建失败', (err as Error).message)
    } finally {
      setSaving(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-lg">
        <DialogHeader>
          <DialogTitle>创建模型组</DialogTitle>
          <DialogDescription>从选中的 {models.length} 个模型创建组。调度策略默认轮询，稍后可在模型组页调整。</DialogDescription>
        </DialogHeader>
        <div className="grid gap-4">
          <div className="space-y-2">
            <Label required>组名（对外模型 ID）</Label>
            <Input value={name} placeholder={defaultName} onChange={(e) => setName(e.target.value)} />
          </div>
          <div className="space-y-2 rounded-xl border border-border/70 bg-background/40 p-3">
            <div className="flex flex-wrap items-center gap-1.5">
              {models.slice(0, 8).map((m) => (
                <Badge key={`${m.sourceId}-${m.id}`} variant="outline" className="max-w-full">
                  <span className="truncate">{m.id}</span>
                </Badge>
              ))}
              {models.length > 8 && <Badge variant="muted">+{models.length - 8}</Badge>}
            </div>
            <p className="pt-1 text-xs text-muted-foreground">
              按成员推导的组能力：
              <span className="ml-1">视觉 {derived.visionCapable ? '开' : '关'}</span>、
              <span className="ml-1">工具 {derived.toolsCapable ? '开' : '关'}</span>（任一成员支持即开启）
            </p>
          </div>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)} disabled={saving}>
            取消
          </Button>
          <Button onClick={handleCreate} disabled={saving}>
            {saving ? '创建中…' : '创建'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

// 把选中的模型批量加入现有模型组（方向3）：走原子 append 端点，去重由后端保证。
export function AddToGroupDialog({
  open,
  onOpenChange,
  models,
  onSuccess,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  models: Model[]
  /** 添加成功后回调（取消/失败不触发）：调用方借此清除这批模型的选择态。 */
  onSuccess?: () => void
}) {
  const toast = useToast()
  const { data: groups } = useGroups()
  const [selected, setSelected] = useState<string>('')
  const [search, setSearch] = useState('')
  const [saving, setSaving] = useState(false)

  useEffect(() => {
    if (open) {
      setSelected('')
      setSearch('')
    }
  }, [open])

  const filteredGroups = useMemo(() => {
    const kw = search.trim().toLowerCase()
    const list = (groups ?? []).filter((g) => g.enabled)
    if (!kw) return list
    return list.filter((g) => g.name.toLowerCase().includes(kw))
  }, [groups, search])

  async function handleAdd() {
    if (!selected) {
      toast.error('请选择模型组')
      return
    }
    setSaving(true)
    try {
      const result = await api.addGroupMembers(
        selected,
        models.map((m) => `${m.sourceId ?? ''}:${m.id}`),
      )
      await revalidate.groups()
      const groupName = (groups ?? []).find((g) => g.id === selected)?.name ?? selected
      toast.success('已加入模型组', `${groupName} · 新增 ${result.added} 个成员`)
      onSuccess?.()
      onOpenChange(false)
    } catch (err) {
      toast.error('添加失败', (err as Error).message)
    } finally {
      setSaving(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-md">
        <DialogHeader>
          <DialogTitle>添加到模型组</DialogTitle>
          <DialogDescription>把选中的 {models.length} 个模型追加到现有组（重复引用自动跳过）。</DialogDescription>
        </DialogHeader>
        <div className="space-y-2">
          <Input placeholder="搜索模型组…" value={search} onChange={(e) => setSearch(e.target.value)} />
          <div className="max-h-64 space-y-1 overflow-y-auto rounded-xl border border-border/70 bg-background/40 p-2">
            {filteredGroups.length === 0 && (
              <p className="py-6 text-center text-sm text-muted-foreground">没有可用的模型组</p>
            )}
            {filteredGroups.map((group) => (
              <button
                key={group.id}
                type="button"
                onClick={() => setSelected(group.id)}
                className={cn(
                  'flex w-full items-center justify-between gap-2 rounded-lg px-2.5 py-2 text-left text-sm transition-colors',
                  selected === group.id ? 'bg-primary/12 text-primary' : 'hover:bg-accent',
                )}
              >
                <span className="min-w-0 flex-1 truncate">{group.name}</span>
                <span className="shrink-0 text-xs text-muted-foreground">{group.models.length} 成员</span>
                {selected === group.id && <Check className="h-4 w-4 shrink-0" />}
              </button>
            ))}
          </div>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)} disabled={saving}>
            取消
          </Button>
          <Button onClick={handleAdd} disabled={saving || !selected}>
            {saving ? '添加中…' : '添加'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
