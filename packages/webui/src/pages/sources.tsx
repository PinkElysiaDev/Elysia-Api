import { useState } from 'react'
import { Database, Pencil, Plus, RefreshCw, Trash2 } from 'lucide-react'
import { PageHeader } from '@/components/page-header'
import { Card } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { AsyncState } from '@/components/ui/states'
import { EnabledBadge, PlatformBadge } from '@/components/badges'
import { useConfirm } from '@/components/ui/confirm-dialog'
import { useToast } from '@/components/ui/use-toast'
import { useSources, revalidate } from '@/lib/hooks'
import { api } from '@/lib/api'
import type { ModelSource } from '@/lib/types'
import { SourceFormDialog } from './sources/source-form'

export function SourcesPage() {
  const toast = useToast()
  const { confirm, dialog } = useConfirm()
  const { data, isLoading, error, mutate } = useSources()
  const [editing, setEditing] = useState<ModelSource | null>(null)
  const [formOpen, setFormOpen] = useState(false)
  const [busyId, setBusyId] = useState<string | null>(null)

  function openCreate() {
    setEditing(null)
    setFormOpen(true)
  }

  function openEdit(source: ModelSource) {
    setEditing(source)
    setFormOpen(true)
  }

  async function handleFetch(source: ModelSource) {
    setBusyId(source.id)
    try {
      const result = await api.fetchSource(source.id)
      await revalidate.models()
      toast.success('拉取完成', `${source.name} 拉取到 ${result.count} 个模型`)
    } catch (err) {
      toast.error('拉取失败', (err as Error).message)
    } finally {
      setBusyId(null)
    }
  }

  async function handleDelete(source: ModelSource) {
    const okToDelete = await confirm({
      title: `删除模型源「${source.name}」？`,
      description: '删除后引用此源的模型缓存将失效，且无法恢复。',
      confirmText: '删除',
    })
    if (!okToDelete) return
    try {
      await api.deleteSource(source.id)
      await mutate()
      toast.success('已删除模型源')
    } catch (err) {
      toast.error('删除失败', (err as Error).message)
    }
  }

  return (
    <div className="space-y-6">
      <PageHeader
        title="模型源"
        description="管理上游供应商，支持 OpenAI / 兼容 / Claude / Gemini，自动拉取或手动模型"
        actions={
          <Button onClick={openCreate}>
            <Plus className="h-4 w-4" /> 新增模型源
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
          emptyIcon={<Database className="h-7 w-7" />}
          emptyTitle="还没有模型源"
          emptyDescription="新增一个上游供应商，开始聚合模型。"
          emptyAction={
            <Button onClick={openCreate}>
              <Plus className="h-4 w-4" /> 新增模型源
            </Button>
          }
        >
          {(sources) => (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>名称</TableHead>
                  <TableHead>平台</TableHead>
                  <TableHead>Base URL</TableHead>
                  <TableHead>模式</TableHead>
                  <TableHead>状态</TableHead>
                  <TableHead className="text-right">操作</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {sources.map((source) => (
                  <TableRow key={source.id}>
                    <TableCell className="font-medium">
                      {source.name}
                      <div className="text-xs text-muted-foreground">{source.id}</div>
                    </TableCell>
                    <TableCell>
                      <PlatformBadge platform={source.platform} />
                    </TableCell>
                    <TableCell className="max-w-[220px] truncate font-mono text-xs text-muted-foreground">
                      {source.baseUrl}
                    </TableCell>
                    <TableCell>
                      {source.autoFetchModels ? (
                        <Badge variant="default">自动拉取</Badge>
                      ) : (
                        <Badge variant="secondary">手动 {source.manualModels?.length ?? 0}</Badge>
                      )}
                    </TableCell>
                    <TableCell>
                      <EnabledBadge enabled={source.enabled} />
                    </TableCell>
                    <TableCell>
                      <div className="flex items-center justify-end gap-1">
                        {source.autoFetchModels && (
                          <Button
                            variant="ghost"
                            size="iconSm"
                            title="拉取模型"
                            disabled={busyId === source.id || !source.enabled}
                            onClick={() => handleFetch(source)}
                          >
                            <RefreshCw className={busyId === source.id ? 'h-4 w-4 animate-spin' : 'h-4 w-4'} />
                          </Button>
                        )}
                        <Button variant="ghost" size="iconSm" title="编辑" onClick={() => openEdit(source)}>
                          <Pencil className="h-4 w-4" />
                        </Button>
                        <Button
                          variant="ghost"
                          size="iconSm"
                          title="删除"
                          onClick={() => handleDelete(source)}
                        >
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

      <SourceFormDialog open={formOpen} onOpenChange={setFormOpen} source={editing} />
      {dialog}
    </div>
  )
}
