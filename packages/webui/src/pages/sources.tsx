import { useMemo, useState } from 'react'
import { Database, Pencil, Plus, RefreshCw, Trash2, ChevronRight, Eye, Wrench, Boxes } from 'lucide-react'
import { PageHeader } from '@/components/page-header'
import { Card } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { AsyncState } from '@/components/ui/states'
import { EnabledBadge, PlatformBadge, ModelTypeBadge } from '@/components/badges'
import { useConfirm } from '@/components/ui/confirm-dialog'
import { useToast } from '@/components/ui/use-toast'
import { useSources, useModels, revalidate } from '@/lib/hooks'
import { api } from '@/lib/api'
import { cn, formatNumber, formatRelative } from '@/lib/utils'
import type { ModelSource, Model } from '@/lib/types'
import { SourceFormDialog } from './sources/source-form'

export function SourcesPage() {
  const toast = useToast()
  const { confirm, dialog } = useConfirm()
  const { data, isLoading, error, mutate } = useSources()
  const { data: models } = useModels()
  const [editing, setEditing] = useState<ModelSource | null>(null)
  const [formOpen, setFormOpen] = useState(false)
  const [busyId, setBusyId] = useState<string | null>(null)
  const [refreshingAll, setRefreshingAll] = useState(false)
  const [expanded, setExpanded] = useState<Record<string, boolean>>({})

  // 按 sourceId 分组的模型缓存（合并自原「模型缓存」页）。
  const modelsBySource = useMemo(() => {
    const map = new Map<string, Model[]>()
    for (const m of models ?? []) {
      const key = m.sourceId || ''
      const list = map.get(key) ?? []
      list.push(m)
      map.set(key, list)
    }
    return map
  }, [models])

  function openCreate() {
    setEditing(null)
    setFormOpen(true)
  }

  function openEdit(source: ModelSource) {
    setEditing(source)
    setFormOpen(true)
  }

  function toggleExpand(id: string) {
    setExpanded((prev) => ({ ...prev, [id]: !prev[id] }))
  }

  async function refreshAll() {
    setRefreshingAll(true)
    try {
      const result = await api.refreshModels()
      await revalidate.models()
      toast.success('已刷新全部模型', `共聚合 ${result.count} 个模型`)
    } catch (err) {
      toast.error('刷新失败', (err as Error).message)
    } finally {
      setRefreshingAll(false)
    }
  }

  async function handleFetch(source: ModelSource) {
    setBusyId(source.id)
    try {
      const result = await api.fetchSource(source.id)
      await revalidate.models()
      setExpanded((prev) => ({ ...prev, [source.id]: true }))
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
      await revalidate.models()
      toast.success('已删除模型源')
    } catch (err) {
      toast.error('删除失败', (err as Error).message)
    }
  }

  return (
    <div className="space-y-6">
      <PageHeader
        title="模型源"
        description="管理上游供应商与其聚合的模型"
        actions={
          <div className="flex items-center gap-2">
            <Button variant="outline" onClick={refreshAll} disabled={refreshingAll}>
              <RefreshCw className={refreshingAll ? 'h-4 w-4 animate-spin' : 'h-4 w-4'} /> 刷新全部模型
            </Button>
            <Button onClick={openCreate}>
              <Plus className="h-4 w-4" /> 新增模型源
            </Button>
          </div>
        }
      />

      <Card>
        <AsyncState
          isLoading={isLoading}
          error={error}
          data={data}
          onRetry={() => mutate()}
          loadingColumns={6}
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
                  <TableHead className="w-8" />
                  <TableHead>名称</TableHead>
                  <TableHead>平台</TableHead>
                  <TableHead>Base URL</TableHead>
                  <TableHead>模型数</TableHead>
                  <TableHead>状态</TableHead>
                  <TableHead className="text-right">操作</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {sources.map((source) => {
                  const sourceModels = modelsBySource.get(source.id) ?? []
                  const isOpen = !!expanded[source.id]
                  return (
                    <>
                      <TableRow key={source.id}>
                        <TableCell className="pr-0">
                          <button
                            type="button"
                            onClick={() => toggleExpand(source.id)}
                            className="rounded-md p-1 text-muted-foreground transition-colors hover:text-foreground"
                            aria-label={isOpen ? '收起' : '展开'}
                          >
                            <ChevronRight className={cn('h-4 w-4 transition-transform', isOpen && 'rotate-90')} />
                          </button>
                        </TableCell>
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
                          <Badge variant="muted">{sourceModels.length}</Badge>
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
                            <Button variant="ghost" size="iconSm" title="删除" onClick={() => handleDelete(source)}>
                              <Trash2 className="h-4 w-4 text-destructive" />
                            </Button>
                          </div>
                        </TableCell>
                      </TableRow>
                      {isOpen && (
                        <TableRow key={`${source.id}-models`} className="hover:bg-transparent">
                          <TableCell colSpan={7} className="bg-muted/30">
                            {sourceModels.length === 0 ? (
                              <div className="flex items-center gap-2 py-3 text-sm text-muted-foreground">
                                <Boxes className="h-4 w-4" />
                                暂无模型。
                                {source.autoFetchModels
                                  ? '点击右侧刷新按钮拉取，或在编辑中添加手动模型。'
                                  : '在编辑中添加手动模型。'}
                              </div>
                            ) : (
                              <div className="grid gap-2 py-2 sm:grid-cols-2 lg:grid-cols-3">
                                {sourceModels.map((model) => (
                                  <ModelCard key={`${model.sourceId}-${model.id}`} model={model} />
                                ))}
                              </div>
                            )}
                          </TableCell>
                        </TableRow>
                      )}
                    </>
                  )
                })}
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

function ModelCard({ model }: { model: Model }) {
  const dimmed = !model.available
  return (
    <div
      className={cn(
        'rounded-xl border border-border/70 bg-background/60 p-3 transition-colors',
        dimmed && 'opacity-55',
      )}
    >
      <div className="flex items-start justify-between gap-2">
        <div className="min-w-0">
          <p className="truncate text-sm font-medium" title={model.id}>
            {model.name || model.id}
          </p>
          <p className="truncate font-mono text-xs text-muted-foreground" title={model.id}>
            {model.id}
          </p>
        </div>
        <PlatformBadge platform={model.platform} />
      </div>
      <div className="mt-2.5 flex flex-wrap items-center gap-1.5">
        <ModelTypeBadge type={model.type} />
        {model.maxTokens > 0 && <Badge variant="outline">{formatNumber(model.maxTokens)} tok</Badge>}
        {model.visionCapable && (
          <Badge variant="secondary">
            <Eye className="h-3 w-3" /> 视觉
          </Badge>
        )}
        {model.toolsCapable && (
          <Badge variant="secondary">
            <Wrench className="h-3 w-3" /> 工具
          </Badge>
        )}
        {!model.available && <Badge variant="muted">不可用</Badge>}
      </div>
      <p className="mt-2 text-[11px] text-muted-foreground">检测于 {formatRelative(model.lastCheckedAt)}</p>
    </div>
  )
}
