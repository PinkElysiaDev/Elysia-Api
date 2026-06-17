import { useState } from 'react'
import { Layers, Pencil, Plus, Trash2 } from 'lucide-react'
import { PageHeader } from '@/components/page-header'
import { Card } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { AsyncState } from '@/components/ui/states'
import { EnabledBadge, StrategyBadge } from '@/components/badges'
import { useConfirm } from '@/components/ui/confirm-dialog'
import { useToast } from '@/components/ui/use-toast'
import { useGroups } from '@/lib/hooks'
import { api } from '@/lib/api'
import { formatNumber } from '@/lib/utils'
import type { ModelGroup } from '@/lib/types'
import { GroupFormDialog } from './groups/group-form'

export function GroupsPage() {
  const toast = useToast()
  const { confirm, dialog } = useConfirm()
  const { data, isLoading, error, mutate } = useGroups()
  const [editing, setEditing] = useState<ModelGroup | null>(null)
  const [formOpen, setFormOpen] = useState(false)

  function openCreate() {
    setEditing(null)
    setFormOpen(true)
  }

  function openEdit(group: ModelGroup) {
    setEditing(group)
    setFormOpen(true)
  }

  async function handleDelete(group: ModelGroup) {
    const okToDelete = await confirm({
      title: `删除模型组「${group.name}」？`,
      description: '删除后客户端将无法再通过该模型 ID 访问，且无法恢复。',
      confirmText: '删除',
    })
    if (!okToDelete) return
    try {
      await api.deleteGroup(group.id)
      await mutate()
      toast.success('已删除模型组')
    } catch (err) {
      toast.error('删除失败', (err as Error).message)
    }
  }

  return (
    <div className="space-y-6">
      <PageHeader
        title="模型组"
        description="对外展示的模型 ID"
        actions={
          <Button onClick={openCreate}>
            <Plus className="h-4 w-4" /> 新增模型组
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
          emptyIcon={<Layers className="h-7 w-7" />}
          emptyTitle="还没有模型组"
          emptyDescription="创建模型组，把多个模型聚合为一个对外模型 ID。"
          emptyAction={
            <Button onClick={openCreate}>
              <Plus className="h-4 w-4" /> 新增模型组
            </Button>
          }
        >
          {(groups) => (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>组名 / 模型 ID</TableHead>
                  <TableHead>模型</TableHead>
                  <TableHead>策略</TableHead>
                  <TableHead>限流</TableHead>
                  <TableHead>状态</TableHead>
                  <TableHead className="text-right">操作</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {groups.map((group) => (
                  <TableRow key={group.id}>
                    <TableCell className="font-medium">
                      {group.name}
                      <div className="flex gap-1 pt-1">
                        {group.visionCapable && <Badge variant="secondary">视觉</Badge>}
                        {group.toolsCapable && <Badge variant="secondary">工具</Badge>}
                      </div>
                    </TableCell>
                    <TableCell>
                      <div className="flex flex-wrap gap-1">
                        {group.models.slice(0, 3).map((m) => (
                          <Badge key={m} variant="outline" className="font-mono text-[11px]">
                            {m}
                          </Badge>
                        ))}
                        {group.models.length > 3 && (
                          <Badge variant="muted">+{group.models.length - 3}</Badge>
                        )}
                      </div>
                    </TableCell>
                    <TableCell>
                      <StrategyBadge strategy={group.strategy} />
                      <div className="pt-1 text-xs text-muted-foreground">
                        重试 {group.maxRetries} · {group.retryInterval}ms
                      </div>
                    </TableCell>
                    <TableCell className="text-xs text-muted-foreground">
                      {group.maxConcurrency ? `并发 ${group.maxConcurrency}` : '并发不限'}
                      <br />
                      {group.dailyLimitMaxRequests
                        ? `${formatNumber(group.dailyLimitMaxRequests)} 次/日`
                        : '请求不限'}
                    </TableCell>
                    <TableCell>
                      <EnabledBadge enabled={group.enabled} />
                    </TableCell>
                    <TableCell>
                      <div className="flex items-center justify-end gap-1">
                        <Button variant="ghost" size="iconSm" title="编辑" onClick={() => openEdit(group)}>
                          <Pencil className="h-4 w-4" />
                        </Button>
                        <Button variant="ghost" size="iconSm" title="删除" onClick={() => handleDelete(group)}>
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

      <GroupFormDialog open={formOpen} onOpenChange={setFormOpen} group={editing} />
      {dialog}
    </div>
  )
}
