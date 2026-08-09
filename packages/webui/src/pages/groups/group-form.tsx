import { useEffect, useMemo, useState } from 'react'
import { Check, Search } from 'lucide-react'
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
import { Badge } from '@/components/ui/badge'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { useToast } from '@/components/ui/use-toast'
import { api } from '@/lib/api'
import { revalidate, useModels, useSources } from '@/lib/hooks'
import { cn } from '@/lib/utils'
import type { GroupStrategy, Model, ModelGroup, ModelType } from '@/lib/types'

function emptyGroup(): ModelGroup {
  return {
    id: '',
    name: '',
    enabled: true,
    models: [],
    strategy: 'round-robin',
    maxRetries: 3,
    retryInterval: 1000,
    maxConcurrency: 0,
    dailyLimitMaxRequests: 0,
    dailyLimitMaxTokens: 0,
    type: 'llm',
    maxTokens: 0,
    visionCapable: false,
    toolsCapable: false,
  }
}

export function GroupFormDialog({
  open,
  onOpenChange,
  group,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  group: ModelGroup | null
}) {
  const toast = useToast()
  const isEdit = !!group
  const { data: models } = useModels()
  const { data: sources } = useSources()
  const [form, setForm] = useState<ModelGroup>(emptyGroup())
  const [saving, setSaving] = useState(false)
  const [modelSearch, setModelSearch] = useState('')

  const enabledSourceIds = useMemo(() => {
    const set = new Set<string>()
    for (const s of sources ?? []) {
      if (s.enabled) set.add(s.id)
    }
    return set
  }, [sources])

  useEffect(() => {
    if (open) {
      setForm(group ? { ...group, models: [...group.models] } : emptyGroup())
      setModelSearch('')
    }
  }, [open, group])

  function update<K extends keyof ModelGroup>(key: K, value: ModelGroup[K]) {
    setForm((prev) => ({ ...prev, [key]: value }))
  }

  // 模型的复合身份键：sourceId:modelId。解决不同源同名模型被裸 id 联动选中的问题。
  function modelKey(model: Model): string {
    return `${model.sourceId ?? ''}:${model.id}`
  }

  function toggleModel(key: string) {
    setForm((prev) => ({
      ...prev,
      models: prev.models.includes(key) ? prev.models.filter((m) => m !== key) : [...prev.models, key],
    }))
  }

  const filteredModels = useMemo(() => {
    const list = (models ?? []).filter((m) => !m.sourceId || enabledSourceIds.has(m.sourceId))
    const kw = modelSearch.trim().toLowerCase()
    if (!kw) return list
    return list.filter((m) => `${m.id} ${m.name} ${m.sourceName ?? ''}`.toLowerCase().includes(kw))
  }, [models, enabledSourceIds, modelSearch])

  async function handleSave() {
    if (!form.name.trim()) {
      toast.error('请填写组名', '组名即客户端 /v1/models 看到的模型 ID')
      return
    }
    if (form.models.length === 0) {
      toast.error('请至少选择一个模型')
      return
    }
    setSaving(true)
    try {
      const payload: ModelGroup = {
        ...form,
        maxRetries: Math.max(0, form.maxRetries),
        retryInterval: Math.max(0, form.retryInterval),
      }
      if (isEdit && group) await api.updateGroup(group.id, payload)
      else await api.createGroup(payload)
      await revalidate.groups()
      toast.success(isEdit ? '模型组已更新' : '模型组已创建')
      onOpenChange(false)
    } catch (err) {
      toast.error('保存失败', (err as Error).message)
    } finally {
      setSaving(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-3xl">
        <DialogHeader>
          <DialogTitle>{isEdit ? '编辑模型组' : '新增模型组'}</DialogTitle>
          <DialogDescription>
            模型组名称是客户端请求时看到的模型 ID，组内模型按策略转发。
          </DialogDescription>
        </DialogHeader>

        <div className="grid gap-5">
          <div className="grid gap-4 sm:grid-cols-2">
            <div className="space-y-2">
              <Label required>组名（对外模型 ID）</Label>
              <Input value={form.name} placeholder="gpt-default" onChange={(e) => update('name', e.target.value)} />
            </div>
            <div className="space-y-2">
              <Label>类型</Label>
              <Select value={form.type} onValueChange={(v) => update('type', v as ModelType)}>
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

          {/* 模型多选 */}
          <div className="space-y-2">
            <div className="flex items-center justify-between">
              <Label required>组内模型</Label>
              <span className="text-xs text-muted-foreground">已选 {form.models.length} 个</span>
            </div>
            <div className="relative">
              <Search className="absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
              <Input
                className="pl-9"
                placeholder="搜索模型缓存"
                value={modelSearch}
                onChange={(e) => setModelSearch(e.target.value)}
              />
            </div>
            <div className="max-h-56 space-y-1 overflow-y-auto rounded-xl border border-border/70 bg-background/40 p-2">
              {filteredModels.length === 0 && (
                <p className="py-6 text-center text-sm text-muted-foreground">
                  无可选模型，请先在模型缓存页刷新
                </p>
              )}
              {filteredModels.map((model) => (
                <ModelOption
                  key={`${model.sourceId}-${model.id}`}
                  model={model}
                  selected={form.models.includes(modelKey(model))}
                  onToggle={() => toggleModel(modelKey(model))}
                />
              ))}
            </div>
            {/* 已选但不在缓存中的模型（例如手动模型 / 已删除源），按复合键比对 */}
            {form.models.filter((key) => !(models ?? []).some((m) => modelKey(m) === key)).length > 0 && (
              <div className="flex flex-wrap gap-1.5 pt-1">
                {form.models
                  .filter((key) => !(models ?? []).some((m) => modelKey(m) === key))
                  .map((key) => (
                    <Badge key={key} variant="outline" className="cursor-pointer" onClick={() => toggleModel(key)}>
                      {key.includes(':') ? key.slice(key.indexOf(':') + 1) : key} ✕
                    </Badge>
                  ))}
              </div>
            )}
          </div>

          {/* 策略与重试 */}
          <div className="grid gap-4 sm:grid-cols-3">
            <div className="space-y-2">
              <Label>策略</Label>
              <Select value={form.strategy} onValueChange={(v) => update('strategy', v as GroupStrategy)}>
                <SelectTrigger>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="round-robin">轮询 Round-robin</SelectItem>
                  <SelectItem value="sequential">顺序 Sequential</SelectItem>
                  <SelectItem value="random">随机 Random</SelectItem>
                </SelectContent>
              </Select>
            </div>
            <NumberField label="最大重试" value={form.maxRetries} onChange={(v) => update('maxRetries', v)} />
            <NumberField
              label="重试间隔 (ms)"
              value={form.retryInterval}
              onChange={(v) => update('retryInterval', v)}
            />
          </div>

          {/* 限流 */}
          <div className="grid gap-4 sm:grid-cols-3">
            <NumberField
              label="最大并发"
              hint="0 = 不限"
              value={form.maxConcurrency ?? 0}
              onChange={(v) => update('maxConcurrency', v)}
            />
            <NumberField
              label="每日请求上限"
              hint="0 = 不限"
              value={form.dailyLimitMaxRequests ?? 0}
              onChange={(v) => update('dailyLimitMaxRequests', v)}
            />
            <NumberField
              label="每日 Token 上限"
              hint="0 = 不限"
              value={form.dailyLimitMaxTokens ?? 0}
              onChange={(v) => update('dailyLimitMaxTokens', v)}
            />
          </div>

          <div className="grid gap-4 sm:grid-cols-2">
            <NumberField
              label="MaxTokens"
              hint="0 = 跟随请求"
              value={form.maxTokens ?? 0}
              onChange={(v) => update('maxTokens', v)}
            />
            <div className="flex items-end gap-6 pb-1">
              <label className="flex items-center gap-2">
                <Switch checked={form.visionCapable} onCheckedChange={(v) => update('visionCapable', v)} />
                <span className="text-sm font-medium">视觉</span>
              </label>
              <label className="flex items-center gap-2">
                <Switch checked={form.toolsCapable} onCheckedChange={(v) => update('toolsCapable', v)} />
                <span className="text-sm font-medium">工具</span>
              </label>
            </div>
          </div>

          <label className="flex items-center gap-3">
            <Switch checked={form.enabled} onCheckedChange={(v) => update('enabled', v)} />
            <span className="text-sm font-medium">启用此组（对外可见）</span>
          </label>
        </div>

        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)} disabled={saving}>
            取消
          </Button>
          <Button onClick={handleSave} disabled={saving}>
            {saving ? '保存中…' : '保存'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function ModelOption({
  model,
  selected,
  onToggle,
}: {
  model: Model
  selected: boolean
  onToggle: () => void
}) {
  return (
    <button
      type="button"
      onClick={onToggle}
      className={cn(
        'flex w-full items-center gap-3 rounded-lg px-2.5 py-2 text-left text-sm transition-colors',
        selected ? 'bg-primary/12 text-primary' : 'hover:bg-accent',
      )}
    >
      <span
        className={cn(
          'flex h-4 w-4 shrink-0 items-center justify-center rounded border',
          selected ? 'border-primary bg-primary text-primary-foreground' : 'border-border',
        )}
      >
        {selected && <Check className="h-3 w-3" />}
      </span>
      <span className="min-w-0 flex-1 truncate">{model.id}</span>
      {model.sourceName && <span className="shrink-0 text-xs text-muted-foreground">{model.sourceName}</span>}
    </button>
  )
}

function NumberField({
  label,
  hint,
  value,
  onChange,
}: {
  label: string
  hint?: string
  value: number
  onChange: (value: number) => void
}) {
  return (
    <div className="space-y-2">
      <Label>
        {label}
        {hint && <span className="ml-1 text-xs font-normal text-muted-foreground">{hint}</span>}
      </Label>
      <Input
        type="number"
        min={0}
        value={value}
        onChange={(e) => onChange(Math.max(0, Number(e.target.value) || 0))}
      />
    </div>
  )
}
