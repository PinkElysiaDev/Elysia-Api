import { Fragment, useMemo, useState } from 'react'
import { ChevronRight, Layers, Pencil, Plus, Trash2, X } from 'lucide-react'
import { PageHeader } from '@/components/page-header'
import { RoleWatermark } from '@/components/role-watermark'
import { Button } from '@/components/ui/button'
import { Switch } from '@/components/ui/switch'
import { Seg } from '@/components/ui/seg'
import { TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { AsyncState } from '@/components/ui/states'
import { ExpandRow } from '@/components/expand-row'
import { CapChip, Dot, StrategyBadge } from '@/components/badges'
import { useConfirm } from '@/components/ui/confirm-dialog'
import { useToast } from '@/components/ui/use-toast'
import { useGroups, revalidate } from '@/lib/hooks'
import { api } from '@/lib/api'
import { cn, formatNumber } from '@/lib/utils'
import type { ModelGroup, GroupStrategy } from '@/lib/types'
import { GroupFormDialog } from './groups/group-form'

type StrategyFilter = 'all' | GroupStrategy

const STRATEGY_OPTIONS: { value: StrategyFilter; label: string }[] = [
  { value: 'all', label: '全部' },
  { value: 'round-robin', label: '轮询' },
  { value: 'sequential', label: '顺序' },
  { value: 'random', label: '随机' },
]

export function GroupsPage() {
  const toast = useToast()
  const { confirm, dialog } = useConfirm()
  const { data, isLoading, error, mutate } = useGroups()
  const [strategyFilter, setStrategyFilter] = useState<StrategyFilter>('all')
  const [editing, setEditing] = useState<ModelGroup | null>(null)
  const [formOpen, setFormOpen] = useState(false)
  const [expanded, setExpanded] = useState<Record<string, boolean>>({})
  const [switchBusyId, setSwitchBusyId] = useState<string | null>(null)

  const filtered = useMemo(
    () =>
      strategyFilter === 'all'
        ? (data ?? [])
        : (data ?? []).filter((g) => g.strategy === strategyFilter),
    [data, strategyFilter],
  )
  const enabledCount = (data ?? []).filter((g) => g.enabled).length

  function openCreate() {
    setEditing(null)
    setFormOpen(true)
  }

  function openEdit(group: ModelGroup) {
    setEditing(group)
    setFormOpen(true)
  }

  async function toggleGroup(group: ModelGroup) {
    setSwitchBusyId(group.id)
    try {
      await api.updateGroup(group.id, { ...group, enabled: !group.enabled })
      await mutate()
      toast.success(group.enabled ? '已停用模型组' : '已启用模型组', group.name)
    } catch (err) {
      toast.error('操作失败', (err as Error).message)
    } finally {
      setSwitchBusyId(null)
    }
  }

  async function removeMember(group: ModelGroup, model: string) {
    try {
      await api.updateGroup(group.id, { ...group, models: group.models.filter((m) => m !== model) })
      await Promise.all([mutate(), revalidate.models()])
      toast.success('已移除成员', `${model} · ${group.name}`)
    } catch (err) {
      toast.error('移除失败', (err as Error).message)
    }
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
    <>
      <RoleWatermark className="-right-8 top-0 opacity-[0.05] dark:opacity-[0.08]" />

      <div className="relative z-[1] space-y-6">
        <PageHeader
          title="模型组"          actions={
            <Button variant="primary" onClick={openCreate}>
              <Plus className="h-4 w-4" /> 新增模型组
            </Button>
          }
        />

        {/* 策略筛选 + 汇总指标 */}
        <div className="flex flex-wrap items-center justify-between gap-3 py-1">
          <div className="flex flex-wrap items-center gap-3">
            <span className="text-2xs font-semibold uppercase tracking-wider text-muted-foreground">调度策略</span>
            <Seg aria-label="策略筛选" options={STRATEGY_OPTIONS} value={strategyFilter} onChange={setStrategyFilter} />
          </div>
          <div className="flex items-center gap-4 text-xs">
            <span className="tnum flex items-center gap-3 text-muted-foreground font-mono">
              <span className="flex items-center gap-1.5">
                <span className="h-2 w-2 rounded-full bg-jade" />
                <b className="font-semibold text-foreground">{enabledCount}</b> 启用
              </span>
              <span className="flex items-center gap-1.5">
                <span className="h-2 w-2 rounded-full bg-ember" />
                <b className="font-semibold text-foreground">{(data ?? []).length - enabledCount}</b> 停用
              </span>
            </span>
            <span className="h-3 w-px bg-border/70" />
            <span className="text-muted-foreground font-mono">
              共 <b className="tnum font-semibold text-foreground">{(data ?? []).length}</b> 个模型组
            </span>
          </div>
        </div>

        <AsyncState
          isLoading={isLoading}
          error={error}
          data={data}
          onRetry={() => mutate()}
          loadingColumns={9}
          emptyIcon={<Layers className="h-7 w-7" />}
          emptyTitle="暂无任何模型组"
          emptyDescription="创建你的第一个模型组，聚合多渠道模型实现负载均衡与故障自动转移。"
          emptyAction={
            <Button variant="primary" onClick={openCreate}>
              <Plus className="h-4 w-4" /> 新增模型组
            </Button>
          }
        >
          {() => (
            <div className="overflow-x-auto">
              <table className="w-full text-sm">
                <TableHeader className="bg-secondary/20">
                  <TableRow className="border-b border-border/60 hover:bg-transparent">
                    <TableHead className="w-[38px] px-0 text-center" />
                    <TableHead className="py-3.5 font-semibold text-2xs uppercase tracking-wider text-muted-foreground">组名称 / 类型</TableHead>
                    <TableHead className="py-3.5 num text-center font-semibold text-2xs uppercase tracking-wider text-muted-foreground">成员数</TableHead>
                    <TableHead className="py-3.5 font-semibold text-2xs uppercase tracking-wider text-muted-foreground">聚合模型列表</TableHead>
                    <TableHead className="py-3.5 text-center font-semibold text-2xs uppercase tracking-wider text-muted-foreground">调度策略</TableHead>
                    <TableHead className="py-3.5 text-center font-semibold text-2xs uppercase tracking-wider text-muted-foreground">重试规则</TableHead>
                    <TableHead className="py-3.5 text-center font-semibold text-2xs uppercase tracking-wider text-muted-foreground">并发 / 限额</TableHead>
                    <TableHead className="py-3.5 text-center font-semibold text-2xs uppercase tracking-wider text-muted-foreground">状态</TableHead>
                    <TableHead className="py-3.5 pr-5 text-right font-semibold text-2xs uppercase tracking-wider text-muted-foreground">操作</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody className="divide-y divide-border/30">
                  {filtered.map((group) => {
                    const isOpen = !!expanded[group.id]
                    const members = group.models
                    return (
                      <Fragment key={group.id}>
                        <TableRow className="transition-colors hover:bg-secondary/30">
                          <TableCell className="w-[38px] px-0 text-center">
                            <button
                              type="button"
                              onClick={() => setExpanded((p) => ({ ...p, [group.id]: !p[group.id] }))}
                              className="rounded-md p-1.5 text-muted-foreground transition-colors hover:text-foreground hover:bg-secondary"
                              aria-label={isOpen ? '收起' : '展开'}
                            >
                              <ChevronRight className={cn('h-4 w-4 transition-transform duration-200', isOpen && 'rotate-90 text-primary')} />
                            </button>
                          </TableCell>
                          <TableCell className="py-3.5 font-medium text-foreground">
                            <div>{group.name}</div>
                            <div className="mt-1 flex flex-wrap gap-1">
                              <CapChip>{(group.type || 'llm').toUpperCase()}</CapChip>
                              {group.visionCapable && <CapChip>视觉</CapChip>}
                              {group.toolsCapable && <CapChip>工具</CapChip>}
                            </div>
                          </TableCell>
                          <TableCell className="py-3.5 num text-center text-foreground font-semibold">{members.length}</TableCell>
                          <TableCell className="py-3.5">
                            <div className="flex flex-wrap gap-1">
                              {members.slice(0, 3).map((m) => (
                                <span
                                  key={m}
                                  className="max-w-[160px] truncate rounded border border-border bg-card px-1.5 py-0.5 font-mono text-2xs text-muted-foreground"
                                  title={m}
                                >
                                  {m}
                                </span>
                              ))}
                              {members.length > 3 && (
                                <span className="rounded border border-dashed border-border px-1.5 py-0.5 font-mono text-2xs text-muted-foreground">
                                  +{members.length - 3}
                                </span>
                              )}
                            </div>
                          </TableCell>
                          <TableCell className="py-3.5 text-center">
                            <StrategyBadge strategy={group.strategy} />
                          </TableCell>
                          <TableCell className="py-3.5 num text-center text-xs text-muted-foreground">
                            {group.maxRetries} 次 · {group.retryInterval}ms
                          </TableCell>
                          <TableCell className="py-3.5 text-center text-xs text-muted-foreground">
                            <span className="tnum block">{group.maxConcurrency ? `并发 ${group.maxConcurrency}` : '并发无限制'}</span>
                            <span className="tnum block">
                              {group.dailyLimitMaxRequests ? `${formatNumber(group.dailyLimitMaxRequests)} 次/日` : '请求无限制'}
                            </span>
                          </TableCell>
                          <TableCell className="py-3.5 text-center">
                            <Switch
                              checked={group.enabled}
                              disabled={switchBusyId === group.id}
                              onCheckedChange={() => toggleGroup(group)}
                              aria-label={`${group.enabled ? '停用' : '启用'} ${group.name}`}
                            />
                          </TableCell>
                          <TableCell className="py-3.5 pr-5 text-right">
                            <div className="flex items-center justify-end gap-1">
                              <Button variant="ghost" size="iconSm" title="编辑" onClick={() => openEdit(group)}>
                                <Pencil className="h-3.5 w-3.5" />
                              </Button>
                              <Button variant="danger" size="iconSm" title="删除" onClick={() => handleDelete(group)}>
                                <Trash2 className="h-3.5 w-3.5" />
                              </Button>
                            </div>
                          </TableCell>
                        </TableRow>
                        <ExpandRow open={isOpen} colSpan={9} className="bg-secondary/15 pl-12 py-3">
                          {() => members.length === 0 ? (
                            <p className="text-xs text-muted-foreground">该组暂无聚合成员模型，点击编辑即可勾选关联。</p>
                          ) : (
                            <div className="flex flex-wrap gap-2">
                              {members.map((m) => (
                                <span
                                  key={m}
                                  className="group/chip inline-flex items-center gap-1.5 rounded-lg border border-border/80 bg-card px-2.5 py-1 font-mono text-xs text-muted-foreground shadow-sm"
                                >
                                  <Dot state="ok" />
                                  <span className="max-w-[240px] truncate">{m}</span>
                                  <button
                                    type="button"
                                    title="移除成员"
                                    aria-label={`移除 ${m}`}
                                    onClick={() => removeMember(group, m)}
                                    className="rounded p-0.5 text-muted-foreground/60 transition-colors hover:text-ember"
                                  >
                                    <X className="h-3 w-3" />
                                  </button>
                                </span>
                              ))}
                            </div>
                          )}
                        </ExpandRow>
                      </Fragment>
                    )
                  })}
                  {filtered.length === 0 && (
                    <TableRow>
                      <TableCell colSpan={9} className="py-12 text-center text-sm text-muted-foreground">
                        暂无匹配策略的模型组
                      </TableCell>
                    </TableRow>
                  )}
                </TableBody>
              </table>
            </div>
        )}
      </AsyncState>

      <GroupFormDialog open={formOpen} onOpenChange={setFormOpen} group={editing} />
      {dialog}
    </div>
    </>
  )
}
