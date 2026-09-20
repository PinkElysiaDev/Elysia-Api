import { Brain, Loader2, Settings2 } from 'lucide-react'
import { useState } from 'react'
import { Label } from '@/components/ui/label'
import { Seg } from '@/components/ui/seg'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Switch } from '@/components/ui/switch'
import { useModels, useSources } from '@/lib/hooks'
import type { AgentPermission, AgentSession, AgentSettings, AgentThinkingEffort } from '@/lib/agent/types'
import { cn } from '@/lib/utils'

const THINKING_EFFORTS: { value: AgentThinkingEffort; label: string }[] = [
  { value: '', label: '默认' },
  { value: 'low', label: '低' },
  { value: 'medium', label: '中' },
  { value: 'high', label: '高' },
  { value: 'max', label: '最高' },
  { value: 'adaptive', label: '自适应' },
]

const PERMISSION_OPTIONS: { value: AgentPermission; label: string }[] = [
  { value: 'ask', label: '询问' },
  { value: 'always', label: '总是允许' },
  { value: 'never', label: '禁止' },
]

/**
 * 会话设置行：模型 / 思考 / 写入与出站权限。修改即保存。
 * 测试凭证不再在此配置——用户在对话中告知，助手作为工具参数传入并自动记住。
 */
export function SettingsBar({
  session,
  disabled,
  onChange,
}: {
  session: AgentSession
  disabled?: boolean
  onChange: (patch: { settings?: Partial<AgentSettings>; title?: string }) => Promise<void> | void
}) {
  const { data: sources } = useSources()
  const { data: models } = useModels()
  const settings = session.settings
  const [saving, setSaving] = useState(false)

  const llmModels = (models ?? []).filter(
    (model) =>
      (!settings.modelSourceId || model.sourceId === settings.modelSourceId) &&
      model.enabled &&
      (model.type === 'llm' || !model.type),
  )

  const save = async (patch: { settings?: Partial<AgentSettings> }) => {
    setSaving(true)
    try {
      await onChange(patch)
    } finally {
      setSaving(false)
    }
  }

  return (
    <div className="flex flex-wrap items-center gap-x-4 gap-y-2 border-b border-border bg-card/60 px-4 py-2 text-xs">
      <div className="flex min-w-0 items-center gap-1.5">
        <Select
          value={settings.modelSourceId || undefined}
          disabled={disabled}
          onValueChange={(value) => void save({ settings: { modelSourceId: value, modelName: '' } })}
        >
          <SelectTrigger className="h-7 w-36 text-xs">
            <SelectValue placeholder="选择模型源" />
          </SelectTrigger>
          <SelectContent>
            {(sources ?? []).map((source) => (
              <SelectItem key={source.id} value={source.id} className="text-xs">
                {source.name || source.id}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        <Select
          value={settings.modelName || undefined}
          disabled={disabled || !settings.modelSourceId}
          onValueChange={(value) => void save({ settings: { modelName: value } })}
        >
          <SelectTrigger className="h-7 w-40 text-xs">
            <SelectValue placeholder="选择模型" />
          </SelectTrigger>
          <SelectContent>
            {llmModels.map((model) => (
              <SelectItem key={`${model.sourceId}/${model.id}`} value={model.name} className="text-xs">
                {model.name}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </div>

      <div className="flex items-center gap-1.5">
        <Brain className="h-3.5 w-3.5 text-muted-foreground" />
        <Switch
          id="agent-thinking"
          checked={settings.thinkingEnabled}
          disabled={disabled}
          onCheckedChange={(checked) => void save({ settings: { thinkingEnabled: checked } })}
          className="scale-90"
        />
        <Label htmlFor="agent-thinking" className="cursor-pointer text-2xs text-muted-foreground">
          思考
        </Label>
        {settings.thinkingEnabled ? (
          <Select
            value={settings.thinkingEffort || 'default'}
            disabled={disabled}
            onValueChange={(value) => void save({ settings: { thinkingEffort: (value === 'default' ? '' : value) as AgentThinkingEffort } })}
          >
            <SelectTrigger className="h-7 w-[76px] text-xs">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {THINKING_EFFORTS.map((effort) => (
                <SelectItem key={effort.value || 'default'} value={effort.value || 'default'} className="text-xs">
                  {effort.label}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        ) : null}
      </div>

      <div className={cn('flex items-center gap-1.5', disabled && 'pointer-events-none opacity-60')}>
        <Settings2 className="h-3.5 w-3.5 text-muted-foreground" />
        <span className="text-2xs text-muted-foreground" title="保存协议、创建/修改模型源与模型组等持久化写操作">
          写入
        </span>
        <Seg<AgentPermission>
          size="sm"
          aria-label="写入权限"
          options={PERMISSION_OPTIONS}
          value={settings.allowSave || 'ask'}
          onChange={(value) => void save({ settings: { allowSave: value } })}
        />
        <span className="ml-1 text-2xs text-muted-foreground" title="真实上游测试与模型列表拉取等出站请求">
          出站
        </span>
        <Seg<AgentPermission>
          size="sm"
          aria-label="出站权限"
          options={PERMISSION_OPTIONS}
          value={settings.allowLiveTest || 'ask'}
          onChange={(value) => void save({ settings: { allowLiveTest: value } })}
        />
      </div>

      <span
        className={cn(
          'ml-auto flex items-center gap-1 text-2xs text-muted-foreground transition-opacity',
          saving ? 'opacity-100' : 'opacity-0',
        )}
      >
        <Loader2 className="h-3 w-3 animate-spin" /> 保存中
      </span>
    </div>
  )
}
