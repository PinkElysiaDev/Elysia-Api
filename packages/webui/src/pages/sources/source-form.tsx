import { useEffect, useState } from 'react'
import { Plus, Trash2 } from 'lucide-react'
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
import type { ManualModel, ModelSource, Platform } from '@/lib/types'

// 按「线路 API 协议」命名，取代旧的厂商混称（openai/openai-compatible/claude/gemini）。
// 选 Responses API 即触发透传（不做格式转换），适合明确知道上游支持 Responses 的场景。
const PLATFORMS: { value: Platform; label: string; hint: string }[] = [
  { value: 'responses', label: 'Responses API', hint: '上游原生 Responses（codex 直连，透传不转换）' },
  { value: 'chat_completions', label: 'Chat Completions API', hint: 'OpenAI 兼容协议，最通用' },
  { value: 'anthropic', label: 'Anthropic API', hint: 'Claude /v1/messages' },
  { value: 'gemini', label: 'Gemini API', hint: 'Gemini /v1beta generateContent' },
]

// 把历史 platform 值归一化到新的四个 apiFormat，使旧源在新下拉里正确回显
// （与后端 NormalizeAPIFormat 保持一致）。
function normalizePlatform(raw: string | undefined): Platform {
  switch ((raw ?? '').toLowerCase()) {
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
  }
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

  useEffect(() => {
    if (open) {
      // 编辑时不回填明文 apiKey（后端按 secret 处理），留空表示保持原值；
      // platform 归一化到新 apiFormat，旧源（openai/claude…）也能正确回显。
      setForm(
        source
          ? { ...source, platform: normalizePlatform(source.platform), apiKey: '', manualModels: source.manualModels ?? [] }
          : emptySource(),
      )
    }
  }, [open, source])

  function update<K extends keyof ModelSource>(key: K, value: ModelSource[K]) {
    setForm((prev) => ({ ...prev, [key]: value }))
  }

  function addManualModel() {
    update('manualModels', [
      ...(form.manualModels ?? []),
      { id: '', name: '', type: 'llm', available: true },
    ])
  }

  function updateManualModel(index: number, patch: Partial<ManualModel>) {
    const next = [...(form.manualModels ?? [])]
    next[index] = { ...next[index], ...patch }
    update('manualModels', next)
  }

  function removeManualModel(index: number) {
    update(
      'manualModels',
      (form.manualModels ?? []).filter((_, i) => i !== index),
    )
  }

  async function handleSave() {
    if (!form.name.trim() || !form.baseUrl.trim()) {
      toast.error('请完善必填项', 'name 与 baseUrl 不能为空')
      return
    }
    setSaving(true)
    try {
      const payload: ModelSource = {
        ...form,
        manualModels: form.autoFetchModels ? [] : (form.manualModels ?? []).filter((m) => m.id || m.name),
      }
      // 编辑时若 apiKey 留空则不覆盖
      if (isEdit && !payload.apiKey) delete payload.apiKey
      if (isEdit && source) {
        await api.updateSource(source.id, payload)
      } else {
        await api.createSource(payload)
      }
      await revalidate.sources()
      toast.success(isEdit ? '模型源已更新' : '模型源已创建')
      onOpenChange(false)
    } catch (err) {
      toast.error('保存失败', (err as Error).message)
    } finally {
      setSaving(false)
    }
  }

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
              <Select value={form.platform} onValueChange={(v) => update('platform', v as Platform)}>
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
                {PLATFORMS.find((p) => p.value === form.platform)?.hint}
              </p>
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
            <Label>API Key</Label>
            <SecretInput
              value={form.apiKey ?? ''}
              placeholder={isEdit ? '留空则保持原密钥不变' : 'sk-…'}
              onChange={(e) => update('apiKey', e.target.value)}
            />
          </div>

          <div className="flex flex-wrap gap-6">
            <label className="flex items-center gap-3">
              <Switch checked={form.enabled} onCheckedChange={(v) => update('enabled', v)} />
              <span className="text-sm font-medium">启用此源</span>
            </label>
            <label className="flex items-center gap-3">
              <Switch checked={form.autoFetchModels} onCheckedChange={(v) => update('autoFetchModels', v)} />
              <span className="text-sm font-medium">自动拉取模型</span>
            </label>
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
                  <div key={index} className="flex flex-wrap items-center gap-2">
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
