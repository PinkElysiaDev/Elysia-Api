import { useEffect, useState } from 'react'
import { Check, Copy, Eye, EyeOff, KeyRound, Pencil, Plus, Trash2 } from 'lucide-react'
import { PageHeader } from '@/components/page-header'
import { RoleWatermark } from '@/components/role-watermark'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { AsyncState } from '@/components/ui/states'
import { CapChip } from '@/components/badges'
import { SecretInput } from '@/components/secret-input'
import { CopyButton } from '@/components/copy-button'
import { useConfirm } from '@/components/ui/confirm-dialog'
import { useToast } from '@/components/ui/use-toast'
import { useTokens, useGroups, revalidate } from '@/lib/hooks'
import { api } from '@/lib/api'
import { cn, formatDateTime } from '@/lib/utils'
import type { ApiToken } from '@/lib/types'

export function TokensPage() {
  const toast = useToast()
  const { confirm, dialog } = useConfirm()
  const { data, isLoading, error, mutate } = useTokens()
  const { data: groups } = useGroups()
  const [editing, setEditing] = useState<ApiToken | null>(null)
  const [formOpen, setFormOpen] = useState(false)
  const [switchBusy, setSwitchBusy] = useState<string | null>(null)

  // 当前存在的模型组名集合，用于标记列表中已失效的组名。
  const validGroupNames = new Set((groups ?? []).map((g) => g.name))

  function openCreate() {
    setEditing(null)
    setFormOpen(true)
  }

  function openEdit(token: ApiToken) {
    setEditing(token)
    setFormOpen(true)
  }

  async function toggleToken(token: ApiToken) {
    setSwitchBusy(token.name)
    try {
      // 只提交启停所需字段，绝不回传列表里的 token——那是脱敏值（abcd...wxyz），
      // 整体 PUT 会绕过「留空即不变」把真实密钥覆盖成掩码（密钥永久损坏）。
      await api.updateToken(token.name, {
        name: token.name,
        enabled: !token.enabled,
        allowedGroups: token.allowedGroups ?? [],
      })
      await Promise.all([mutate(), revalidate.usage()])
      toast.success(token.enabled ? '已停用 API Key' : '已启用 API Key', token.name)
    } catch (err) {
      toast.error('操作失败', (err as Error).message)
    } finally {
      setSwitchBusy(null)
    }
  }

  async function handleDelete(token: ApiToken) {
    const okToDelete = await confirm({
      title: `删除 API Key「${token.name}」？`,
      description: '使用该 Key 的客户端将立即失去访问权限，且无法恢复。',
      confirmText: '删除',
    })
    if (!okToDelete) return
    try {
      await api.deleteToken(token.name)
      await mutate()
      toast.success('已删除 API Key')
    } catch (err) {
      toast.error('删除失败', (err as Error).message)
    }
  }

  return (
    <>
      <RoleWatermark className="-right-8 top-0 opacity-[0.05] dark:opacity-[0.08]" />

      <div className="relative z-[1] space-y-6">
        <PageHeader
          title="访问令牌"          actions={
            <Button variant="primary" onClick={openCreate}>
              <Plus className="h-4 w-4" /> 新增令牌
            </Button>
          }
        />

        <AsyncState
          isLoading={isLoading}
          error={error}
          data={data}
          onRetry={() => mutate()}
          loadingColumns={5}
          emptyIcon={<KeyRound className="h-7 w-7" />}
          emptyTitle="暂无任何访问令牌"
          emptyDescription="创建你的第一个 API Key，供 OpenAI / Claude SDK 客户端鉴权接入。"
          emptyAction={
            <Button variant="primary" onClick={openCreate}>
              <Plus className="h-4 w-4" /> 新增令牌
            </Button>
          }
        >
          {(tokens) => (
            <div className="overflow-x-auto">
              <table className="w-full text-sm">
                <TableHeader className="bg-secondary/20">
                  <TableRow className="border-b border-border/60 hover:bg-transparent">
                    <TableHead className="py-3.5 pl-4 font-semibold text-2xs uppercase tracking-wider text-muted-foreground">名称</TableHead>
                    <TableHead className="py-3.5 font-semibold text-2xs uppercase tracking-wider text-muted-foreground">Key 凭证</TableHead>
                    <TableHead className="py-3.5 font-semibold text-2xs uppercase tracking-wider text-muted-foreground">绑定模型组</TableHead>
                    <TableHead className="py-3.5 font-semibold text-2xs uppercase tracking-wider text-muted-foreground">创建时间</TableHead>
                    <TableHead className="py-3.5 text-center font-semibold text-2xs uppercase tracking-wider text-muted-foreground">状态</TableHead>
                    <TableHead className="py-3.5 pr-4 text-right font-semibold text-2xs uppercase tracking-wider text-muted-foreground">操作</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody className="divide-y divide-border/30">
                {tokens.map((token) => (
                  <TableRow key={token.name} className="transition-colors hover:bg-secondary/30">
                    <TableCell className="py-3.5 pl-4 font-medium text-foreground">{token.name}</TableCell>
                    <TableCell className="py-3.5 font-mono text-xs text-muted-foreground">
                      <span className="flex items-center gap-1.5">
                        <RevealCopyButton name={token.name} maskedToken={token.token || '••••'} />
                      </span>
                    </TableCell>
                    <TableCell className="py-3.5">
                      {token.allowedGroups && token.allowedGroups.length > 0 ? (
                        <div className="flex flex-wrap gap-1.5">
                          {token.allowedGroups.map((g) => (
                            <CapChip
                              key={g}
                              className={cn(!validGroupNames.has(g) && 'line-through opacity-60')}
                              title={validGroupNames.has(g) ? undefined : '该模型组已不存在，编辑后将自动清除'}
                            >
                              {g}
                            </CapChip>
                          ))}
                        </div>
                      ) : (
                        <span className="inline-flex items-center rounded border border-border px-1.5 py-0.5 font-mono text-2xs text-muted-foreground">
                          全部模型组
                        </span>
                      )}
                    </TableCell>
                    <TableCell className="py-3.5 whitespace-nowrap font-mono text-xs text-muted-foreground">
                      {token.createdAt ? formatDateTime(token.createdAt) : '—'}
                    </TableCell>
                    <TableCell className="py-3.5 text-center">
                      <Switch
                        checked={token.enabled}
                        disabled={switchBusy === token.name}
                        onCheckedChange={() => toggleToken(token)}
                        aria-label={`${token.enabled ? '停用' : '启用'} ${token.name}`}
                      />
                    </TableCell>
                    <TableCell className="py-3.5 pr-4 text-right">
                      <div className="flex items-center justify-end gap-1">
                        <Button variant="ghost" size="iconSm" title="编辑" onClick={() => openEdit(token)}>
                          <Pencil className="h-3.5 w-3.5" />
                        </Button>
                        <Button variant="danger" size="iconSm" title="删除" onClick={() => handleDelete(token)}>
                          <Trash2 className="h-3.5 w-3.5" />
                        </Button>
                      </div>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </table>
          </div>
        )}
      </AsyncState>

      <TokenFormDialog open={formOpen} onOpenChange={setFormOpen} token={editing} onSaved={() => mutate()} />
      {dialog}
    </div>
    </>
  )
}

// RevealCopyButton 支持显示/隐藏和复制完整 Key。
function RevealCopyButton({ name, maskedToken }: { name: string; maskedToken: string }) {
  const toast = useToast()
  const [copied, setCopied] = useState(false)
  const [revealed, setRevealed] = useState(false)
  const [revealedToken, setRevealedToken] = useState('')
  const [busy, setBusy] = useState(false)

  async function handleReveal() {
    if (revealed) {
      setRevealed(false)
      setRevealedToken('')
      return
    }
    setBusy(true)
    try {
      const { token } = await api.revealToken(name)
      setRevealedToken(token)
      setRevealed(true)
    } catch (err) {
      toast.error('获取失败', (err as Error).message)
    } finally {
      setBusy(false)
    }
  }

  async function handleCopy() {
    setBusy(true)
    try {
      const token = revealed ? revealedToken : (await api.revealToken(name)).token
      await navigator.clipboard.writeText(token)
      setCopied(true)
      setTimeout(() => setCopied(false), 1500)
    } catch (err) {
      toast.error('复制失败', (err as Error).message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <>
      <span className="font-mono text-xs">{revealed ? revealedToken : maskedToken}</span>
      <Button
        variant="ghost"
        size="iconSm"
        title={revealed ? '隐藏' : '显示完整 Key'}
        disabled={busy}
        onClick={handleReveal}
      >
        {revealed ? <EyeOff className="h-3.5 w-3.5" /> : <Eye className="h-3.5 w-3.5" />}
      </Button>
      <Button variant="ghost" size="iconSm" title="复制完整 Key" disabled={busy} onClick={handleCopy}>
        {copied ? <Check className="h-3.5 w-3.5 text-jade" /> : <Copy className="h-3.5 w-3.5" />}
      </Button>
    </>
  )
}

function TokenFormDialog({
  open,
  onOpenChange,
  token,
  onSaved,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  token: ApiToken | null
  onSaved: () => void
}) {
  const toast = useToast()
  const { data: groups } = useGroups()
  const isEdit = !!token
  const [name, setName] = useState('')
  const [secret, setSecret] = useState('')
  const [enabled, setEnabled] = useState(true)
  const [allowedGroups, setAllowedGroups] = useState<string[]>([])
  const [saving, setSaving] = useState(false)

  useEffect(() => {
    if (open) {
      setName(token?.name ?? '')
      setSecret('')
      setEnabled(token?.enabled ?? true)
      // 过滤掉已不存在的组名（历史悬空引用），保存时不写回脏数据。
      const validNames = new Set((groups ?? []).map((g) => g.name))
      setAllowedGroups((token?.allowedGroups ?? []).filter((g) => validNames.has(g)))
    }
  }, [open, token, groups])

  function toggleGroup(groupName: string) {
    setAllowedGroups((prev) =>
      prev.includes(groupName) ? prev.filter((g) => g !== groupName) : [...prev, groupName],
    )
  }

  async function handleSave() {
    if (!name.trim()) {
      toast.error('请填写名称')
      return
    }
    if (!isEdit && !secret.trim()) {
      toast.error('请填写 Key 明文')
      return
    }
    setSaving(true)
    try {
      const payload: ApiToken = { name: name.trim(), enabled, allowedGroups }
      if (secret.trim()) payload.token = secret.trim()
      if (isEdit && token) await api.updateToken(token.name, payload)
      else await api.createToken(payload)
      // 保存后立即清空明文输入框
      setSecret('')
      onSaved()
      toast.success(isEdit ? 'API Key 已更新' : 'API Key 已创建')
      onOpenChange(false)
    } catch (err) {
      toast.error('保存失败', (err as Error).message)
    } finally {
      setSaving(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-md">
        <DialogHeader>
          <DialogTitle>{isEdit ? '编辑访问令牌' : '新增访问令牌'}</DialogTitle>
          <DialogDescription>明文 Key 仅在此处录入，保存后不再展示完整值。</DialogDescription>
        </DialogHeader>
        <div className="grid gap-4">
          <div className="space-y-2">
            <Label required>名称</Label>
            <Input
              value={name}
              placeholder="default"
              disabled={isEdit}
              onChange={(e) => setName(e.target.value)}
            />
          </div>
          <div className="space-y-2">
            <Label required={!isEdit}>Key 明文</Label>
            <div className="flex items-center gap-2">
              <SecretInput
                className="flex-1"
                value={secret}
                placeholder={isEdit ? '留空则保持原 Key' : 'client-key'}
                onChange={(e) => setSecret(e.target.value)}
              />
              {secret.trim() && <CopyButton value={secret.trim()} title="复制" />}
            </div>
          </div>
          <div className="space-y-2">
            <Label>可访问模型组</Label>
            <p className="text-xs text-muted-foreground">不选则可访问全部模型组；选择后仅限所选组。</p>
            <div className="flex flex-wrap gap-2">
              {(groups ?? []).length === 0 ? (
                <span className="text-xs text-muted-foreground">暂无模型组</span>
              ) : (
                (groups ?? []).map((g) => {
                  const active = allowedGroups.includes(g.name)
                  return (
                    <button
                      key={g.id}
                      type="button"
                      onClick={() => toggleGroup(g.name)}
                      className={cn(
                        'inline-flex h-[29px] items-center gap-1.5 rounded-full border px-3 text-xs transition-colors',
                        active
                          ? 'border-rose bg-wash font-semibold text-rose'
                          : 'border-border bg-card text-muted-foreground hover:text-foreground',
                      )}
                    >
                      {g.name}
                    </button>
                  )
                })
              )}
            </div>
          </div>
          <label className="flex items-center gap-3">
            <Switch checked={enabled} onCheckedChange={setEnabled} />
            <span className="text-sm font-medium">启用</span>
          </label>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)} disabled={saving}>
            取消
          </Button>
          <Button variant="primary" onClick={handleSave} disabled={saving}>
            {saving ? '保存中…' : '保存'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
