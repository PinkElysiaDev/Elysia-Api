import { Fragment, useEffect, useMemo, useRef, useState } from 'react'
import {
  Boxes,
  Check,
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
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { Switch } from '@/components/ui/switch'
import { AsyncState } from '@/components/ui/states'
import { ExpandRow } from '@/components/expand-row'
import { CapChip, Dot, PlatformBadge } from '@/components/badges'
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
  const [keyword, setKeyword] = useState('')
  const [editing, setEditing] = useState<ModelSource | null>(null)
  const [formOpen, setFormOpen] = useState(false)
  const [busyId, setBusyId] = useState<string | null>(null)
  const [switchBusyId, setSwitchBusyId] = useState<string | null>(null)
  const [refreshingAll, setRefreshingAll] = useState(false)
  const [expanded, setExpanded] = useState<Record<string, boolean>>({})
  // 源内模型的选择状态、面板检索（跨组全局 + 组内）、分组折叠、单模型编辑。
  const [selected, setSelected] = useState<Record<string, boolean>>({})
  const [globalSearch, setGlobalSearch] = useState<Record<string, string>>({})
  const [groupSearch, setGroupSearch] = useState<Record<string, string>>({})
  const [collapsedGroups, setCollapsedGroups] = useState<Record<string, boolean>>({})
  const [batchBusy, setBatchBusy] = useState(false)
  const [editingModel, setEditingModel] = useState<Model | null>(null)
  const [modelEditOpen, setModelEditOpen] = useState(false)
  const [quickCreate, setQuickCreate] = useState<{ source: ModelSource; models: Model[] } | null>(null)
  const [addToGroup, setAddToGroup] = useState<Model[] | null>(null)

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

  // bar 搜索：名称 / 平台 / Base URL
  const kw = keyword.trim().toLowerCase()
  const filtered = useMemo(() => {
    if (!kw) return data ?? []
    return (data ?? []).filter(
      (s) => `${s.name} ${s.platform} ${s.baseUrl}`.toLowerCase().includes(kw),
    )
  }, [data, kw])

  const enabledCount = (data ?? []).filter((s) => s.enabled).length
  const disabledCount = (data ?? []).length - enabledCount

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

  // 批量启停：对选中模型循环 PATCH enabled，完成后统一刷新并汇总。
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

  // 后台拉取任务：任一源进行中时快速轮询（3s），空闲时回落 60s 常规刷新。
  const anyRefreshing = (data ?? []).some((s) => s.refreshState?.refreshing)
  useEffect(() => {
    if (!anyRefreshing) return
    const id = setInterval(() => {
      revalidate.sources()
      revalidate.models()
    }, 3000)
    return () => clearInterval(id)
  }, [anyRefreshing])

  // 任务完成通知：只提示本页面见过的「进行中 → 完成」迁移（按 lastFinishedAt
  // 去重），首次加载的历史结果不弹。
  const seenRefreshing = useRef<Record<string, boolean>>({})
  const notifiedFinish = useRef<Record<string, string>>({})
  useEffect(() => {
    for (const source of data ?? []) {
      const state = source.refreshState
      const isRefreshing = !!state?.refreshing
      const wasRefreshing = !!seenRefreshing.current[source.id]
      seenRefreshing.current[source.id] = isRefreshing
      if (!state || isRefreshing || !wasRefreshing || !state.lastFinishedAt) continue
      if (notifiedFinish.current[source.id] === state.lastFinishedAt) continue
      notifiedFinish.current[source.id] = state.lastFinishedAt
      if (state.lastError) {
        toast.error('拉取失败', `${source.name}：${state.lastError}`)
      } else {
        const changes = [
          state.lastAdded ? `新增 ${state.lastAdded}` : '',
          state.lastRemoved ? `移除 ${state.lastRemoved}` : '',
        ]
          .filter(Boolean)
          .join('，')
        toast.success('拉取完成', `${source.name} 共 ${state.lastCount ?? 0} 个模型${changes ? `（${changes}）` : ''}`)
      }
    }
  }, [data, toast])

  /** 该源的后台拉取进行中：锁定其模型相关操作，避免合并期间冲突误操作。 */
  function sourceBusy(source: ModelSource): boolean {
    return !!source.refreshState?.refreshing
  }

  async function refreshAll() {
    setRefreshingAll(true)
    try {
      const result = await api.refreshModels()
      await Promise.all([mutate(), revalidate.models()])
      toast.success('已开始后台刷新', `已启动 ${result.started}/${result.total} 个源的拉取任务`)
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
      setExpanded((prev) => ({ ...prev, [source.id]: true }))
      await mutate()
      if (result.alreadyRunning) {
        toast.success('已在后台拉取中', `${source.name} 正在拉取，完成后会提示`)
      } else {
        toast.success('已在后台开始拉取', `${source.name} · 页面可继续其他操作`)
      }
    } catch (err) {
      toast.error('发起拉取失败', (err as Error).message)
    } finally {
      setBusyId(null)
    }
  }

  async function toggleSource(source: ModelSource) {
    setSwitchBusyId(source.id)
    try {
      // 专用轻量端点：整源 PUT 会触发「保存后自动同步模型」（重拉上游），启停无需。
      await api.setSourceEnabled(source.id, !source.enabled)
      await Promise.all([mutate(), revalidate.models()])
      toast.success(source.enabled ? '已停用模型源' : '已启用模型源', source.name)
    } catch (err) {
      toast.error('操作失败', (err as Error).message)
    } finally {
      setSwitchBusyId(null)
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
        description="管理上游 API 供应商与其聚合的模型能力，支持多 Key 轮询鉴权与自动能力回填。"
        actions={
          <>
            {catalogStatus?.enabled && (
              <CapChip
                className="max-w-[280px] truncate"
                title={
                  catalogStatus.entries > 0
                    ? `模型能力目录已加载 ${catalogStatus.entries} 个模型（models.dev），刷新模型时自动回填视觉/工具等能力`
                    : '能力目录尚未加载成功：模型能力不会被自动回填，可在模型编辑中手动开启；服务器需可访问 models.dev（或配置 modelCatalog.url/proxy）'
                }
              >
                {catalogStatus.entries > 0 ? `能力目录 ${catalogStatus.entries} 模型` : '能力目录未加载'}
              </CapChip>
            )}
            <Button onClick={refreshAll} disabled={refreshingAll}>
              <RefreshCw className={cn('h-4 w-4', (refreshingAll || anyRefreshing) && 'animate-spin')} /> 刷新全部模型
            </Button>
            <Button variant="primary" onClick={openCreate}>
              <Plus className="h-4 w-4" /> 新增模型源
            </Button>
          </>
        }
      />

      {/* 搜索工具条与统计卡片 */}
      <div className="flex flex-wrap items-center justify-between gap-3 rounded-xl border border-border/80 bg-card p-3 shadow-soft">
        <div className="relative w-full sm:w-72">
          <Search className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
          <Input
            className="pl-9 text-xs"
            type="search"
            placeholder="搜索源名称 / 平台 / 接口地址…"
            value={keyword}
            onChange={(e) => setKeyword(e.target.value)}
          />
        </div>
        <div className="flex items-center gap-4 text-xs">
          <span className="tnum flex items-center gap-3 text-muted-foreground">
            <span className="flex items-center gap-1.5">
              <span className="h-2 w-2 rounded-full bg-jade" />
              <b className="font-semibold text-foreground">{enabledCount}</b> 启用
            </span>
            <span className="flex items-center gap-1.5">
              <span className="h-2 w-2 rounded-full bg-ember" />
              <b className="font-semibold text-foreground">{disabledCount}</b> 停用
            </span>
            <span className="flex items-center gap-1.5">
              聚合模型 <b className="font-semibold text-foreground">{formatNumber(models?.length ?? 0)}</b>
            </span>
          </span>
          <span className="h-3 w-px bg-border" />
          <span className="text-muted-foreground">
            共 <b className="tnum font-semibold text-foreground">{(data ?? []).length}</b> 个模型源
          </span>
        </div>
      </div>

      <AsyncState
        isLoading={isLoading}
        error={error}
        data={data}
        onRetry={() => mutate()}
        loadingColumns={8}
        emptyIcon={<Boxes className="h-7 w-7" />}
        emptyTitle="暂无任何模型源"
        emptyDescription="添加你的第一个上游供应商（OpenAI / Anthropic / DeepSeek / 自定义网关），开始聚合模型。"
        emptyAction={
          <Button variant="primary" onClick={openCreate}>
            <Plus className="h-4 w-4" /> 新增模型源
          </Button>
        }
      >
        {() => (
          <div className="overflow-hidden rounded-xl border border-border/80 bg-card shadow-soft">
            <div className="overflow-x-auto">
              <table className="w-full text-sm">
                <TableHeader className="bg-secondary/40">
                  <TableRow className="border-b border-border/80 hover:bg-transparent">
                    <TableHead className="w-[38px] px-0 text-center" />
                    <TableHead className="py-3.5 font-semibold text-xs uppercase tracking-wider text-muted-foreground">源名称 / ID</TableHead>
                    <TableHead className="py-3.5 text-center font-semibold text-xs uppercase tracking-wider text-muted-foreground">平台架构</TableHead>
                    <TableHead className="py-3.5 font-semibold text-xs uppercase tracking-wider text-muted-foreground">Base URL</TableHead>
                    <TableHead className="py-3.5 num text-center font-semibold text-xs uppercase tracking-wider text-muted-foreground">模型数</TableHead>
                    <TableHead className="py-3.5 text-center font-semibold text-xs uppercase tracking-wider text-muted-foreground">同步策略</TableHead>
                    <TableHead className="py-3.5 text-center font-semibold text-xs uppercase tracking-wider text-muted-foreground">状态</TableHead>
                    <TableHead className="py-3.5 pr-5 text-right font-semibold text-xs uppercase tracking-wider text-muted-foreground">操作</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody className="divide-y divide-border/60">
                  {filtered.map((source) => {
                    const sourceModels = modelsBySource.get(source.id) ?? []
                    const isOpen = !!expanded[source.id]
                    // 后台拉取进行中：锁定该源的模型相关操作（避免合并期间冲突误操作），
                    // 其余源与页面功能不受影响。
                    const busy = sourceBusy(source)
                    const groups = buildModelGroups(source, sourceModels)
                    const globalKeyword = (globalSearch[source.id] ?? '').trim().toLowerCase()
                    const matchesGlobal = (m: Model) =>
                      !globalKeyword ||
                      `${m.id} ${m.name} ${m.sourceName ?? ''}`.toLowerCase().includes(globalKeyword)
                    const selectedCount = selectedModelsOf(source.id).length
                    const allVisibleModels = groups
                      .map((g) => {
                        const kw2 = (groupSearch[`${source.id}:${g.key}`] ?? '').trim().toLowerCase()
                        return kw2
                          ? g.models.filter(
                              (m) =>
                                matchesGlobal(m) &&
                                `${m.id} ${m.name} ${m.sourceName ?? ''}`.toLowerCase().includes(kw2),
                            )
                          : g.models.filter(matchesGlobal)
                      })
                      .flat()
                    return (
                      <Fragment key={source.id}>
                        <TableRow className="transition-colors hover:bg-secondary/30">
                          <TableCell className="w-[38px] px-0 text-center">
                            <button
                              type="button"
                              onClick={() => toggleExpand(source.id)}
                              className="rounded-md p-1.5 text-muted-foreground transition-colors hover:text-foreground hover:bg-secondary"
                              aria-label={isOpen ? '收起' : '展开'}
                            >
                              <ChevronRight
                                className={cn(
                                  'h-4 w-4 transition-transform duration-200',
                                  isOpen && 'rotate-90 text-primary',
                                )}
                              />
                            </button>
                          </TableCell>
                          <TableCell className="py-3.5 font-medium text-foreground">
                            <span className="inline-flex items-center gap-1.5">
                              {source.name}
                              {busy && <RefreshCw className="h-3.5 w-3.5 animate-spin text-primary" />}
                            </span>
                            <span className="sub font-mono">{source.id}</span>
                          </TableCell>
                          <TableCell className="py-3.5 text-center">
                            <PlatformBadge platform={source.platform} />
                          </TableCell>
                          <TableCell className="py-3.5 max-w-[220px]">
                            <span className="block truncate font-mono text-xs text-muted-foreground" title={source.baseUrl}>
                              {source.baseUrl}
                            </span>
                          </TableCell>
                          <TableCell className="py-3.5 num text-center font-semibold text-foreground">{sourceModels.length}</TableCell>
                          <TableCell className="py-3.5 text-center">
                            <CapChip>{source.autoFetchModels ? '自动拉取' : '手动维护'}</CapChip>
                          </TableCell>
                          <TableCell className="py-3.5 text-center">
                            <Switch
                              checked={source.enabled}
                              disabled={switchBusyId === source.id || busy}
                              onCheckedChange={() => toggleSource(source)}
                              aria-label={`${source.enabled ? '停用' : '启用'} ${source.name}`}
                            />
                          </TableCell>
                          <TableCell className="py-3.5 pr-5 text-right">
                            <div className="flex items-center justify-end gap-1">
                              {source.autoFetchModels && (
                                <Button
                                  variant="ghost"
                                  size="iconSm"
                                  title={busy ? '后台拉取进行中' : '拉取模型'}
                                  disabled={busyId === source.id || !source.enabled || busy}
                                  onClick={() => handleFetch(source)}
                                >
                                  <RefreshCw className={cn('h-3.5 w-3.5', (busy || busyId === source.id) && 'animate-spin')} />
                                </Button>
                              )}
                              <Button
                                variant="ghost"
                                size="iconSm"
                                title="编辑"
                                disabled={busy}
                                onClick={() => openEdit(source)}
                              >
                                <Pencil className="h-3.5 w-3.5" />
                              </Button>
                              <Button
                                variant="danger"
                                size="iconSm"
                                title="删除"
                                disabled={busy}
                                onClick={() => handleDelete(source)}
                              >
                                <Trash2 className="h-3.5 w-3.5" />
                              </Button>
                            </div>
                          </TableCell>
                        </TableRow>
                        <ExpandRow open={isOpen} colSpan={8} className="bg-secondary/15 pl-12 py-3">
                        {() => sourceModels.length === 0 ? (
                          <div className="flex items-center gap-2 py-3 text-sm text-muted-foreground">
                            <Boxes className="h-4 w-4" />
                            暂无模型。
                            {source.autoFetchModels
                              ? '点击右侧刷新按钮拉取，或在编辑中添加手动模型。'
                              : '在编辑中添加手动模型。'}
                          </div>
                        ) : (
                          <div className="space-y-3">
                            {/* 顶部工具条：跨组搜索 + 更新时间 + 全局选择 + 批量操作 */}
                            <div className="flex flex-wrap items-center gap-2">
                              {groups.length > 1 && (
                                <div className="relative w-56">
                                  <Search className="pointer-events-none absolute left-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-muted-foreground" />
                                  <Input
                                    className="h-[29px] pl-8 text-xs"
                                    placeholder="跨组搜索全部模型…"
                                    value={globalSearch[source.id] ?? ''}
                                    onChange={(e) =>
                                      setGlobalSearch((prev) => ({ ...prev, [source.id]: e.target.value }))
                                    }
                                  />
                                </div>
                              )}
                              <Button
                                variant="ghost"
                                size="sm"
                                disabled={busy || allVisibleModels.length === 0}
                                onClick={() => setModelsSelected(allVisibleModels, true)}
                              >
                                全选
                              </Button>
                              <Button
                                variant="ghost"
                                size="sm"
                                disabled={busy}
                                onClick={() => setModelsSelected(sourceModels, false)}
                              >
                                清空选择
                              </Button>
                              <div className="ml-auto flex flex-wrap items-center gap-2">
                                <span className="tnum text-xs text-muted-foreground">已选 {selectedCount}</span>
                                <Button
                                  variant="outline"
                                  size="sm"
                                  disabled={busy || selectedCount === 0 || batchBusy}
                                  onClick={() => batchSetEnabled(selectedModelsOf(source.id), true)}
                                >
                                  <Power /> 启用
                                </Button>
                                <Button
                                  variant="outline"
                                  size="sm"
                                  disabled={busy || selectedCount === 0 || batchBusy}
                                  onClick={() => batchSetEnabled(selectedModelsOf(source.id), false)}
                                >
                                  <PowerOff /> 禁用
                                </Button>
                                <Button
                                  variant="outline"
                                  size="sm"
                                  disabled={busy || selectedCount === 0}
                                  onClick={() => setQuickCreate({ source, models: selectedModelsOf(source.id) })}
                                >
                                  <Plus /> 创建模型组
                                </Button>
                                <Button
                                  variant="outline"
                                  size="sm"
                                  disabled={busy || selectedCount === 0}
                                  onClick={() => setAddToGroup(selectedModelsOf(source.id))}
                                >
                                  <ListPlus /> 加入模型组
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
                                <div key={group.key}>
                                  <div className="flex flex-wrap items-center gap-2 py-1">
                                    <button
                                      type="button"
                                      onClick={() =>
                                        setCollapsedGroups((prev) => ({ ...prev, [groupStateKey]: !collapsed }))
                                      }
                                      className="rounded-md p-1 text-muted-foreground transition-colors hover:text-foreground"
                                      aria-label={collapsed ? '展开分组' : '折叠分组'}
                                    >
                                      <ChevronRight
                                        className={cn(
                                          'h-3.5 w-3.5 transition-transform',
                                          !collapsed && 'rotate-90',
                                        )}
                                      />
                                    </button>
                                    <span className="text-xs font-semibold">
                                      {group.label}
                                      {group.note ? ` · ${group.note}` : ''}
                                    </span>
                                    {group.badge && <CapChip>{group.badge}</CapChip>}
                                    <CapChip>{group.models.length} 模型</CapChip>
                                    <div className="ml-auto flex items-center gap-2">
                                      <div className="relative w-44">
                                        <Search className="pointer-events-none absolute left-2.5 top-1/2 h-3 w-3 -translate-y-1/2 text-muted-foreground" />
                                        <Input
                                          className="h-[29px] pl-7 text-xs"
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
                                    <div className="mb-1 mt-1.5">
                                      {groupVisible.length === 0 ? (
                                        <p className="py-2 text-center text-sm text-muted-foreground">
                                          没有匹配的模型
                                        </p>
                                      ) : (
                                        <div className="flex flex-wrap gap-[7px]">
                                          {groupVisible.map((model) => (
                                            <ModelChip
                                              key={`${group.key}-${model.sourceId}-${model.id}`}
                                              model={model}
                                              checked={!!selected[`${source.id}:${model.id}`]}
                                              locked={busy}
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
                            {/* 脚注：可用性检测 / 上次拉取 */}
                            <p className="tnum mt-2.5 flex flex-wrap gap-x-3.5 text-2xs text-muted-foreground">
                              <span>
                                上次拉取{' '}
                                {source.updatedAt ? formatRelative(source.updatedAt) : '—'}
                              </span>
                              <span>
                                可用性检测{' '}
                                {sourceModels.some((m) => m.lastCheckedAt)
                                  ? formatRelative(
                                      sourceModels
                                        .filter((m) => m.lastCheckedAt)
                                        .map((m) => m.lastCheckedAt)
                                        .sort()
                                        .at(-1),
                                    )
                                  : '—'}
                              </span>
                            </p>
                          </div>
                        )}
                      </ExpandRow>
                    </Fragment>
                  )
                })}
                {filtered.length === 0 && (
                  <TableRow>
                    <TableCell colSpan={8} className="py-8 text-center text-sm text-muted-foreground">
                      没有匹配的模型源
                    </TableCell>
                  </TableRow>
                )}
              </TableBody>
            </table>
          </div>
        </div>
      )}
    </AsyncState>

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

/** 模型芯片：mono 名称 + 能力图标 + 类型 + max tokens；失效置灰。
 * locked：所属源后台拉取进行中——禁用选择与编辑/启停，避免合并期间冲突。 */
function ModelChip({
  model,
  checked,
  locked = false,
  onToggleSelect,
  onEdit,
  onToggleEnabled,
}: {
  model: Model
  checked: boolean
  locked?: boolean
  onToggleSelect: () => void
  onEdit: () => void
  onToggleEnabled: () => void
}) {
  const dimmed = model.enabled === false || !model.available
  const isEnabled = model.enabled !== false
  return (
    <span
      className={cn(
        'group inline-flex items-center gap-1.5 rounded-md border bg-card px-[9px] py-1 font-mono text-xs text-muted-foreground transition-colors',
        checked ? 'border-rose text-rose' : 'border-input hover:border-rose hover:text-foreground',
        (dimmed || locked) && 'opacity-50',
        locked && 'pointer-events-none',
      )}
      title={`${model.id}${model.lastCheckedAt ? ` · 检测于 ${formatRelative(model.lastCheckedAt)}` : ''}${locked ? ' · 拉取进行中' : ''}`}
    >
      <button
        type="button"
        onClick={onToggleSelect}
        disabled={locked}
        aria-label={checked ? '取消选择' : '选择'}
        aria-pressed={checked}
        className={cn(
          'flex h-3.5 w-3.5 shrink-0 items-center justify-center rounded-[3px] border transition-colors',
          checked ? 'border-rose bg-rose text-white' : 'border-border hover:border-rose',
        )}
      >
        {checked && <Check className="h-2.5 w-2.5" strokeWidth={3} />}
      </button>
      <button type="button" onClick={onToggleSelect} disabled={locked} className="max-w-[220px] truncate">
        {model.name || model.id}
      </button>
      <span className="rounded border border-border px-1 text-2xs uppercase tracking-[0.08em] text-muted-foreground">
        {(model.type || 'llm').slice(0, 3)}
      </span>
      {model.visionCapable && <Eye className="h-3 w-3 text-muted-foreground" aria-label="视觉" />}
      {model.toolsCapable && <Wrench className="h-3 w-3 text-muted-foreground" aria-label="工具" />}
      {model.maxTokens > 0 && (
        <span className="tnum text-2xs text-muted-foreground">{formatNumber(model.maxTokens)}</span>
      )}
      <span className="flex items-center gap-0.5 opacity-0 transition-opacity group-hover:opacity-100">
        <button
          type="button"
          onClick={onEdit}
          disabled={locked}
          title="编辑模型"
          aria-label="编辑模型"
          className="rounded p-0.5 text-muted-foreground hover:text-rose"
        >
          <Pencil className="h-3 w-3" />
        </button>
        <button
          type="button"
          onClick={onToggleEnabled}
          disabled={locked}
          title={isEnabled ? '停用（不参与调度）' : '启用'}
          aria-label={isEnabled ? '停用模型' : '启用模型'}
          className="rounded p-0.5 text-muted-foreground hover:text-rose"
        >
          <Dot state={isEnabled ? 'ok' : 'off'} />
        </button>
      </span>
    </span>
  )
}
