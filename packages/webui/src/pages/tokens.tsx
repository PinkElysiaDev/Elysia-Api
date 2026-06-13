import { useEffect, useState } from 'react'
import { KeyRound, Pencil, Plus, Trash2 } from 'lucide-react'
import { PageHeader } from '@/components/page-header'
import { Card } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
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
import { useConfirm } from '@/components/ui/confirm-dialog'
import { useToast } from '@/components/ui/use-toast'
import { useTokens } from '@/lib/hooks'
import { api } from '@/lib/api'
import { formatRelative } from '@/lib/utils'
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
      title: `删除 Token「${token.name}」？`,
      description: '使用该 token 的客户端将立即失去访问权限，且无法恢复。',
      confirmText: '删除',
    })
    if (!okToDelete) return
    try {
      await api.deleteToken(token.name)
      await mutate()
      toast.success('已删除 Token')
    } catch (err) {
      toast.error('删除失败', (err as Error).message)
    }
  }

  return (
    <div className="space-y-6">
      <PageHeader
        title="API Tokens"
        description="转发客户端访问 /v1/* 与 /v1beta/* 所用的令牌，列表中已脱敏"
        actions={
          <Button onClick={openCreate}>
            <Plus className="h-4 w-4" /> 新增 Token
          </Button>
        }
      />

      <Card>
        <AsyncState
          isLoading={isLoading}
          error={error}
          data={data}
          onRetry={() => mutate()}
          loadingColumns={4}
          emptyIcon={<KeyRound className="h-7 w-7" />}
          emptyTitle="还没有 API Token"
          emptyDescription="创建一个 token，供转发客户端鉴权使用。"
          emptyAction={
            <Button onClick={openCreate}>
              <Plus className="h-4 w-4" /> 新增 Token
            </Button>
          }
        >
          {(tokens) => (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>名称</TableHead>
                  <TableHead>Token（脱敏）</TableHead>
                  <TableHead>状态</TableHead>
                  <TableHead>更新时间</TableHead>
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
                      <EnabledBadge enabled={token.enabled} />
                    </TableCell>
                    <TableCell className="text-xs text-muted-foreground">
                      {formatRelative(token.updatedAt)}
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
  const isEdit = !!token
  const [name, setName] = useState('')
  const [secret, setSecret] = useState('')
  const [enabled, setEnabled] = useState(true)
  const [saving, setSaving] = useState(false)

  useEffect(() => {
    if (open) {
      setName(token?.name ?? '')
      setSecret('')
      setEnabled(token?.enabled ?? true)
    }
  }, [open, token])

  async function handleSave() {
    if (!name.trim()) {
      toast.error('请填写名称')
      return
    }
    if (!isEdit && !secret.trim()) {
      toast.error('请填写 Token 明文')
      return
    }
    setSaving(true)
    try {
      const payload: ApiToken = { name: name.trim(), enabled }
      if (secret.trim()) payload.token = secret.trim()
      if (isEdit && token) await api.updateToken(token.name, payload)
      else await api.createToken(payload)
      // 保存后立即清空明文输入框
      setSecret('')
      onSaved()
      toast.success(isEdit ? 'Token 已更新' : 'Token 已创建')
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
          <DialogTitle>{isEdit ? '编辑 Token' : '新增 Token'}</DialogTitle>
          <DialogDescription>明文 Token 仅在此处录入，保存后不再展示完整值。</DialogDescription>
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
            <Label required={!isEdit}>Token 明文</Label>
            <SecretInput
              value={secret}
              placeholder={isEdit ? '留空则保持原 token' : 'client-token'}
              onChange={(e) => setSecret(e.target.value)}
            />
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
