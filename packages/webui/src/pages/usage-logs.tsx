import { useEffect, useMemo, useState } from 'react'
import { useLocation, useNavigate } from 'react-router-dom'
import { Download, RotateCcw, ScrollText, Zap } from 'lucide-react'
import { PageHeader } from '@/components/page-header'
import { RoleWatermark } from '@/components/role-watermark'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { PaginationBar } from '@/components/pagination'
import { CodePill, StreamIcon } from '@/components/badges'
import { LogDetailSheet } from './usage-logs/log-detail-sheet'
import { Seg } from '@/components/ui/seg'
import { AsyncState } from '@/components/ui/states'
import { UsageFilterBar } from '@/components/usage-filter-bar'
import { useConfirm } from '@/components/ui/confirm-dialog'
import { useToast } from '@/components/ui/use-toast'
import { useUsageLogs, revalidate, useDebouncedValue } from '@/lib/hooks'
import { useUsageFilters } from '@/lib/usage-filters'
import { api } from '@/lib/api'
import { downloadJSON, formatDateTime, formatDuration, formatNumber, isSuccessStatus } from '@/lib/utils'

const PAGE_SIZE = 20

type StatusView = 'all' | 'ok' | 'fail'

export function UsageLogsPage() {
  const toast = useToast()
  const { confirm, dialog } = useConfirm()
  const location = useLocation()
  const navigate = useNavigate()
  const [statusCodeInput, setStatusCodeInput] = useState('')
  const statusCode = useDebouncedValue(statusCodeInput)
  const [statusView, setStatusView] = useState<StatusView>('all')
  const [page, setPage] = useState(0)
  const [detailId, setDetailId] = useState<string | null>(null)

  // 总览「最近失败」跳转：带 openDetail 状态直接打开抽屉（一次性消费，
  // 否则刷新页面会因 history.state 仍在而重开抽屉）。
  useEffect(() => {
    const open = (location.state as { openDetail?: string } | null)?.openDetail
    if (!open) return
    setDetailId(open)
    navigate(location.pathname, { replace: true, state: {} })
  }, [location.state, location.pathname, navigate])

  // 共用筛选(时间窗/组/模型/源/调用方):任一变化即重置分页。
  const filters = useUsageFilters(() => setPage(0))
  const sourceLabelById = useMemo(() => {
    const map = new Map<string, string>()
    for (const source of filters.sources) map.set(source.id, source.name || source.id)
    return map
  }, [filters.sources])

  const params = useMemo(
    () => ({
      ...filters.params,
      status: statusView === 'ok' ? ('success' as const) : statusView === 'fail' ? ('failed' as const) : undefined,
      statusCode: statusCode.trim() ? Number(statusCode) : undefined,
      limit: PAGE_SIZE,
      offset: page * PAGE_SIZE,
    }),
    [filters.params, statusView, statusCode, page],
  )

  const { data, isLoading, error, mutate } = useUsageLogs(params)
  const total = data?.total ?? 0
  const totalPages = Math.max(1, Math.ceil(total / PAGE_SIZE))
  // total 收缩（筛选变严/日志被清理）后把超界页码收敛回末页。
  useEffect(() => {
    if (!data || isLoading || error) return
    setPage((p) => Math.min(p, totalPages - 1))
  }, [data, isLoading, error, totalPages])

  const items = useMemo(() => data?.items ?? [], [data])

  const summary = useMemo(() => {
    const all = items
    const totals = all.reduce(
      (acc, item) => {
        if (isSuccessStatus(item.statusCode)) {
          acc.ok += 1
          acc.duration += item.durationMs || 0
        } else {
          acc.failed += 1
        }
        return acc
      },
      { ok: 0, failed: 0, duration: 0 },
    )
    return {
      ok: totals.ok,
      failed: totals.failed,
      avg: totals.ok ? totals.duration / totals.ok : 0,
      count: all.length,
    }
  }, [items])

  async function handleReset() {
    const okToReset = await confirm({
      title: '重置全部 Usage 记录？',
      description: '将永久删除所有用量日志与统计数据，此操作不可恢复。',
      confirmText: '重置',
    })
    if (!okToReset) return
    try {
      await api.usageReset()
      await Promise.all([mutate(), revalidate.usage()])
      toast.success('Usage 已重置')
      setPage(0)
    } catch (err) {
      toast.error('重置失败', (err as Error).message)
    }
  }

  function handleExport() {
    if (!data?.items?.length) {
      toast.error('没有可导出的记录')
      return
    }
    downloadJSON(`usage-logs-${filters.range}.json`, data.items)
    toast.success('已导出当前页', `${data.items.length} 条记录`)
  }

  return (
    <>
      <RoleWatermark className="-right-8 top-0 opacity-[0.05] dark:opacity-[0.08]" />

      <div className="relative z-[1] space-y-6">
        <PageHeader
          title="调用日志"
          actions={
            <>
              <Button onClick={handleExport}>
                <Download className="h-4 w-4" /> 导出日志
              </Button>
              <Button variant="danger" onClick={handleReset}>
                <RotateCcw className="h-4 w-4" /> 重置用量
              </Button>
            </>
          }
        />

        {/* 共用筛选条（含模型源维度）+ 页面专属状态筛选 + 汇总图例 */}
        <UsageFilterBar
          {...filters.barProps}
          right={
            <div className="flex items-center gap-3 text-xs font-mono">
              <span className="tnum flex flex-wrap items-center gap-3 text-muted-foreground">
                <span className="flex items-center gap-1.5">
                  <span className="h-2 w-2 rounded-full bg-jade" />
                  <b className="font-semibold text-foreground">{summary.ok}</b> 成功
                </span>
                <span className="flex items-center gap-1.5">
                  <span className="h-2 w-2 rounded-full bg-ember" />
                  <b className="font-semibold text-foreground">{summary.failed}</b> 失败
                </span>
                <span>
                  平均时延（成功）{' '}
                  <b className="font-semibold text-foreground">
                    {summary.ok ? formatDuration(summary.avg) : '—'}
                  </b>
                </span>
              </span>
              <span className="text-muted-foreground/70">（当前 {summary.count} 条）</span>
            </div>
          }
        >
          <Seg
            aria-label="状态"
            options={[
              { value: 'all', label: '全部' },
              { value: 'ok', label: '成功' },
              { value: 'fail', label: '失败' },
            ]}
            value={statusView}
            onChange={(value) => {
              setStatusView(value)
              setStatusCodeInput('')
              setPage(0)
            }}
          />
          <Input
            aria-label="状态码"
            className="w-[84px] rounded-full border-transparent bg-[var(--well)] text-xs font-mono"
            value={statusCodeInput}
            placeholder="HTTP码"
            title="精确状态码过滤（如 429、500；0 = 连接层失败）"
            onChange={(e) => {
              const value = e.target.value.replace(/[^0-9]/g, '')
              setStatusCodeInput(value)
              if (value) setStatusView('all')
              setPage(0)
            }}
          />
        </UsageFilterBar>

        <AsyncState
          isLoading={isLoading}
          error={error}
          data={data?.items}
          onRetry={() => mutate()}
          loadingColumns={8}
          emptyIcon={<ScrollText className="h-7 w-7" />}
          emptyTitle="暂无匹配日志记录"
          emptyDescription="当前筛选时间与过滤条件范围内未查询到任何请求。"
        >
          {() => (
            <div className="space-y-3">
              <div className={`overflow-x-auto transition-opacity ${isLoading ? 'opacity-50' : ''}`} aria-busy={isLoading}>
                <table className="w-full text-sm">
                  <TableHeader className="bg-secondary/20">
                    <TableRow className="border-b border-border/60 hover:bg-transparent">
                      <TableHead className="py-3.5 pl-4">请求时间</TableHead>
                      <TableHead className="py-3.5">调用凭证</TableHead>
                      <TableHead className="py-3.5">实际模型 / 路由组</TableHead>
                      <TableHead className="py-3.5 text-center">流式</TableHead>
                      <TableHead className="py-3.5 text-center">状态码</TableHead>
                      <TableHead className="py-3.5 num">首字耗时</TableHead>
                      <TableHead className="py-3.5 num">总耗时</TableHead>
                      <TableHead className="py-3.5 pr-4 num">Tokens 输入 / 输出</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody className="divide-y divide-border/30">
                  {items.map((log) => (
                    <TableRow
                      key={log.requestId}
                      role="button"
                      tabIndex={0}
                      aria-label={`查看请求 ${log.requestId} 详情`}
                      className="cursor-pointer focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-[color:color-mix(in_srgb,var(--rose)_50%,transparent)]"
                      onClick={() => setDetailId(log.requestId)}
                      onKeyDown={(e) => {
                        if (e.key === 'Enter' || e.key === ' ') {
                          e.preventDefault()
                          setDetailId(log.requestId)
                        }
                      }}
                    >
                      <TableCell className="py-3 pl-4 whitespace-nowrap font-mono text-xs text-muted-foreground">
                        {formatDateTime(log.startedAt)}
                      </TableCell>
                      <TableCell className="py-3 max-w-[120px] truncate text-xs font-mono text-foreground">{log.keyName || '—'}</TableCell>
                      <TableCell className="py-3">
                        <span className="block truncate text-xs font-semibold text-foreground">{log.modelName || '—'}</span>
                        <span className="sub font-mono">
                          {log.groupName || '—'}
                          {log.sourceId ? ` · ${sourceLabelById.get(log.sourceId) || log.sourceId}` : ''}
                        </span>
                      </TableCell>
                      <TableCell className="py-3 text-center">
                        <StreamIcon streaming={log.stream} />
                      </TableCell>
                      <TableCell className="py-3 text-center">
                        <CodePill code={log.statusCode} />
                      </TableCell>
                      <TableCell className="py-3 num">
                        {log.firstByteMs > 0 ? formatDuration(log.firstByteMs) : '—'}
                      </TableCell>
                      <TableCell className="py-3 num font-medium text-foreground">{formatDuration(log.durationMs)}</TableCell>
                      <TableCell className="py-3 pr-4 num">
                        <span className="font-mono">↑{formatNumber(log.inputTokens)}</span>{' '}
                        <span className="text-muted-foreground font-mono">↓{formatNumber(log.outputTokens)}</span>
                        {log.cacheHitTokens != null && log.cacheHitTokens > 0 && log.inputTokens > 0 && (
                          <span
                            className="ml-1.5 whitespace-nowrap font-mono text-xs text-amber"
                            title={`缓存命中 ${formatNumber(log.cacheHitTokens)} tokens`}
                          >
                            <Zap className="mr-px inline h-3 w-3 align-[-1px]" />
                            {Math.round((log.cacheHitTokens / log.inputTokens) * 100)}%
                          </span>
                        )}
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </table>
            </div>

            {/* 分页栏 */}
            <PaginationBar total={total} page={page} totalPages={totalPages} onNavigate={setPage} loading={isLoading} />
          </div>
        )}
      </AsyncState>

      <LogDetailSheet id={detailId} onClose={() => setDetailId(null)} />
      {dialog}
    </div>
    </>
  )
}
