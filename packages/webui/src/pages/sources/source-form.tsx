import { useEffect, useState } from 'react'
import { ArrowDown, ArrowUp, Check, ChevronDown, Plus, Search, Trash2 } from 'lucide-react'
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
import { SecretInput } from '@/components/secret-input'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { useToast } from '@/components/ui/use-toast'
import { api } from '@/lib/api'
import { revalidate } from '@/lib/hooks'
import { cn } from '@/lib/utils'
import type { ManualModel, ModelSource, Platform, SourceAPIKey, SourceKeyStrategy } from '@/lib/types'

// 按「线路 API 协议」命名，取代旧的厂商混称（openai/openai-compatible/claude/gemini）。
// 选择 Responses API 表示上游端点类型；默认仍经过 Maheshvara，显式 relay.passthrough
// 才会启用同协议透传。
const PLATFORMS: { value: string; label: string; hint: string }[] = [
  { value: 'responses', label: 'Responses API', hint: '上游原生 Responses（默认经过 Maheshvara）' },
  { value: 'chat_completions', label: 'Chat Completions API', hint: 'OpenAI 兼容协议，最通用' },
  { value: 'anthropic', label: 'Anthropic API', hint: 'Claude /v1/messages' },
  { value: 'gemini', label: 'Gemini API', hint: 'Gemini /v1beta generateContent' },
  { value: 'custom', label: '自定义 Maheshvara 协议', hint: '使用 config.json 中注册的 customProtocols 协议 ID' },
]

// 把历史 platform 值归一化到新的四个 apiFormat，使旧源在新下拉里正确回显
// （与后端 NormalizeAPIFormat 保持一致）。
function normalizePlatform(raw: string | undefined): Platform {
  const normalized = (raw ?? '').trim().toLowerCase()
  if (normalized.startsWith('custom:')) return normalized as Platform
  switch (normalized) {
    case 'responses':
    case 'openai_responses':
      return 'responses'
    case 'anthropic':
    case 'claude':
      return 'anthropic'
    case 'gemini':
    case 'google':
      return 'gemini'
    default:
      // chat_completions / openai / openai-compatible / azure / deepseek / 空
      return 'chat_completions'
  }
}

function isCustomPlatform(platform: string): platform is `custom:${string}` {
  return platform.toLowerCase().startsWith('custom:')
}

function customProtocolID(platform: string): string {
  return isCustomPlatform(platform) ? platform.slice('custom:'.length).trim() : ''
}

const KEY_STRATEGIES: { value: SourceKeyStrategy; label: string; hint: string }[] = [
  { value: 'round-robin', label: '轮询 Round-robin', hint: '每次请求按顺序轮换 Key（仅一个 Key 时无差别）' },
  { value: 'random', label: '随机 Random', hint: '每次请求随机选取 Key' },
  { value: 'priority', label: '优先级 Priority', hint: '按列表顺序优先，失败先轮换 Key 再换模型' },
]

function emptySource(): ModelSource {
  return {
    id: '',
    name: '',
    baseUrl: '',
    apiKey: '',
    platform: 'chat_completions',
    enabled: true,
    autoFetchModels: true,
    manualModels: [],
    apiKeys: [],
    keyStrategy: 'round-robin',
  }
}

// 存量兼容：'single'/空策略归一化为「轮询」（单 Key 下三种策略行为完全等价）。
function normalizeKeyStrategy(raw: string | undefined): SourceKeyStrategy {
  return raw === 'random' || raw === 'priority' ? raw : 'round-robin'
}

export function SourceFormDialog({
  open,
  onOpenChange,
  source,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  source: ModelSource | null
}) {
  const toast = useToast()
  const isEdit = !!source
  const [form, setForm] = useState<ModelSource>(emptySource())
  const [saving, setSaving] = useState(false)
  // 「自定义模型拉取地址」开关（默认关闭）：关闭 = 拉取走 API 地址。
  const [customFetchEnabled, setCustomFetchEnabled] = useState(false)
  // a 方案：展开显示某个 key 拉取到的模型勾选面板（多 key 时）。
  const [expandedKey, setExpandedKey] = useState<number | null>(null)
  // b 方案：手动模式下每个手动模型选中的 key 下标集合（key 数 >1 时）。
  const [manualKeySelection, setManualKeySelection] = useState<Record<number, number[]>>({})

  const keyCount = (form.apiKeys ?? []).filter((k) => k.value.trim()).length

  useEffect(() => {
    if (open) {
      // 单 key 存量源：把旧 apiKey 带进列表第一行，自然成为单 key 配置。
      const existingKeys = source?.apiKeys ?? []
      const initialKeys =
        existingKeys.length > 0
          ? existingKeys.map((k) => ({ ...k }))
          : source?.apiKey
            ? [{ value: source.apiKey }]
            : []
      // 编辑时不回填明文 apiKey（后端按 secret 处理），留空表示保持原值；
      // platform 归一化到新 apiFormat，旧源（openai/claude…）也能正确回显。
      setForm(
        source
          ? {
              ...source,
              platform: normalizePlatform(source.platform),
              apiKey: '',
              manualModels: source.manualModels ?? [],
              apiKeys: initialKeys,
              keyStrategy: normalizeKeyStrategy(source.keyStrategy),
            }
          : emptySource(),
      )
      setCustomFetchEnabled(!!(source?.fetchBaseUrl ?? '').trim())
      // b 方案初始化：手动模式的「模型 ↔ key」选择。任何 key 都有显式
      // allowedModels 时按其还原；否则视为未配置（全部 key 选中）。
      const allKeyIndexes = (source?.apiKeys ?? [])
        .map((k, i) => (k.value.trim() ? i : -1))
        .filter((i) => i >= 0)
      const hasRestriction = (source?.apiKeys ?? []).some((k) => Array.isArray(k.allowedModels))
      const initialSelection: Record<number, number[]> = {}
      ;(source?.manualModels ?? []).forEach((model, index) => {
        if (hasRestriction && model.id) {
          const selected = (source?.apiKeys ?? [])
            .map((k, ki) =>
              k.value.trim() && (k.allowedModels ?? []).includes(model.id) ? ki : -1,
            )
            .filter((ki) => ki >= 0)
          initialSelection[index] = selected.length > 0 ? selected : allKeyIndexes
        } else {
          initialSelection[index] = allKeyIndexes
        }
      })
      setManualKeySelection(initialSelection)
      setExpandedKey(null)
    }
  }, [open, source])

  function update<K extends keyof ModelSource>(key: K, value: ModelSource[K]) {
    setForm((prev) => ({ ...prev, [key]: value }))
  }

  // ---- 多 key 编辑器（方向6） ----

  function updateApiKey(index: number, patch: Partial<SourceAPIKey>) {
    setForm((prev) => {
      const next = (prev.apiKeys ?? []).map((k, i) => (i === index ? { ...k, ...patch } : k))
      return { ...prev, apiKeys: next }
    })
  }

  function addApiKey() {
    setForm((prev) => ({ ...prev, apiKeys: [...(prev.apiKeys ?? []), { value: '' }] }))
  }

  function removeApiKey(index: number) {
    setForm((prev) => ({ ...prev, apiKeys: (prev.apiKeys ?? []).filter((_, i) => i !== index) }))
  }

  function moveApiKey(index: number, delta: -1 | 1) {
    setForm((prev) => {
      const keys = [...(prev.apiKeys ?? [])]
      const target = index + delta
      if (target < 0 || target >= keys.length) return prev
      ;[keys[index], keys[target]] = [keys[target], keys[index]]
      return { ...prev, apiKeys: keys }
    })
  }

  function setKeyStrategy(strategy: SourceKeyStrategy) {
    setForm((prev) => ({ ...prev, keyStrategy: strategy }))
  }

  // ---- 手动模型（autoFetch=false / custom） ----

  function addManualModel() {
    update('manualModels', [
      ...(form.manualModels ?? []),
      { id: '', name: '', type: 'llm', available: true },
    ])
    // 新手动模型默认由全部 key 服务（b 方案）。
    const allKeyIndexes = (form.apiKeys ?? [])
      .map((k, i) => (k.value.trim() ? i : -1))
      .filter((i) => i >= 0)
    setManualKeySelection((prev) => ({
      ...prev,
      [(form.manualModels ?? []).length]: allKeyIndexes,
    }))
  }

  function updateManualModel(index: number, patch: Partial<ManualModel>) {
    const next = [...(form.manualModels ?? [])]
    next[index] = { ...next[index], ...patch }
    update('manualModels', next)
  }

  function toggleManualKeySelection(modelIndex: number, keyIndex: number) {
    setManualKeySelection((prev) => {
      const current = prev[modelIndex] ?? []
      const next = current.includes(keyIndex)
        ? current.filter((i) => i !== keyIndex)
        : [...current, keyIndex]
      return { ...prev, [modelIndex]: next }
    })
  }

  function removeManualModel(index: number) {
    update(
      'manualModels',
      (form.manualModels ?? []).filter((_, i) => i !== index),
    )
    // 选择状态下标随行删除整体前移。
    setManualKeySelection((prev) => {
      const next: Record<number, number[]> = {}
      for (const [key, value] of Object.entries(prev)) {
        const i = Number(key)
        if (i === index) continue
        next[i > index ? i - 1 : i] = value
      }
      return next
    })
  }

  async function handleSave() {
    if (!form.name.trim() || !form.baseUrl.trim()) {
      toast.error('请完善必填项', 'name 与 baseUrl 不能为空')
      return
    }
    const custom = isCustomPlatform(form.platform)
    const protocolID = customProtocolID(form.platform)
    if (custom && !protocolID) {
      toast.error('请填写自定义协议 ID', '该 ID 必须与 config.json 的 customProtocols[].id 一致')
      return
    }
    // b 方案：手动模式 + 多 key 时，把「模型 ↔ key」选择编译为每个 key 的显式
    // allowedModels（无 nil 歧义）；单 key 或自动模式保持原值（自动模式的面板已
    // 直接编辑 allowedModels）。选择状态下标基于未过滤的原始数组，这里保持一致。
    let payloadKeys = (form.apiKeys ?? []).filter((k) => k.value.trim())
    const manualMode = custom || !form.autoFetchModels
    if (manualMode && payloadKeys.length > 1) {
      const allManual = form.manualModels ?? []
      for (const [index, model] of allManual.entries()) {
        if (!model.id.trim()) continue
        if ((manualKeySelection[index] ?? []).length === 0) {
          toast.error('请为每个手动模型至少选择一个 Key', `模型「${model.id}」没有任何可用 Key`)
          return
        }
      }
      // 有效 key 在原数组中的下标（manualKeySelection 记录的是原数组下标）。
      const keyOriginalIndexes = (form.apiKeys ?? [])
        .map((k, i) => (k.value.trim() ? i : -1))
        .filter((i) => i >= 0)
      payloadKeys = keyOriginalIndexes.map((originalIndex) => ({
        ...(form.apiKeys ?? [])[originalIndex],
        allowedModels: allManual
          .filter((m, i) => m.id.trim() && (manualKeySelection[i] ?? []).includes(originalIndex))
          .map((m) => m.id.trim()),
      }))
    }
    setSaving(true)
    try {
      const payload: ModelSource = {
        ...form,
        platform: custom ? (`custom:${protocolID}` as Platform) : form.platform,
        autoFetchModels: custom ? false : form.autoFetchModels,
        manualModels: custom || !form.autoFetchModels ? (form.manualModels ?? []).filter((m) => m.id || m.name) : [],
        // 关闭「自定义模型拉取地址」时不提交地址（后端空值 = 跟随 baseUrl）。
        fetchBaseUrl: customFetchEnabled ? form.fetchBaseUrl?.trim() ?? '' : '',
        // key 始终走列表（配一个 key 即单 key）；空列表 = 无鉴权源。
        apiKeys: payloadKeys,
      }
      // 编辑时若 apiKey 留空则不覆盖（作为旧数据的回退冗余字段，后端以 apiKeys 优先）。
      if (isEdit && !payload.apiKey) delete payload.apiKey
      if (isEdit && source) {
        await api.updateSource(source.id, payload)
        await revalidate.sources()
        await revalidate.models()
        toast.success('模型源已更新')
      } else {
        const created = await api.createSource(payload)
        await revalidate.sources()
        let backgroundFetch = false
        if (created.autoFetchModels && created.enabled) {
          // 拉取为后台任务：发起即返回，进度经源列表 refreshState 轮询，
          // 完成时由 sources 页统一弹结果提示。
          try {
            const result = await api.fetchSource(created.id)
            backgroundFetch = result.started || !!result.alreadyRunning
          } catch (fetchErr) {
            console.error('Auto fetch models failed:', fetchErr)
          }
        }
        await revalidate.models()
        toast.success(
          '模型源已创建',
          backgroundFetch ? '已在后台开始拉取模型' : undefined,
        )
      }
      onOpenChange(false)
    } catch (err) {
      toast.error('保存失败', (err as Error).message)
    } finally {
      setSaving(false)
    }
  }

  const custom = isCustomPlatform(form.platform)
  const selectedPlatform = custom ? 'custom' : form.platform
  const selectedStrategy = form.keyStrategy ?? 'round-robin'

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-2xl">
        <DialogHeader>
          <DialogTitle>{isEdit ? '编辑模型源' : '新增模型源'}</DialogTitle>
          <DialogDescription>
            配置上游供应商。开启自动拉取后将从供应商接口获取模型列表，否则使用手动模型表格。
          </DialogDescription>
        </DialogHeader>

        <div className="grid gap-4">
          <div className="grid gap-4 sm:grid-cols-2">
            <div className="space-y-2">
              <Label required>名称</Label>
              <Input
                value={form.name}
                placeholder="OpenAI Main"
                onChange={(e) => update('name', e.target.value)}
              />
            </div>
            <div className="space-y-2">
              <Label required>API 协议</Label>
              <Select
                value={selectedPlatform}
                onValueChange={(value) =>
                  setForm((previous) =>
                    value === 'custom'
                      ? { ...previous, platform: 'custom:', autoFetchModels: false }
                      : { ...previous, platform: value as Platform },
                  )
                }
              >
                <SelectTrigger>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {PLATFORMS.map((p) => (
                    <SelectItem key={p.value} value={p.value}>
                      {p.label}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
              <p className="text-xs text-muted-foreground">
                {PLATFORMS.find((p) => p.value === selectedPlatform)?.hint}
              </p>
              {custom && (
                <div className="space-y-2 pt-1">
                  <Label required>自定义协议 ID</Label>
                  <Input
                    value={customProtocolID(form.platform)}
                    placeholder="vendor-json"
                    onChange={(event) => update('platform', `custom:${event.target.value}` as Platform)}
                  />
                </div>
              )}
            </div>
          </div>

          <div className="space-y-2">
            <Label required>Base URL</Label>
            <Input
              value={form.baseUrl}
              placeholder="https://api.openai.com/v1"
              onChange={(e) => update('baseUrl', e.target.value)}
            />
          </div>

          <div className="space-y-2">
            <Label>Key 调度策略</Label>
            <Select value={selectedStrategy} onValueChange={(v) => setKeyStrategy(v as SourceKeyStrategy)}>
              <SelectTrigger>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {KEY_STRATEGIES.map((k) => (
                  <SelectItem key={k.value} value={k.value}>
                    {k.label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            <p className="text-xs text-muted-foreground">
              {KEY_STRATEGIES.find((k) => k.value === selectedStrategy)?.hint}
            </p>
          </div>

          <div className="space-y-2 rounded-xl border border-border/70 bg-background/40 p-4">
            <div className="flex items-center justify-between">
              <Label>
                API Keys（{selectedStrategy === 'priority' ? '按优先级从上到下' : '列表'}）
                <span className="ml-1 text-xs font-normal text-muted-foreground">配置一个即为单 Key</span>
              </Label>
              <Button type="button" variant="outline" size="sm" onClick={addApiKey}>
                <Plus className="h-4 w-4" /> 添加 Key
              </Button>
            </div>
            {(form.apiKeys ?? []).length === 0 && (
              <p className="py-2 text-center text-sm text-muted-foreground">
                尚无 Key（留空 = 无鉴权源）。列表顺序即优先级顺序（priority 策略）。
              </p>
            )}
            <div className="space-y-2">
              {(form.apiKeys ?? []).map((key, index) => (
                <div key={index} className="rounded-lg border border-border/60">
                  <div className="flex flex-wrap items-center gap-2 p-2">
                    <span className="w-6 shrink-0 text-center font-mono text-xs text-muted-foreground">
                      {index + 1}
                    </span>
                    <SecretInput
                      className="min-w-[160px] flex-1"
                      value={key.value}
                      placeholder={`sk-key-${index + 1}`}
                      onChange={(e) => updateApiKey(index, { value: e.target.value })}
                    />
                    <Input
                      className="w-28 shrink-0"
                      value={key.note ?? ''}
                      placeholder="备注"
                      onChange={(e) => updateApiKey(index, { note: e.target.value })}
                    />
                    <label className="flex shrink-0 items-center gap-1.5 text-xs text-muted-foreground">
                      <Switch
                        checked={!key.disabled}
                        onCheckedChange={(v) => updateApiKey(index, { disabled: !v })}
                      />
                      启用
                    </label>
                    <div className="flex shrink-0 items-center">
                      <Button
                        type="button"
                        variant="ghost"
                        size="iconSm"
                        title="上移"
                        disabled={index === 0}
                        onClick={() => moveApiKey(index, -1)}
                      >
                        <ArrowUp className="h-3.5 w-3.5" />
                      </Button>
                      <Button
                        type="button"
                        variant="ghost"
                        size="iconSm"
                        title="下移"
                        disabled={index === (form.apiKeys ?? []).length - 1}
                        onClick={() => moveApiKey(index, 1)}
                      >
                        <ArrowDown className="h-3.5 w-3.5" />
                      </Button>
                      <Button type="button" variant="ghost" size="iconSm" title="删除" onClick={() => removeApiKey(index)}>
                        <Trash2 className="h-3.5 w-3.5 text-destructive" />
                      </Button>
                    </div>
                  </div>
                  {keyCount > 1 && (
                    <>
                      <button
                        type="button"
                        className="flex w-full items-center gap-2 border-t border-border/60 px-3 py-1.5 text-left text-xs text-muted-foreground transition-colors hover:text-foreground"
                        onClick={() => setExpandedKey((prev) => (prev === index ? null : index))}
                      >
                        <ChevronDown
                          className={cn('h-3.5 w-3.5 transition-transform', expandedKey === index && 'rotate-180')}
                        />
                        模型权限
                        <KeyPermissionBadge apiKeyEntry={key} />
                      </button>
                      {expandedKey === index && (
                        <KeyModelsPanel
                          apiKeyEntry={key}
                          onChange={(allowed) => updateApiKey(index, { allowedModels: allowed })}
                        />
                      )}
                    </>
                  )}
                </div>
              ))}
            </div>
          </div>

          <div className="space-y-3">
            <div className="flex flex-wrap gap-6">
              <label className="flex items-center gap-3">
                <Switch checked={form.enabled} onCheckedChange={(v) => update('enabled', v)} />
                <span className="text-sm font-medium">启用此源</span>
              </label>
              <label className="flex items-center gap-3">
                <Switch
                  checked={form.autoFetchModels}
                  disabled={custom}
                  onCheckedChange={(v) => update('autoFetchModels', v)}
                />
                <span className="text-sm font-medium">自动拉取模型</span>
              </label>
              <label className="flex items-center gap-3">
                <Switch checked={customFetchEnabled} onCheckedChange={setCustomFetchEnabled} />
                <span className="text-sm font-medium">自定义模型拉取地址</span>
              </label>
            </div>
            {customFetchEnabled && (
              <div className="space-y-2">
                <Label required>模型拉取 Base URL</Label>
                <Input
                  value={form.fetchBaseUrl ?? ''}
                  placeholder="https://fetch-gateway.example.com/v1"
                  onChange={(e) => update('fetchBaseUrl', e.target.value)}
                />
                <p className="text-xs text-muted-foreground">
                  仅用于拉取模型列表，请求转发仍走上方 API 地址；按所选协议的约定拼接路径（OpenAI 系补
                  /models、Claude 补 /v1/models、Gemini 补 /v1beta/models），无需单独配置协议。
                </p>
              </div>
            )}
          </div>

          {!form.autoFetchModels && (
            <div className="space-y-2 rounded-xl border border-border/70 bg-background/40 p-4">
              <div className="flex items-center justify-between">
                <Label>手动模型</Label>
                <Button type="button" variant="outline" size="sm" onClick={addManualModel}>
                  <Plus className="h-4 w-4" /> 添加
                </Button>
              </div>
              {(form.manualModels ?? []).length === 0 && (
                <p className="py-3 text-center text-sm text-muted-foreground">尚无手动模型</p>
              )}
              <div className="space-y-2">
                {(form.manualModels ?? []).map((model, index) => (
                  <div key={index} className="space-y-1.5 rounded-lg border border-border/60 p-2">
                    <div className="flex flex-wrap items-center gap-2">
                      <Input
                        className="min-w-[140px] flex-1"
                        value={model.id}
                        placeholder="模型 ID"
                        onChange={(e) => updateManualModel(index, { id: e.target.value })}
                      />
                      <Input
                        className="min-w-[140px] flex-1"
                        value={model.name}
                        placeholder="显示名称"
                        onChange={(e) => updateManualModel(index, { name: e.target.value })}
                      />
                      <Select
                        value={model.type ?? 'llm'}
                        onValueChange={(v) => updateManualModel(index, { type: v as ManualModel['type'] })}
                      >
                        <SelectTrigger className="w-[130px]">
                          <SelectValue />
                        </SelectTrigger>
                        <SelectContent>
                          <SelectItem value="llm">LLM</SelectItem>
                          <SelectItem value="embedding">Embedding</SelectItem>
                          <SelectItem value="reranker">Reranker</SelectItem>
                        </SelectContent>
                      </Select>
                      <Button
                        type="button"
                        variant="ghost"
                        size="iconSm"
                        onClick={() => removeManualModel(index)}
                      >
                        <Trash2 className="h-4 w-4 text-destructive" />
                      </Button>
                    </div>
                    <div className="flex flex-wrap items-center gap-x-5 gap-y-1.5 px-1">
                      <label className="flex items-center gap-1.5 text-xs text-muted-foreground">
                        <Switch
                          checked={!!model.visionCapable}
                          onCheckedChange={(v) => updateManualModel(index, { visionCapable: v })}
                        />
                        视觉
                      </label>
                      <label className="flex items-center gap-1.5 text-xs text-muted-foreground">
                        <Switch
                          checked={!!model.toolsCapable}
                          onCheckedChange={(v) => updateManualModel(index, { toolsCapable: v })}
                        />
                        工具
                      </label>
                      <label className="flex items-center gap-1.5 text-xs text-muted-foreground">
                        <Switch
                          checked={!!model.structuredOutput}
                          onCheckedChange={(v) => updateManualModel(index, { structuredOutput: v })}
                        />
                        结构化输出
                      </label>
                      <Input
                        className="h-7 w-28"
                        type="number"
                        min={0}
                        value={model.maxTokens ?? 0}
                        placeholder="MaxTokens"
                        onChange={(e) =>
                          updateManualModel(index, { maxTokens: Math.max(0, Number(e.target.value) || 0) })
                        }
                      />
                    </div>
                    {keyCount > 1 && (
                      <div className="flex flex-wrap items-center gap-2 px-1 pb-1">
                        <span className="text-xs text-muted-foreground">可用 Key：</span>
                        {(form.apiKeys ?? []).map((sourceKey, keyIndex) =>
                          sourceKey.value.trim() ? (
                            <button
                              key={keyIndex}
                              type="button"
                              onClick={() => toggleManualKeySelection(index, keyIndex)}
                              className={cn(
                                'flex items-center gap-1.5 rounded-md border px-2 py-1 text-xs transition-colors',
                                (manualKeySelection[index] ?? []).includes(keyIndex)
                                  ? 'border-primary/50 bg-primary/10 text-primary'
                                  : 'border-border/60 text-muted-foreground hover:bg-accent',
                              )}
                            >
                              <span
                                className={cn(
                                  'flex h-3 w-3 items-center justify-center rounded border',
                                  (manualKeySelection[index] ?? []).includes(keyIndex)
                                    ? 'border-primary bg-primary text-primary-foreground'
                                    : 'border-border',
                                )}
                              >
                                {(manualKeySelection[index] ?? []).includes(keyIndex) && (
                                  <Check className="h-2 w-2" />
                                )}
                              </span>
                              Key {keyIndex + 1}
                              {sourceKey.note ? ` · ${sourceKey.note}` : ''}
                            </button>
                          ) : null,
                        )}
                      </div>
                    )}
                  </div>
                ))}
              </div>
            </div>
          )}
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

// KeyPermissionBadge 显示 key 的模型权限状态：
// 已拉取 → 「已启用 x/y」；未做过按 key 拉取 → 「未拉取 · 不限制」。
function KeyPermissionBadge({ apiKeyEntry }: { apiKeyEntry: SourceAPIKey }) {
  const fetched = apiKeyEntry.fetchedModels ?? []
  if (fetched.length === 0) {
    return <span className="rounded bg-muted px-1.5 py-0.5 text-2xs">未拉取 · 不限制</span>
  }
  const enabled = apiKeyEntry.allowedModels ?? fetched
  return (
    <span className="rounded bg-muted px-1.5 py-0.5 text-2xs">
      已启用 {enabled.length}/{fetched.length}
    </span>
  )
}

// KeyModelsPanel 是 a 方案的 per-key 模型勾选面板：展示该 key 独立拉取到的模型
// （权限自动发现结果），勾选 = allowedModels；搜索 + 全选/反选。勾选变动即写入
// 显式 allowedModels（undefined → 显式列表），空数组 = 全部停用。
function KeyModelsPanel({
  apiKeyEntry,
  onChange,
}: {
  apiKeyEntry: SourceAPIKey
  onChange: (allowed: string[]) => void
}) {
  const [search, setSearch] = useState('')
  const fetched = apiKeyEntry.fetchedModels ?? []
  const enabled = apiKeyEntry.allowedModels ?? fetched

  const keyword = search.trim().toLowerCase()
  const visible = keyword ? fetched.filter((id) => id.toLowerCase().includes(keyword)) : fetched

  function toggle(id: string) {
    onChange(enabled.includes(id) ? enabled.filter((x) => x !== id) : [...enabled, id])
  }

  return (
    <div className="space-y-2 border-t border-border/60 p-3">
      <div className="flex flex-wrap items-center gap-2">
        <div className="relative w-44">
          <Search className="absolute left-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-muted-foreground" />
          <Input
            className="h-7 pl-8 text-xs"
            placeholder="搜索模型…"
            value={search}
            onChange={(e) => setSearch(e.target.value)}
          />
        </div>
        <Button
          type="button"
          variant="ghost"
          size="sm"
          className="h-7 text-xs"
          onClick={() => onChange(fetched.slice())}
        >
          全选
        </Button>
        <Button
          type="button"
          variant="ghost"
          size="sm"
          className="h-7 text-xs"
          onClick={() => onChange(fetched.filter((id) => !enabled.includes(id)))}
        >
          反选
        </Button>
        <span className="ml-auto text-2xs text-muted-foreground">
          该 key 拉取到的模型即其分组权限；取消勾选后此 key 不再服务对应模型
        </span>
      </div>
      {visible.length === 0 ? (
        <p className="py-2 text-center text-xs text-muted-foreground">没有匹配的模型</p>
      ) : (
        <div className="grid gap-1 sm:grid-cols-2 lg:grid-cols-3">
          {visible.map((id) => {
            const checked = enabled.includes(id)
            return (
              <button
                key={id}
                type="button"
                onClick={() => toggle(id)}
                className={cn(
                  'flex items-center gap-2 rounded-md border px-2 py-1 text-left text-xs transition-colors',
                  checked ? 'border-primary/50 bg-primary/10 text-primary' : 'border-border/60 hover:bg-accent',
                )}
              >
                <span
                  className={cn(
                    'flex h-3.5 w-3.5 shrink-0 items-center justify-center rounded border',
                    checked ? 'border-primary bg-primary text-primary-foreground' : 'border-border',
                  )}
                >
                  {checked && <Check className="h-2.5 w-2.5" />}
                </span>
                <span className="truncate font-mono">{id}</span>
              </button>
            )
          })}
        </div>
      )}
    </div>
  )
}
