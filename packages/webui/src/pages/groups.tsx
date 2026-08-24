import { Fragment, useMemo, useState } from 'react'
import { ChevronRight, Layers, Pencil, Plus, Trash2, X } from 'lucide-react'
import { PageHeader } from '@/components/page-header'
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
      <PageHeader
        title="模型组"
        description="把多个上游模型聚合为一个对客户端暴露的模型 ID，按策略调度与重试。"
        actions={
          <Button variant="primary" onClick={openCreate}>
            <Plus /> 新增模型组
          </Button>
        }
      />

      {/* 策略筛选 + 汇总 */}
      <div className="flex flex-wrap items-center gap-2.5 border-b border-border py-3">
        <Seg aria-label="策略筛选" options={STRATEGY_OPTIONS} value={strategyFilter} onChange={setStrategyFilter} />
        <span className="tnum flex items-center gap-x-4 text-xs text-muted-foreground">
          <span>
            <b className="font-semibold text-jade">{enabledCount}</b> 启用
          </span>
          <span>
            <b className="font-semibold text-ember">{(data ?? []).length - enabledCount}</b> 停用
          </span>
        </span>
        <span className="ml-auto text-xs text-muted-foreground">
          共 <b className="tnum font-semibold text-foreground">{(data ?? []).length}</b> 个组
        </span>
      </div>

      <AsyncState
        isLoading={isLoading}
        error={error}
        data={data}
        onRetry={() => mutate()}
        loadingColumns={9}
        emptyIcon={<Layers className="h-7 w-7" />}
        emptyTitle="还没有模型组"
        emptyDescription="创建模型组，把多个模型聚合为一个对外模型 ID。"
        emptyAction={
          <Button variant="primary" onClick={openCreate}>
            <Plus /> 新增模型组
          </Button>
        }
      >
        {() => (
          <div className="overflow-x-auto">
            <table className="w-full text-sm">
              <TableHeader>
                <TableRow>
                  <TableHead className="w-[34px] px-0 text-center" />
                  <TableHead>名称 / 类型</TableHead>
                  <TableHead className="num text-center">模型数</TableHead>
                  <TableHead>成员</TableHead>
                  <TableHead className="text-center">策略</TableHead>
                  <TableHead className="text-center">重试</TableHead>
                  <TableHead className="text-center">并发 / 日限额</TableHead>
                  <TableHead className="text-center">启停</TableHead>
                  <TableHead className="text-center">操作</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {filtered.map((group) => {
                  const isOpen = !!expanded[group.id]
                  const members = group.models ?? []
                  return (
                    <Fragment key={group.id}>
                      <TableRow>
                        <TableCell className="w-[34px] px-0 text-center">
                          <button
                            type="button"
                            onClick={() => setExpanded((p) => ({ ...p, [group.id]: !p[group.id] }))}
                            className="rounded-md p-1 text-muted-foreground transition-colors hover:text-foreground"
                            aria-label={isOpen ? '收起' : '展开'}
                          >
                            <ChevronRight className={cn('h-[15px] w-[15px] transition-transform duration-200', isOpen && 'rotate-90 text-rose')} />
                          </button>
                        </TableCell>
                        <TableCell className="font-semibold">
                          {group.name}
                          <span className="mt-1 flex flex-wrap gap-1">
                            <CapChip>{(group.type || 'llm').toUpperCase()}</CapChip>
                            {group.visionCapable && <CapChip>视觉</CapChip>}
                            {group.toolsCapable && <CapChip>工具</CapChip>}
                          </span>
                        </TableCell>
                        <TableCell className="num text-center">{members.length}</TableCell>
                        <TableCell>
                          <span className="flex flex-wrap gap-1">
                            {members.slice(0, 3).map((m) => (
                              <span
                                key={m}
                                className="max-w-[160px] truncate rounded border border-border px-1.5 py-px font-mono text-2xs text-muted-foreground"
                                title={m}
                              >
                                {m}
                              </span>
                            ))}
                            {members.length > 3 && (
                              <span className="rounded border border-dashed border-border px-1.5 py-px font-mono text-2xs text-muted-foreground">
                                +{members.length - 3}
                              </span>
                            )}
                          </span>
                        </TableCell>
                        <TableCell className="text-center">
                          <StrategyBadge strategy={group.strategy} />
                        </TableCell>
                        <TableCell className="num text-center text-xs">
                          {group.maxRetries} · {group.retryInterval}ms
                        </TableCell>
                        <TableCell className="text-center text-xs text-muted-foreground">
                          <span className="tnum block">{group.maxConcurrency ? `并发 ${group.maxConcurrency}` : '并发不限'}</span>
                          <span className="tnum block">
                            {group.dailyLimitMaxRequests ? `${formatNumber(group.dailyLimitMaxRequests)} 次/日` : '请求不限'}
                          </span>
                        </TableCell>
                        <TableCell className="text-center">
                          <Switch
                            checked={group.enabled}
                            disabled={switchBusyId === group.id}
                            onCheckedChange={() => toggleGroup(group)}
                            aria-label={`${group.enabled ? '停用' : '启用'} ${group.name}`}
                          />
                        </TableCell>
                        <TableCell className="text-center">
                          <div className="flex items-center justify-center gap-0.5">
                            <Button variant="ghost" size="iconSm" title="编辑" onClick={() => openEdit(group)}>
                              <Pencil />
                            </Button>
                            <Button variant="danger" size="iconSm" title="删除" onClick={() => handleDelete(group)}>
                              <Trash2 />
                            </Button>
                          </div>
                        </TableCell>
                      </TableRow>
                      <ExpandRow open={isOpen} colSpan={9} className="pl-11">
                        {() => members.length === 0 ? (
                          <p className="py-2 text-sm text-muted-foreground">该组还没有成员，编辑可添加。</p>
                        ) : (
                          <div className="flex flex-wrap gap-[7px]">
                            {members.map((m) => (
                              <span
                                key={m}
                                className="group/chip inline-flex items-center gap-1.5 rounded-md border border-input bg-card px-[9px] py-1 font-mono text-xs text-muted-foreground"
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
                    <TableCell colSpan={9} className="py-8 text-center text-sm text-muted-foreground">
                      没有匹配的模型组
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
    </>
  )
}
