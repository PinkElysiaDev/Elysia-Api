import { useState } from 'react'
import { KeyRound, PlugZap, Plus, Trash2 } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { SettingSection, SettingRow } from '@/components/ui/setting-card'
import { CopyButton } from '@/components/copy-button'
import { useConfirm } from '@/components/ui/confirm-dialog'
import { useToast } from '@/components/ui/use-toast'
import { useTokens } from '@/lib/hooks'
import { api } from '@/lib/api'
import type { RuntimeConfigForm } from './use-runtime-config-form'

/** AI 助手远程访问配置区（运行配置页）：开关/对外地址 + 接入信息 +
 * agent 作用域访问令牌的集中管理。 */

// 与后端 generateAPIKeySecret 同口径：32 字节 URL-safe base64。
function generateRemoteKeySecret(): string {
  const bytes = new Uint8Array(32)
  crypto.getRandomValues(bytes)
  let binary = ''
  for (const byte of bytes) binary += String.fromCharCode(byte)
  return btoa(binary).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '')
}

export function AgentRemoteSection({
  form,
  onToggle,
  onPublicUrlChange,
}: {
  form: RuntimeConfigForm
  onToggle: (enabled: boolean) => void
  onPublicUrlChange: (url: string) => void
}) {
  const enabled = form.agentRemote.enabled
  const publicBase =
    form.agentRemote.publicUrl.trim() || window.location.origin

  return (
    <SettingSection
      icon={PlugZap}
      title="AI 助手远程访问"
      description="通过 REST / MCP / A2A 把内置 AI 助手暴露给外部程序远程驱动与配置"
    >
      <div className="space-y-4">
        <SettingRow
          label="启用远程访问"
          description="关闭后 /api/agent、/mcp、/a2a 三个入口全部下线（404）；保存后即时生效"
        >
          <Switch checked={enabled} onCheckedChange={onToggle} />
        </SettingRow>

        <SettingRow
          label="对外基础地址"
          description="反向代理后填写（如 https://gw.example.com），供 Agent Card 生成绝对地址；留空按当前访问地址推导"
          inline={false}
        >
          <Input
            className="w-full font-mono text-xs"
            value={form.agentRemote.publicUrl}
            placeholder={window.location.origin}
            onChange={(e) => onPublicUrlChange(e.target.value)}
          />
        </SettingRow>

        <div className="border-t border-border/40 pt-3 space-y-2">
          <p className="text-xs font-medium text-muted-foreground">接入信息</p>
          {enabled ? (
            <>
              <EndpointRow label="MCP" path="/mcp" base={publicBase} />
              <EndpointRow label="A2A" path="/a2a" base={publicBase} />
              <EndpointRow label="Agent Card" path="/.well-known/agent-card.json" base={publicBase} />
              <EndpointRow label="REST" path="/api/agent" base={publicBase} />
              <p className="text-2xs text-muted-foreground/70">
                调用以上端点需携带开启「允许控制 AI 助手」作用的 API
                Key（Bearer）；助手自身的写操作仍按会话权限档逐次确认。
              </p>
            </>
          ) : (
            <p className="text-2xs text-muted-foreground">
              远程访问已停用，三个入口均返回 404。开启后此处展示接入地址。
            </p>
          )}
        </div>

        <AgentRemoteTokens />
      </div>
    </SettingSection>
  )
}

function EndpointRow({ label, path, base }: { label: string; path: string; base: string }) {
  const value = `${base.replace(/\/$/, '')}${path}`
  return (
    <div className="flex items-center gap-2">
      <span className="w-20 shrink-0 text-xs text-muted-foreground">{label}</span>
      <code className="min-w-0 flex-1 truncate rounded bg-muted px-2 py-1 font-mono text-2xs text-foreground">
        {value}
      </code>
      <CopyButton value={value} size="iconSm" variant="outline" title="复制地址" />
    </div>
  )
}

/** agent 作用域访问令牌的集中管理：列表 / 启停 / 删除 / 快捷创建。 */
function AgentRemoteTokens() {
  const toast = useToast()
  const { confirm, dialog } = useConfirm()
  const { data: tokens, mutate } = useTokens()
  const [newName, setNewName] = useState('')
  const [creating, setCreating] = useState(false)
  const [createdSecret, setCreatedSecret] = useState<{ name: string; secret: string } | null>(null)

  const agentKeys = (tokens ?? []).filter((token) => (token.scopes ?? []).includes('agent'))

  async function handleCreate() {
    const name = newName.trim()
    if (!name) {
      toast.error('请填写名称')
      return
    }
    setCreating(true)
    try {
      const secret = generateRemoteKeySecret()
      await api.createToken({ name, token: secret, enabled: true, scopes: ['agent'] })
      await mutate()
      setNewName('')
      setCreatedSecret({ name, secret })
    } catch (err) {
      toast.error('创建失败', (err as Error).message)
    } finally {
      setCreating(false)
    }
  }

  async function handleToggle(name: string, enabled: boolean, scopes: string[]) {
    try {
      await api.updateToken(name, { name, enabled, scopes, allowedGroups: [] })
      await mutate()
    } catch (err) {
      toast.error('更新失败', (err as Error).message)
    }
  }

  async function handleDelete(name: string) {
    const okToDelete = await confirm({
      title: `删除 API Key「${name}」？`,
      description: '使用该 Key 的外部客户端将立即失去远程访问能力（推理调用一并失效）。',
      confirmText: '删除',
    })
    if (!okToDelete) return
    try {
      await api.deleteToken(name)
      await mutate()
      toast.success('API Key 已删除')
    } catch (err) {
      toast.error('删除失败', (err as Error).message)
    }
  }

  return (
    <div className="border-t border-border/40 pt-3 space-y-3">
      <div className="flex items-center justify-between">
        <p className="text-xs font-medium text-muted-foreground flex items-center gap-1.5">
          <KeyRound className="h-3.5 w-3.5" /> 远程访问 Key（{agentKeys.length}）
        </p>
      </div>

      {agentKeys.length === 0 ? (
        <p className="text-2xs text-muted-foreground">
          还没有可远程控制的 API Key。创建一个，或在「API Key」页为现有 Key 开启作用域。
        </p>
      ) : (
        <div className="space-y-1.5">
          {agentKeys.map((token) => (
            <div key={token.name} className="flex items-center gap-2">
              <Switch
                checked={token.enabled}
                onCheckedChange={(v) => void handleToggle(token.name, v, token.scopes ?? ['agent'])}
              />
              <span className="w-28 shrink-0 truncate text-xs font-medium">{token.name}</span>
              <code className="min-w-0 flex-1 truncate font-mono text-2xs text-muted-foreground">
                {token.token}
              </code>
              <Button
                variant="ghost"
                size="iconSm"
                title="删除"
                onClick={() => void handleDelete(token.name)}
              >
                <Trash2 className="h-3.5 w-3.5" />
              </Button>
            </div>
          ))}
        </div>
      )}

      <div className="flex items-center gap-2">
        <Input
          className="h-8 flex-1 text-xs"
          value={newName}
          placeholder="新 Key 名称（如 cursor、ops-agent）"
          onChange={(e) => setNewName(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter') void handleCreate()
          }}
        />
        <Button variant="outline" size="sm" disabled={creating} onClick={() => void handleCreate()}>
          <Plus className="mr-1 h-3.5 w-3.5" /> 新建
        </Button>
      </div>
      <p className="text-2xs text-muted-foreground/70">
        完整 Key 管理（模型组授权、明文录入）在「API Key」页。
      </p>

      {dialog}

      <Dialog open={createdSecret !== null} onOpenChange={(open) => !open && setCreatedSecret(null)}>
        <DialogContent className="max-w-md">
          <DialogHeader>
            <DialogTitle>远程访问 Key 已创建</DialogTitle>
            <DialogDescription>
              明文仅此一次显示，请立即复制保存（「{createdSecret?.name}」，已授予控制 AI
              助手的作用域）。
            </DialogDescription>
          </DialogHeader>
          <div className="flex items-center gap-2">
            <code className="min-w-0 flex-1 truncate rounded bg-muted px-2.5 py-2 font-mono text-xs">
              {createdSecret?.secret}
            </code>
            <CopyButton value={createdSecret?.secret ?? ''} title="复制" />
          </div>
          <DialogFooter>
            <Button variant="primary" onClick={() => setCreatedSecret(null)}>
              已保存
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  )
}
