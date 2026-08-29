import { useEffect, useState } from 'react'
import { Trash2 } from 'lucide-react'
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
import { Switch } from '@/components/ui/switch'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { useConfirm } from '@/components/ui/confirm-dialog'
import { useToast } from '@/components/ui/use-toast'
import { api } from '@/lib/api'
import { revalidate } from '@/lib/hooks'
import type { Model, ModelType, ThinkingMode } from '@/lib/types'

// 模型编辑弹窗（方向4）：对拉取/手动入库的单个模型做编辑与启停。
// 能力字段一经保存即标记 manual，后续刷新保留用户值（capability_source 语义）。
export function ModelEditDialog({
  open,
  onOpenChange,
  model,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  model: Model | null
}) {
  const toast = useToast()
  const { confirm, dialog } = useConfirm()
  const [saving, setSaving] = useState(false)
  const [form, setForm] = useState({
    name: '',
    type: 'llm' as ModelType,
    maxTokens: 0,
    visionCapable: false,
    toolsCapable: false,
    structuredOutput: false,
    thinkingMode: 'both' as ThinkingMode,
    enabled: true,
  })

  useEffect(() => {
    if (open && model) {
      setForm({
        name: model.name || model.id,
        type: model.type,
        maxTokens: model.maxTokens,
        visionCapable: model.visionCapable,
        toolsCapable: !!model.toolsCapable,
        structuredOutput: !!model.structuredOutput,
        thinkingMode: model.thinkingMode ?? 'both',
        enabled: model.enabled !== false,
      })
    }
  }, [open, model])

  if (!model) return null
  const fromCatalog = model.capabilitySource === 'catalog'

  async function handleSave() {
    if (!model) return
    setSaving(true)
    try {
      await api.updateModel(model.sourceId ?? '', model.id, form)
      await revalidate.models()
      toast.success('模型已更新')
      onOpenChange(false)
    } catch (err) {
      toast.error('保存失败', (err as Error).message)
    } finally {
      setSaving(false)
    }
  }

  async function handleDelete() {
    if (!model) return
    const okToDelete = await confirm({
      title: `删除模型「${model.name || model.id}」？`,
      description: '将从模型缓存中删除，并自动移除模型组内的相关引用。',
      confirmText: '删除',
    })
    if (!okToDelete) return
    try {
      await api.deleteModel(model.sourceId ?? '', model.id)
      await revalidate.models()
      await revalidate.groups()
      toast.success('模型已删除')
      onOpenChange(false)
    } catch (err) {
      toast.error('删除失败', (err as Error).message)
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-xl">
        <DialogHeader>
          <DialogTitle>编辑模型</DialogTitle>
          <DialogDescription>
            <span className="font-mono">{model.id}</span>
            {model.sourceName ? ` · ${model.sourceName}` : ''}
            {fromCatalog && ' · 能力来自目录自动填充，保存后将改为手动维护'}
          </DialogDescription>
        </DialogHeader>

        <div className="grid gap-4">
          <div className="grid gap-4 sm:grid-cols-2">
            <div className="space-y-2">
              <Label>显示名称</Label>
              <Input value={form.name} onChange={(e) => setForm((p) => ({ ...p, name: e.target.value }))} />
            </div>
            <div className="space-y-2">
              <Label>类型</Label>
              <Select value={form.type} onValueChange={(v) => setForm((p) => ({ ...p, type: v as ModelType }))}>
                <SelectTrigger>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="llm">LLM</SelectItem>
                  <SelectItem value="embedding">Embedding</SelectItem>
                  <SelectItem value="reranker">Reranker</SelectItem>
                </SelectContent>
              </Select>
            </div>
          </div>

          <div className="grid gap-4 sm:grid-cols-2">
            <div className="space-y-2">
              <Label>
                MaxTokens<span className="ml-1 text-xs font-normal text-muted-foreground">0 = 未设置</span>
              </Label>
              <Input
                type="number"
                min={0}
                value={form.maxTokens}
                onChange={(e) => setForm((p) => ({ ...p, maxTokens: Math.max(0, Number(e.target.value) || 0) }))}
              />
            </div>
            <div className="space-y-2">
              <Label>思考模式</Label>
              <Select
                value={form.thinkingMode}
                onValueChange={(v) => setForm((p) => ({ ...p, thinkingMode: v as ThinkingMode }))}
              >
                <SelectTrigger>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="both">双向 Both</SelectItem>
                  <SelectItem value="thinking-only">仅思考 Thinking-only</SelectItem>
                  <SelectItem value="non-thinking-only">仅非思考 Non-thinking</SelectItem>
                </SelectContent>
              </Select>
            </div>
          </div>

          <div className="flex flex-wrap gap-x-6 gap-y-3">
            <label className="flex items-center gap-2">
              <Switch
                checked={form.visionCapable}
                onCheckedChange={(v) => setForm((p) => ({ ...p, visionCapable: v }))}
              />
              <span className="text-sm font-medium">视觉</span>
            </label>
            <label className="flex items-center gap-2">
              <Switch checked={form.toolsCapable} onCheckedChange={(v) => setForm((p) => ({ ...p, toolsCapable: v }))} />
              <span className="text-sm font-medium">工具调用</span>
            </label>
            <label className="flex items-center gap-2">
              <Switch
                checked={form.structuredOutput}
                onCheckedChange={(v) => setForm((p) => ({ ...p, structuredOutput: v }))}
              />
              <span className="text-sm font-medium">结构化输出</span>
            </label>
          </div>

          <label className="flex items-center gap-3">
            <Switch checked={form.enabled} onCheckedChange={(v) => setForm((p) => ({ ...p, enabled: v }))} />
            <span className="text-sm font-medium">
              启用此模型
              <span className="ml-1 text-xs font-normal text-muted-foreground">关闭后不参与模型组调度</span>
            </span>
          </label>
        </div>

        <DialogFooter className="sm:justify-between">
          <Button variant="ghost" className="text-destructive" onClick={handleDelete} disabled={saving}>
            <Trash2 className="h-4 w-4" /> 删除
          </Button>
          <div className="flex gap-2">
            <Button variant="outline" onClick={() => onOpenChange(false)} disabled={saving}>
              取消
            </Button>
            <Button onClick={handleSave} disabled={saving}>
              {saving ? '保存中…' : '保存'}
            </Button>
          </div>
        </DialogFooter>
        {dialog}
      </DialogContent>
    </Dialog>
  )
}
