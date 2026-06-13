import { useEffect, useState } from 'react'
import { KeyRound, Pencil, Plus, Trash2 } from 'lucide-react'
import { PageHeader } from '@/components/page-header'
import { Card } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { Badge } from '@/components/ui/badge'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { AsyncState } from '@/components/ui/states'
import { EnabledBadge } from '@/components/badges'
import { SecretInput } from '@/components/secret-input'
import { CopyButton } from '@/components/copy-button'
import { useConfirm } from '@/components/ui/confirm-dialog'
import { useToast } from '@/components/ui/use-toast'
import { useTokens, useGroups } from '@/lib/hooks'
import { api } from '@/lib/api'
import { cn } from '@/lib/utils'
import type { ApiToken } from '@/lib/types'

export function TokensPage() {
  const toast = useToast()
  const { confirm, dialog } = useConfirm()
  const { data, isLoading, error, mutate } = useTokens()
  const [editing, setEditing] = useState<ApiToken | null>(null)
  const [formOpen, setFormOpen] = useState(false)

  function openCreate() {
    setEditing(null)
    setFormOpen(true)
  }

  function openEdit(token: ApiToken) {
    setEditing(token)
    setFormOpen(true)
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
    <div className="space-y-6">
      <PageHeader
        title="API Keys"
        description="转发客户端访问 /v1/* 与 /v1beta/* 所用的密钥，列表中已脱敏"
        actions={
          <Button onClick={openCreate}>
            <Plus className="h-4 w-4" /> 新增 API Key
          </Button>
        }
      />

      <Card>
        <AsyncState
          isLoading={isLoading}
          error={error}
          data={data}
          onRetry={() => mutate()}
          loadingColumns={5}
          emptyIcon={<KeyRound className="h-7 w-7" />}
          emptyTitle="还没有 API Key"
          emptyDescription="创建一个 Key，供转发客户端鉴权使用。"
          emptyAction={
            <Button onClick={openCreate}>
              <Plus className="h-4 w-4" /> 新增 API Key
            </Button>
          }
        >
          {(tokens) => (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>名称</TableHead>
                  <TableHead>Key（脱敏）</TableHead>
                  <TableHead>可访问模型组</TableHead>
                  <TableHead>状态</TableHead>
                  <TableHead className="text-right">操作</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {tokens.map((token) => (
                  <TableRow key={token.name}>
                    <TableCell className="font-medium">{token.name}</TableCell>
                    <TableCell className="font-mono text-xs text-muted-foreground">
                      {token.token || '••••'}
                    </TableCell>
                    <TableCell>
                      {token.allowedGroups && token.allowedGroups.length > 0 ? (
                        <div className="flex flex-wrap gap-1">
                          {token.allowedGroups.map((g) => (
                            <Badge key={g} variant="secondary">
                              {g}
                            </Badge>
                          ))}
                        </div>
                      ) : (
                        <Badge variant="muted">全部</Badge>
                      )}
                    </TableCell>
                    <TableCell>
                      <EnabledBadge enabled={token.enabled} />
                    </TableCell>
                    <TableCell>
                      <div className="flex items-center justify-end gap-1">
                        <Button variant="ghost" size="iconSm" title="编辑" onClick={() => openEdit(token)}>
                          <Pencil className="h-4 w-4" />
                        </Button>
                        <Button variant="ghost" size="iconSm" title="删除" onClick={() => handleDelete(token)}>
                          <Trash2 className="h-4 w-4 text-destructive" />
                        </Button>
                      </div>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </AsyncState>
      </Card>

      <TokenFormDialog open={formOpen} onOpenChange={setFormOpen} token={editing} onSaved={() => mutate()} />
      {dialog}
    </div>
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
      setAllowedGroups(token?.allowedGroups ?? [])
    }
  }, [open, token])

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
          <DialogTitle>{isEdit ? '编辑 API Key' : '新增 API Key'}</DialogTitle>
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
                        'rounded-full border px-3 py-1 text-xs transition-colors',
                        active
                          ? 'border-primary bg-primary text-primary-foreground'
                          : 'border-border bg-background text-muted-foreground hover:text-foreground',
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
          <Button onClick={handleSave} disabled={saving}>
            {saving ? '保存中…' : '保存'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
