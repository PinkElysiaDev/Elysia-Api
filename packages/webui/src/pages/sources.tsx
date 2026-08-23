import { Fragment, useMemo, useState } from 'react'
import {
  Boxes,
  Check,
  ChevronDown,
  ChevronRight,
  Eye,
  ListPlus,
  Pencil,
  Plus,
  Power,
  PowerOff,
  RefreshCw,
  Search,
  Trash2,
  Wrench,
} from 'lucide-react'
import { PageHeader } from '@/components/page-header'
import { Card } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Badge } from '@/components/ui/badge'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { AsyncState } from '@/components/ui/states'
import { EnabledBadge, PlatformBadge, ModelTypeBadge } from '@/components/badges'
import { useConfirm } from '@/components/ui/confirm-dialog'
import { useToast } from '@/components/ui/use-toast'
import { useSources, useModels, useModelCatalogStatus, revalidate } from '@/lib/hooks'
import { api } from '@/lib/api'
import { cn, formatNumber, formatRelative } from '@/lib/utils'
import type { ModelSource, Model } from '@/lib/types'
import { SourceFormDialog } from './sources/source-form'
import { ModelEditDialog } from './sources/model-edit-dialog'
import { QuickCreateGroupDialog, AddToGroupDialog } from './sources/quick-group-dialogs'

// 按 key 分组展示的视图结构（多 key 源；单 key 回退为单一「全部模型」组）。
interface ModelGroupView {
  key: string
  label: string
  note?: string
  badge?: string
  models: Model[]
}

// buildModelGroups 按源的 per-key 拉取结果（权限发现）把模型分组成视图：
// 每个带 fetchedModels 的启用 key 一组（模型可归属多组），不属于任何 key 集合的
// 模型归入「其他模型」；无 per-key 数据时维持单一平铺组。
function buildModelGroups(source: ModelSource, sourceModels: Model[]): ModelGroupView[] {
  const keysWithFetch = (source.apiKeys ?? [])
    .map((entry, index) => ({ entry, index }))
    .filter(
      ({ entry }) =>
        !entry.disabled && !!entry.value?.trim() && (entry.fetchedModels?.length ?? 0) > 0,
    )
  if (keysWithFetch.length === 0) {
    return [{ key: 'all', label: '全部模型', models: sourceModels }]
  }
  const groups: ModelGroupView[] = keysWithFetch.map(({ entry, index }) => {
    const fetched = new Set(entry.fetchedModels ?? [])
    const enabledCount = entry.allowedModels
      ? entry.allowedModels.filter((id) => fetched.has(id)).length
      : fetched.size
    return {
      key: `key-${index}`,
      label: `Key ${index + 1}`,
      note: entry.note,
      badge: `已启用 ${enabledCount}/${fetched.size}`,
      models: sourceModels.filter((m) => fetched.has(m.id)),
    }
  })
  const inAnyGroup = new Set(groups.flatMap((g) => g.models.map((m) => m.id)))
  const others = sourceModels.filter((m) => !inAnyGroup.has(m.id))
  if (others.length > 0) {
    groups.push({ key: 'other', label: '其他模型', badge: '未归属任何 Key', models: others })
  }
  return groups
}

export function SourcesPage() {
  const toast = useToast()
  const { confirm, dialog } = useConfirm()
  // 60s 自动刷新：拉取时间/检测时间/模型数所见即所得，无需手动刷新。
  const { data, isLoading, error, mutate } = useSources(60_000)
  const { data: models } = useModels(60_000)
  const { data: catalogStatus } = useModelCatalogStatus()
  const [editing, setEditing] = useState<ModelSource | null>(null)
  const [formOpen, setFormOpen] = useState(false)
  const [busyId, setBusyId] = useState<string | null>(null)
  const [refreshingAll, setRefreshingAll] = useState(false)
  const [expanded, setExpanded] = useState<Record<string, boolean>>({})
  // 方向3/4：源内模型的选择状态、面板检索（跨组全局 + 组内）、分组折叠、单模型编辑。
  const [selected, setSelected] = useState<Record<string, boolean>>({})
  const [globalSearch, setGlobalSearch] = useState<Record<string, string>>({})
  const [groupSearch, setGroupSearch] = useState<Record<string, string>>({})
  const [collapsedGroups, setCollapsedGroups] = useState<Record<string, boolean>>({})
  const [batchBusy, setBatchBusy] = useState(false)
  const [editingModel, setEditingModel] = useState<Model | null>(null)
  const [modelEditOpen, setModelEditOpen] = useState(false)
  const [quickCreate, setQuickCreate] = useState<{ source: ModelSource; models: Model[] } | null>(null)
  const [addToGroup, setAddToGroup] = useState<Model[] | null>(null)

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

  function selectedModelsOf(sourceId: string): Model[] {
    return (modelsBySource.get(sourceId) ?? []).filter((m) => selected[`${sourceId}:${m.id}`])
  }

  function toggleModelSelected(sourceId: string, modelId: string) {
    const key = `${sourceId}:${modelId}`
    setSelected((prev) => ({ ...prev, [key]: !prev[key] }))
  }

  function setModelsSelected(list: Model[], value: boolean) {
    setSelected((prev) => {
      const next = { ...prev }
      for (const m of list) next[`${m.sourceId}:${m.id}`] = value
      return next
    })
  }

  // 批量启停（方向4）：对选中模型循环 PATCH enabled，完成后统一刷新并汇总。
  async function batchSetEnabled(list: Model[], enabled: boolean) {
    if (list.length === 0 || batchBusy) return
    setBatchBusy(true)
    try {
      const results = await Promise.allSettled(
        list.map((m) => api.updateModel(m.sourceId ?? '', m.id, { enabled })),
      )
      const okCount = results.filter((r) => r.status === 'fulfilled').length
      const failedCount = results.length - okCount
      await revalidate.models()
      toast.success(
        enabled ? '已批量启用' : '已批量禁用',
        `${list.length} 个模型：成功 ${okCount}${failedCount > 0 ? `，失败 ${failedCount}` : ''}`,
      )
    } catch (err) {
      toast.error('批量操作失败', (err as Error).message)
    } finally {
      setBatchBusy(false)
    }
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
      // 方向4：变更摘要提示（新增默认启用、移除同步清理组引用）；
      // 多 key 源附带逐 key 拉取结果（权限发现）。
      const added = result.added?.length ?? 0
      const removed = result.removed?.length ?? 0
      const changes = [
        added > 0 ? `新增 ${added}` : '',
        removed > 0 ? `移除 ${removed}` : '',
      ]
        .filter(Boolean)
        .join('，')
      const keySummary = (result.keys ?? [])
        .map((k, i) => (k.error ? `Key${i + 1}失败` : `Key${i + 1}: ${k.count}`))
        .join('，')
      const description = [
        `${source.name} 共 ${result.count} 个模型${changes ? `（${changes}）` : ''}`,
        keySummary,
      ]
        .filter(Boolean)
        .join('；')
      toast.success('拉取完成', description)
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

  function openModelEdit(model: Model) {
    setEditingModel(model)
    setModelEditOpen(true)
  }

  async function toggleModelEnabled(model: Model) {
    const next = !(model.enabled !== false)
    try {
      await api.updateModel(model.sourceId ?? '', model.id, { enabled: next })
      await revalidate.models()
      toast.success(next ? '已启用模型' : '已停用模型', `${model.name || model.id}${next ? '' : '（不参与模型组调度）'}`)
    } catch (err) {
      toast.error('操作失败', (err as Error).message)
    }
  }

  return (
    <div className="space-y-6">
      <PageHeader
        title="模型源"
        description="管理上游供应商与其聚合的模型"
        actions={
          <div className="flex items-center gap-2">
            {catalogStatus?.enabled && (
              <Badge
                variant="muted"
                className="max-w-[280px] truncate"
                title={
                  catalogStatus.entries > 0
                    ? `模型能力目录已加载 ${catalogStatus.entries} 个模型（models.dev），刷新模型时自动回填视觉/工具等能力`
                    : '能力目录尚未加载成功：模型能力不会被自动回填，可在模型编辑中手动开启；服务器需可访问 models.dev（或配置 modelCatalog.url/proxy）'
                }
              >
                {catalogStatus.entries > 0
                  ? `能力目录 ${catalogStatus.entries} 模型`
                  : '能力目录未加载'}
              </Badge>
            )}
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
          emptyIcon={<Boxes className="h-7 w-7" />}
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
                  const groups = buildModelGroups(source, sourceModels)
                  const globalKeyword = (globalSearch[source.id] ?? '').trim().toLowerCase()
                  const matchesGlobal = (m: Model) =>
                    !globalKeyword ||
                    `${m.id} ${m.name} ${m.sourceName ?? ''}`.toLowerCase().includes(globalKeyword)
                  const selectedCount = selectedModelsOf(source.id).length
                  const allVisibleModels = groups
                    .map((g) => {
                      const kw = (groupSearch[`${source.id}:${g.key}`] ?? '').trim().toLowerCase()
                      return kw
                        ? g.models.filter(
                            (m) =>
                              matchesGlobal(m) &&
                              `${m.id} ${m.name} ${m.sourceName ?? ''}`.toLowerCase().includes(kw),
                          )
                        : g.models.filter(matchesGlobal)
                    })
                    .flat()
                  return (
                    <Fragment key={source.id}>
                      <TableRow>
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
                        <TableRow className="hover:bg-transparent">
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
                              <div className="space-y-3 py-3">
                                {/* 顶部工具条：跨组搜索 + 更新时间 + 全局选择 + 批量操作 */}
                                <div className="flex flex-wrap items-center gap-2">
                                  {groups.length > 1 && (
                                    <div className="relative w-56">
                                      <Search className="absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
                                      <Input
                                        className="h-8 pl-9"
                                        placeholder="跨组搜索全部模型…"
                                        value={globalSearch[source.id] ?? ''}
                                        onChange={(e) =>
                                          setGlobalSearch((prev) => ({ ...prev, [source.id]: e.target.value }))
                                        }
                                      />
                                    </div>
                                  )}
                                  {source.updatedAt && (
                                    <span className="text-xs text-muted-foreground">
                                      更新于 {formatRelative(source.updatedAt)}
                                    </span>
                                  )}
                                  <Button
                                    variant="ghost"
                                    size="sm"
                                    disabled={allVisibleModels.length === 0}
                                    onClick={() => setModelsSelected(allVisibleModels, true)}
                                  >
                                    全选
                                  </Button>
                                  <Button variant="ghost" size="sm" onClick={() => setModelsSelected(sourceModels, false)}>
                                    清空选择
                                  </Button>
                                  <div className="ml-auto flex flex-wrap items-center gap-2">
                                    <span className="text-xs text-muted-foreground">已选 {selectedCount}</span>
                                    <Button
                                      variant="outline"
                                      size="sm"
                                      disabled={selectedCount === 0 || batchBusy}
                                      onClick={() => batchSetEnabled(selectedModelsOf(source.id), true)}
                                    >
                                      <Power className="h-4 w-4" /> 一键启用
                                    </Button>
                                    <Button
                                      variant="outline"
                                      size="sm"
                                      disabled={selectedCount === 0 || batchBusy}
                                      onClick={() => batchSetEnabled(selectedModelsOf(source.id), false)}
                                    >
                                      <PowerOff className="h-4 w-4" /> 一键禁用
                                    </Button>
                                    <Button
                                      variant="outline"
                                      size="sm"
                                      disabled={selectedCount === 0}
                                      onClick={() => setQuickCreate({ source, models: selectedModelsOf(source.id) })}
                                    >
                                      <Plus className="h-4 w-4" /> 创建模型组
                                    </Button>
                                    <Button
                                      variant="outline"
                                      size="sm"
                                      disabled={selectedCount === 0}
                                      onClick={() => setAddToGroup(selectedModelsOf(source.id))}
                                    >
                                      <ListPlus className="h-4 w-4" /> 加入模型组
                                    </Button>
                                  </div>
                                </div>
                                {/* 按 key 分组（单 key 源为单一「全部模型」组） */}
                                {groups.map((group) => {
                                  const groupStateKey = `${source.id}:${group.key}`
                                  const groupKeyword = (groupSearch[groupStateKey] ?? '').trim().toLowerCase()
                                  const groupVisible = group.models.filter(
                                    (m) =>
                                      matchesGlobal(m) &&
                                      (!groupKeyword ||
                                        `${m.id} ${m.name} ${m.sourceName ?? ''}`.toLowerCase().includes(groupKeyword)),
                                  )
                                  const collapsed = !!collapsedGroups[groupStateKey]
                                  return (
                                    <div key={group.key} className="rounded-xl border border-border/60 bg-background/40">
                                      <div className="flex flex-wrap items-center gap-2 px-3 py-2">
                                        <button
                                          type="button"
                                          onClick={() =>
                                            setCollapsedGroups((prev) => ({ ...prev, [groupStateKey]: !prev[groupStateKey] }))
                                          }
                                          className="rounded-md p-1 text-muted-foreground transition-colors hover:text-foreground"
                                          aria-label={collapsed ? '展开分组' : '折叠分组'}
                                        >
                                          <ChevronDown
                                            className={cn('h-4 w-4 transition-transform', collapsed && '-rotate-90')}
                                          />
                                        </button>
                                        <span className="text-sm font-medium">
                                          {group.label}
                                          {group.note ? ` · ${group.note}` : ''}
                                        </span>
                                        {group.badge && <Badge variant="outline">{group.badge}</Badge>}
                                        <Badge variant="muted">{group.models.length} 模型</Badge>
                                        <div className="ml-auto flex items-center gap-2">
                                          <div className="relative w-44">
                                            <Search className="absolute left-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-muted-foreground" />
                                            <Input
                                              className="h-7 pl-8 text-xs"
                                              placeholder="组内搜索…"
                                              value={groupSearch[groupStateKey] ?? ''}
                                              onChange={(e) =>
                                                setGroupSearch((prev) => ({ ...prev, [groupStateKey]: e.target.value }))
                                              }
                                            />
                                          </div>
                                          <Button
                                            variant="ghost"
                                            size="sm"
                                            disabled={groupVisible.length === 0}
                                            onClick={() => setModelsSelected(groupVisible, true)}
                                          >
                                            全选本组
                                          </Button>
                                        </div>
                                      </div>
                                      {!collapsed && (
                                        <div className="border-t border-border/60 p-3">
                                          {groupVisible.length === 0 ? (
                                            <p className="py-2 text-center text-sm text-muted-foreground">
                                              没有匹配的模型
                                            </p>
                                          ) : (
                                            <div className="grid gap-2 sm:grid-cols-2 lg:grid-cols-3">
                                              {groupVisible.map((model) => (
                                                <ModelCard
                                                  key={`${group.key}-${model.sourceId}-${model.id}`}
                                                  model={model}
                                                  checked={!!selected[`${source.id}:${model.id}`]}
                                                  onToggleSelect={() => toggleModelSelected(source.id, model.id)}
                                                  onEdit={() => openModelEdit(model)}
                                                  onToggleEnabled={() => toggleModelEnabled(model)}
                                                />
                                              ))}
                                            </div>
                                          )}
                                        </div>
                                      )}
                                    </div>
                                  )
                                })}
                              </div>
                            )}
                          </TableCell>
                        </TableRow>
                      )}
                    </Fragment>
                  )
                })}
              </TableBody>
            </Table>
          )}
        </AsyncState>
      </Card>

      <SourceFormDialog open={formOpen} onOpenChange={setFormOpen} source={editing} />
      <ModelEditDialog open={modelEditOpen} onOpenChange={setModelEditOpen} model={editingModel} />
      <QuickCreateGroupDialog
        open={quickCreate !== null}
        onOpenChange={(open) => !open && setQuickCreate(null)}
        models={quickCreate?.models ?? []}
        defaultName={quickCreate?.source.name ?? ''}
      />
      <AddToGroupDialog
        open={addToGroup !== null}
        onOpenChange={(open) => !open && setAddToGroup(null)}
        models={addToGroup ?? []}
      />
      {dialog}
    </div>
  )
}

function ModelCard({
  model,
  checked,
  onToggleSelect,
  onEdit,
  onToggleEnabled,
}: {
  model: Model
  checked: boolean
  onToggleSelect: () => void
  onEdit: () => void
  onToggleEnabled: () => void
}) {
  // 停用（enabled=false）或健康不可用（available=false）都置灰，但语义区分展示。
  const dimmed = model.enabled === false || !model.available
  const isEnabled = model.enabled !== false
  return (
    <div
      className={cn(
        'group rounded-xl border border-border/70 bg-background/60 p-3 transition-colors',
        dimmed && 'opacity-55',
        checked && 'border-primary/60 ring-1 ring-primary/30',
      )}
    >
      <div className="flex items-start justify-between gap-2">
        <div className="flex min-w-0 items-start gap-2">
          <button
            type="button"
            onClick={onToggleSelect}
            aria-label={checked ? '取消选择' : '选择'}
            className={cn(
              'mt-0.5 flex h-4 w-4 shrink-0 items-center justify-center rounded border transition-colors',
              checked ? 'border-primary bg-primary text-primary-foreground' : 'border-border hover:border-primary/60',
            )}
          >
            {checked && <Check className="h-3 w-3" />}
          </button>
          <div className="min-w-0">
            <p className="truncate text-sm font-medium" title={model.id}>
              {model.name || model.id}
            </p>
            <p className="truncate font-mono text-xs text-muted-foreground" title={model.id}>
              {model.id}
            </p>
          </div>
        </div>
        <div className="flex shrink-0 items-center gap-0.5">
          <PlatformBadge platform={model.platform} />
        </div>
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
        {model.capabilitySource === 'catalog' && <Badge variant="outline">目录</Badge>}
        {model.origin === 'manual' && <Badge variant="outline">手动</Badge>}
        {model.enabled === false && <Badge variant="muted">已停用</Badge>}
        {model.enabled !== false && !model.available && <Badge variant="muted">不可用</Badge>}
      </div>
      <div className="mt-2 flex items-center justify-between">
        <p className="text-[11px] text-muted-foreground">检测于 {formatRelative(model.lastCheckedAt)}</p>
        <div className="flex items-center gap-0.5 opacity-0 transition-opacity group-hover:opacity-100">
          <Button variant="ghost" size="iconSm" title="编辑" onClick={onEdit}>
            <Pencil className="h-3.5 w-3.5" />
          </Button>
          <Button
            variant="ghost"
            size="iconSm"
            title={isEnabled ? '停用（不参与调度）' : '启用'}
            onClick={onToggleEnabled}
          >
            <Power className={cn('h-3.5 w-3.5', !isEnabled && 'text-amber-500')} />
          </Button>
        </div>
      </div>
    </div>
  )
}
