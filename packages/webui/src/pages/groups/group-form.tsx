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
import { CapChip } from '@/components/badges'
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

function sourceIdFromKey(key: string): string {
  const idx = key.indexOf(':')
  return idx >= 0 ? key.slice(0, idx) : ''
}

function modelIdFromKey(key: string): string {
  const idx = key.indexOf(':')
  return idx >= 0 ? key.slice(idx + 1) : key
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
  // 模型列表筛选：能力（vision/tools/structured）与模型源；与搜索词叠加。
  const [capFilter, setCapFilter] = useState<'all' | 'vision' | 'tools' | 'structured'>('all')
  const [sourceFilter, setSourceFilter] = useState<string>('all')
  // 方向1：能力开关是否被用户手动改过。未改过时，选中成员变化会按成员能力
  // 重新推导预填（vision=任一支持、tools=任一支持）；一旦手动切换即固定。
  const [capsTouched, setCapsTouched] = useState(false)

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
      setCapFilter('all')
      setSourceFilter('all')
      setCapsTouched(false)
    }
  }, [open, group])

  function update<K extends keyof ModelGroup>(key: K, value: ModelGroup[K]) {
    setForm((prev) => ({ ...prev, [key]: value }))
  }

  function updateCapability<K extends 'visionCapable' | 'toolsCapable'>(key: K, value: ModelGroup[K]) {
    setCapsTouched(true)
    update(key, value)
  }

  // 模型的复合身份键：sourceId:modelId。解决不同源同名模型被裸 id 联动选中的问题。
  function modelKey(model: Model): string {
    return `${model.sourceId ?? ''}:${model.id}`
  }

  function toggleModel(key: string) {
    setForm((prev) => {
      const selected = prev.models.includes(key)
        ? prev.models.filter((m) => m !== key)
        : [...prev.models, key]
      // 未手动改过能力开关时，按新的选中集合推导预填（方向1）：
      // 视觉与工具均为「任一成员支持即开启」。
      if (capsTouched) return { ...prev, models: selected }
      const members = (models ?? []).filter((m) => selected.includes(modelKey(m)))
      return {
        ...prev,
        models: selected,
        visionCapable: members.some((m) => m.visionCapable),
        toolsCapable: members.some((m) => m.toolsCapable),
      }
    })
  }

  const filteredModels = useMemo(() => {
    // 排除禁用模型（enabled=false 不参与调度，选进组是无效配置）与停用源下的模型。
    const list = (models ?? []).filter(
      (m) => m.enabled !== false && (!m.sourceId || enabledSourceIds.has(m.sourceId)),
    )
    const kw = modelSearch.trim().toLowerCase()
    const matched = list.filter((m) => {
      if (kw && !`${m.id} ${m.name} ${m.sourceName ?? ''}`.toLowerCase().includes(kw)) return false
      if (sourceFilter !== 'all' && (m.sourceId ?? '') !== sourceFilter) return false
      if (capFilter === 'vision' && !m.visionCapable) return false
      if (capFilter === 'tools' && !m.toolsCapable) return false
      if (capFilter === 'structured' && !m.structuredOutput) return false
      return true
    })
    // 已选模型稳定前置：命中列表中已选的排最前（各分区内部保持缓存顺序）。
    const selectedKeys = new Set(form.models)
    const chosen: Model[] = []
    const rest: Model[] = []
    for (const m of matched) {
      (selectedKeys.has(modelKey(m)) ? chosen : rest).push(m)
    }
    return [...chosen, ...rest]
  }, [models, enabledSourceIds, modelSearch, capFilter, sourceFilter, form.models])

  // 已选但被上方过滤隐藏的模型：模型级停用，或所属源已停用。
  // ListModels 不返回停用源下的模型，因此除缓存命中外，还要用 sources 判断
  // 复合键里的 sourceId 是否已停用，否则编辑页会把这些引用当成「不在缓存」。
  const excludedSelected = useMemo(() => {
    const items: { key: string; label: string }[] = []
    for (const key of form.models) {
      const model = (models ?? []).find((m) => modelKey(m) === key)
      if (model) {
        if (model.enabled === false || (!!model.sourceId && !enabledSourceIds.has(model.sourceId))) {
          items.push({ key, label: model.name || model.id })
        }
        continue
      }
      const sourceId = sourceIdFromKey(key)
      if (sourceId && (sources ?? []).some((s) => s.id === sourceId && !s.enabled)) {
        items.push({ key, label: modelIdFromKey(key) })
      }
    }
    return items
  }, [models, sources, enabledSourceIds, form.models])

  const missingSelected = useMemo(() => {
    const excluded = new Set(excludedSelected.map((e) => e.key))
    return form.models.filter(
      (key) => !excluded.has(key) && !(models ?? []).some((m) => modelKey(m) === key),
    )
  }, [form.models, models, excludedSelected])

  // 模型源筛选项：启用源（与列表可选范围一致）。
  const sourceOptions = useMemo(
    () => (sources ?? []).filter((s) => s.enabled),
    [sources],
  )

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
              <Label required>组名</Label>
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
            <div className="flex flex-wrap items-center gap-2">
              <div className="relative w-44">
                <Search className="absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
                <Input
                  className="h-9 pl-9"
                  placeholder="搜索模型缓存"
                  value={modelSearch}
                  onChange={(e) => setModelSearch(e.target.value)}
                />
              </div>
              <Select value={capFilter} onValueChange={(v) => setCapFilter(v as typeof capFilter)}>
                <SelectTrigger className="h-9 w-[118px]">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="all">全部能力</SelectItem>
                  <SelectItem value="vision">视觉</SelectItem>
                  <SelectItem value="tools">工具</SelectItem>
                  <SelectItem value="structured">结构化输出</SelectItem>
                </SelectContent>
              </Select>
              <Select value={sourceFilter} onValueChange={setSourceFilter}>
                <SelectTrigger className="h-9 w-[150px]">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="all">全部模型源</SelectItem>
                  {sourceOptions.map((s) => (
                    <SelectItem key={s.id} value={s.id}>
                      {s.name}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
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
            {missingSelected.length > 0 && (
              <div className="flex flex-wrap gap-1.5 pt-1">
                {missingSelected.map((key) => (
                  <Badge key={key} variant="outline" className="cursor-pointer" onClick={() => toggleModel(key)}>
                    {modelIdFromKey(key)} ✕
                  </Badge>
                ))}
              </div>
            )}
            {/* 已选但已停用（模型级停用或所属源停用）：不进可选列表，但保留徽标供查看/移除 */}
            {excludedSelected.length > 0 && (
              <div className="flex flex-wrap gap-1.5 pt-1">
                {excludedSelected.map((item) => (
                  <Badge
                    key={item.key}
                    variant="outline"
                    className="cursor-pointer text-muted-foreground"
                    onClick={() => toggleModel(item.key)}
                  >
                    {item.label}（已停用）✕
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
            <div className="space-y-2">
              <Label>能力</Label>
              <div className="flex h-10 items-center gap-6">
                <label className="flex items-center gap-2">
                  <Switch checked={form.visionCapable} onCheckedChange={(v) => updateCapability('visionCapable', v)} />
                  <span className="text-sm font-medium">视觉</span>
                </label>
                <label className="flex items-center gap-2">
                  <Switch checked={form.toolsCapable} onCheckedChange={(v) => updateCapability('toolsCapable', v)} />
                  <span className="text-sm font-medium">工具</span>
                </label>
              </div>
            </div>
          </div>

          <label className="flex items-center gap-3">
            <Switch checked={form.enabled} onCheckedChange={(v) => update('enabled', v)} />
            <span className="text-sm font-medium">启用此组</span>
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
      <span className="flex shrink-0 items-center gap-1">
        {model.visionCapable && <CapChip>视觉</CapChip>}
        {model.toolsCapable && <CapChip>工具</CapChip>}
        {model.structuredOutput && <CapChip>结构化</CapChip>}
      </span>
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
