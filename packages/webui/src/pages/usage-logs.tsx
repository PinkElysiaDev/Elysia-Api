import { useEffect, useMemo, useState } from 'react'
import { useLocation, useNavigate } from 'react-router-dom'
import {
  AlertTriangle,
  ChevronLeft,
  ChevronRight,
  Download,
  MoveRight,
  RotateCcw,
  ScrollText,
  Zap,
} from 'lucide-react'
import { PageHeader } from '@/components/page-header'
import { RoleWatermark } from '@/components/role-watermark'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { Seg } from '@/components/ui/seg'
import {
  Sheet,
  SheetBody,
  SheetContent,
  SheetHeader,
  SheetSectionTitle,
  SheetTitle,
} from '@/components/ui/sheet'
import { AsyncState } from '@/components/ui/states'
import { CodePill, StreamIcon } from '@/components/badges'
import { protocolLabel } from '@/lib/protocol'
import { CopyButton } from '@/components/copy-button'
import { UsageFilterBar, type RangeKey } from '@/components/usage-filter-bar'
import { useConfirm } from '@/components/ui/confirm-dialog'
import { useToast } from '@/components/ui/use-toast'
import { useUsageLogs, useUsageFilterOptions, useMinuteTick, useSources, revalidate } from '@/lib/hooks'
import { api } from '@/lib/api'
import { colorize } from '@/lib/json-highlight'
import type { UsageBody, UsageLogDetail } from '@/lib/types'
import {
  cn,
  downloadJSON,
  formatDateTime,
  formatDuration,
  formatNumber,
  startOfRange,
  bucketedTimeISO,
  tryParseJSON,
} from '@/lib/utils'

const PAGE_SIZE = 20

type StatusView = 'all' | 'ok' | 'fail'

export function UsageLogsPage() {
  const toast = useToast()
  const { confirm, dialog } = useConfirm()
  const location = useLocation()
  const navigate = useNavigate()
  const [range, setRange] = useState<RangeKey>('7d')
  const [groupNames, setGroupNames] = useState<string[]>([])
  const [modelNames, setModelNames] = useState<string[]>([])
  const [sourceIds, setSourceIds] = useState<string[]>([])
  const [keyNames, setKeyNames] = useState<string[]>([])
  const [statusCode, setStatusCode] = useState('')
  const [statusView, setStatusView] = useState<StatusView>('all')
  const [page, setPage] = useState(0)
  const [detailId, setDetailId] = useState<string | null>(null)
  const minuteTick = useMinuteTick()

  // 总览「最近失败」跳转：带 openDetail 状态直接打开抽屉（一次性消费，
  // 否则刷新页面会因 history.state 仍在而重开抽屉）。
  useEffect(() => {
    const open = (location.state as { openDetail?: string } | null)?.openDetail
    if (!open) return
    setDetailId(open)
    navigate(location.pathname, { replace: true, state: {} })
  }, [location.state, location.pathname, navigate])

  const { groupOptions, modelOptions, keyOptions } = useUsageFilterOptions()
  const { data: sources } = useSources()
  const sourceOptions = useMemo(
    () => (sources ?? []).filter((s) => s.enabled).map((s) => ({ value: s.id, label: s.name || s.id })),
    [sources],
  )
  const sourceLabelById = useMemo(() => {
    const map = new Map<string, string>()
    for (const source of sources ?? []) map.set(source.id, source.name || source.id)
    return map
  }, [sources])

  const params = useMemo(() => {
    // to 取下一 5 分钟边界：缓存键稳定，且当前桶内新记录能进半开区间。
    // 相对窗起点用 now，避免临近午夜把 from 推到次日。
    const nowMs = minuteTick * 60_000
    const to = bucketedTimeISO(nowMs, 5 * 60_000)
    return {
      from: startOfRange(range, new Date(nowMs).toISOString()),
      to,
      groupNames: groupNames.length ? groupNames : undefined,
      modelNames: modelNames.length ? modelNames : undefined,
      sourceIds: sourceIds.length ? sourceIds : undefined,
      keyNames: keyNames.length ? keyNames : undefined,
      status: statusView === 'ok' ? ('success' as const) : statusView === 'fail' ? ('failed' as const) : undefined,
      statusCode: statusCode.trim() ? Number(statusCode) : undefined,
      limit: PAGE_SIZE,
      offset: page * PAGE_SIZE,
    }
  }, [range, groupNames, modelNames, sourceIds, keyNames, statusView, statusCode, page, minuteTick])

  const { data, isLoading, error, mutate } = useUsageLogs(params)
  const total = data?.total ?? 0
  const totalPages = Math.max(1, Math.ceil(total / PAGE_SIZE))

  useEffect(() => {
    setPage((p) => Math.min(p, totalPages - 1))
  }, [totalPages])

  const items = data?.items ?? []

  const summary = useMemo(() => {
    const all = data?.items ?? []
    const totals = all.reduce(
      (acc, item) => {
        if (item.statusCode >= 200 && item.statusCode < 400) {
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
  }, [data])

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
    downloadJSON(`usage-logs-${range}.json`, data.items)
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
          range={range}
          onRangeChange={(v) => {
            setRange(v)
            setPage(0)
          }}
          groupOptions={groupOptions}
          modelOptions={modelOptions}
          keyOptions={keyOptions}
          sourceOptions={sourceOptions}
          groupNames={groupNames}
          onGroupNamesChange={(v) => {
            setGroupNames(v)
            setPage(0)
          }}
          modelNames={modelNames}
          onModelNamesChange={(v) => {
            setModelNames(v)
            setPage(0)
          }}
          sourceIds={sourceIds}
          onSourceIdsChange={(v) => {
            setSourceIds(v)
            setPage(0)
          }}
          keyNames={keyNames}
          onKeyNamesChange={(v) => {
            setKeyNames(v)
            setPage(0)
          }}
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
              setStatusCode('')
              setPage(0)
            }}
          />
          <Input
            aria-label="状态码"
            className="w-[84px] text-xs font-mono"
            value={statusCode}
            placeholder="HTTP码"
            title="精确状态码过滤（如 429、500）"
            onChange={(e) => {
              const value = e.target.value.replace(/[^0-9]/g, '')
              setStatusCode(value)
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
              <div className="overflow-x-auto">
                <table className="w-full text-sm">
                  <TableHeader className="bg-secondary/20">
                    <TableRow className="border-b border-border/60 hover:bg-transparent">
                      <TableHead className="py-3.5 pl-4 font-semibold text-2xs uppercase tracking-wider text-muted-foreground">请求时间</TableHead>
                      <TableHead className="py-3.5 font-semibold text-2xs uppercase tracking-wider text-muted-foreground">调用凭证</TableHead>
                      <TableHead className="py-3.5 font-semibold text-2xs uppercase tracking-wider text-muted-foreground">实际模型 / 路由组</TableHead>
                      <TableHead className="py-3.5 text-center font-semibold text-2xs uppercase tracking-wider text-muted-foreground">流式</TableHead>
                      <TableHead className="py-3.5 text-center font-semibold text-2xs uppercase tracking-wider text-muted-foreground">状态码</TableHead>
                      <TableHead className="py-3.5 num font-semibold text-2xs uppercase tracking-wider text-muted-foreground">首字耗时</TableHead>
                      <TableHead className="py-3.5 num font-semibold text-2xs uppercase tracking-wider text-muted-foreground">总耗时</TableHead>
                      <TableHead className="py-3.5 pr-4 num font-semibold text-2xs uppercase tracking-wider text-muted-foreground">Tokens 输入 / 输出</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody className="divide-y divide-border/30">
                  {items.map((log) => (
                    <TableRow
                      key={log.requestId}
                      role="button"
                      tabIndex={0}
                      aria-label={`查看请求 ${log.requestId} 详情`}
                      className="cursor-pointer transition-colors hover:bg-secondary/30 focus-visible:bg-secondary/30 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-rose/50"
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
            <div className="flex flex-wrap items-center justify-between gap-x-4 gap-y-2 pt-3 text-xs text-muted-foreground border-t border-border/40">
              <span className="tnum font-mono">
                共 <b className="font-semibold text-foreground">{formatNumber(total)}</b> 条记录 · 第 {page + 1}/{totalPages} 页
              </span>
              <div className="flex items-center gap-2">
                <Button
                  variant="outline"
                  size="sm"
                  disabled={page === 0}
                  onClick={() => setPage((p) => Math.max(0, p - 1))}
                >
                  <ChevronLeft className="h-3.5 w-3.5" /> 上一页
                </Button>
                <Button
                  variant="outline"
                  size="sm"
                  disabled={page >= totalPages - 1}
                  onClick={() => setPage((p) => p + 1)}
                >
                  下一页 <ChevronRight className="h-3.5 w-3.5" />
                </Button>
              </div>
            </div>
          </div>
        )}
      </AsyncState>

      <LogDetailSheet id={detailId} onClose={() => setDetailId(null)} />
      {dialog}
    </div>
    </>
  )
}

/* ---------------- 详情抽屉 ---------------- */

function LogDetailSheet({ id, onClose }: { id: string | null; onClose: () => void }) {
  const toast = useToast()
  const [detail, setDetail] = useState<UsageLogDetail | null>(null)
  const [loading, setLoading] = useState(false)
  const [err, setErr] = useState<string | null>(null)

  useEffect(() => {
    if (!id) {
      setDetail(null)
      setErr(null)
      return
    }
    let active = true
    setLoading(true)
    setErr(null)
    api
      .usageLogDetail(id)
      .then((data) => {
        if (active) setDetail(data)
      })
      .catch((e) => {
        if (active) setErr((e as Error).message)
      })
      .finally(() => {
        if (active) setLoading(false)
      })
    return () => {
      active = false
    }
  }, [id])

  function handleExport() {
    if (!detail) return
    const filename = `usage-log-${detail.requestId}.json`
    downloadJSON(filename, buildExportPayload(detail))
    toast.success('已导出完整日志', filename)
  }

  const usage = detail?.usage
  // tokbar 三段：缓存命中 rose-soft / 未命中输入 rose / 输出 jade
  const cacheHit = usage?.cacheHitTokens ?? 0
  const inputTotal = usage?.inputTokens ?? 0
  const inputMiss = Math.max(inputTotal - cacheHit, 0)
  const output = usage?.outputTokens ?? 0
  const tokTotal = cacheHit + inputMiss + output
  const outputRate =
    detail && output > 0 && detail.durationMs > 0 ? (output / (detail.durationMs / 1000)).toFixed(1) : null

  return (
    <Sheet open={!!id} onOpenChange={(open) => !open && onClose()}>
      <SheetContent className="w-[min(640px,94vw)]">
        <SheetHeader>
          <div className="min-w-0 pr-8">
            <SheetTitle>调用详情</SheetTitle>
            <p className="mt-0.5 break-all font-mono text-xs text-muted-foreground">
              {detail?.requestId ?? id}
              {detail && (
                <span className="ml-2">
                  · {formatDateTime(detail.startedAt)}
                </span>
              )}
            </p>
          </div>
        </SheetHeader>
        <SheetBody>
          {loading && <div className="skeleton h-64 rounded-md" />}
          {err && <p className="text-sm text-ember">{err}</p>}
          {!loading && !err && detail && (
            <>
              {detail.error && (
                <div className="mb-5 flex items-start gap-2 rounded-[7px] border border-[color-mix(in_srgb,var(--ember)_35%,transparent)] bg-[color-mix(in_srgb,var(--ember)_7%,transparent)] p-3 text-sm text-ember">
                  <AlertTriangle className="mt-0.5 h-[15px] w-[15px] shrink-0" />
                  <span className="min-w-0 break-all">
                    HTTP {detail.statusCode} · {detail.error}
                  </span>
                </div>
              )}

              <section className="mb-5">
                <SheetSectionTitle>协议链路</SheetSectionTitle>
                <div className="flex flex-wrap items-center gap-[7px]">
                  <span className="max-w-full break-all rounded-[5px] border border-input bg-card px-2 py-[3px] font-mono text-2xs text-muted-foreground">
                    {protocolLabel(detail.sourceFormat || detail.inputFormat || '', 'long') || '—'}
                  </span>
                  <MoveRight className="h-3 w-3 text-muted-foreground" aria-hidden />
                  <span
                    className={cn(
                      'max-w-full break-all rounded-[5px] border px-2 py-[3px] font-mono text-2xs',
                      detail.sourceFormat && detail.targetFormat && detail.sourceFormat !== detail.targetFormat
                        ? 'border-rose bg-wash text-rose'
                        : 'border-input bg-card text-muted-foreground',
                    )}
                  >
                    {protocolLabel(detail.targetFormat || detail.platform, 'long') || '—'}
                  </span>
                  <span className="ml-2 inline-flex items-center gap-1 font-mono text-2xs text-muted-foreground">
                    <CopyButton value={detail.requestId} />
                  </span>
                </div>
                <dl className="mt-3 grid grid-cols-[max-content_1fr] gap-x-4 gap-y-[7px] text-xs">
                  <dt className="whitespace-nowrap text-muted-foreground">调用方</dt>
                  <dd className="tnum min-w-0 break-all">{detail.keyName || '—'}</dd>
                  <dt className="whitespace-nowrap text-muted-foreground">模型组 → 命中</dt>
                  <dd className="tnum min-w-0 break-all">
                    {detail.groupName || '—'} → <span className="font-mono">{detail.modelName || '—'}</span>
                  </dd>
                  <dt className="whitespace-nowrap text-muted-foreground">平台协议</dt>
                  <dd className="min-w-0 break-all font-mono text-xs">
                    {protocolLabel(detail.platform || '', 'long') || '—'}
                  </dd>
                  <dt className="whitespace-nowrap text-muted-foreground">传输方式</dt>
                  <dd className="tnum">{detail.stream ? '流式' : '缓冲'}</dd>
                  <dt className="whitespace-nowrap text-muted-foreground">用量来源</dt>
                  <dd className="min-w-0 break-all font-mono text-xs">{detail.usageSource || '—'}</dd>
                  <dt className="whitespace-nowrap text-muted-foreground">重试次数</dt>
                  <dd className="tnum">{detail.retryCount > 0 ? `${detail.retryCount} 次` : '无'}</dd>
                </dl>
              </section>

              <section className="mb-5">
                <SheetSectionTitle>耗时与用量</SheetSectionTitle>
                <p className="tnum mb-2 flex flex-wrap gap-x-4 text-xs text-muted-foreground">
                  <span>
                    首字 <b className="font-semibold text-foreground">{formatDuration(detail.firstByteMs)}</b>
                  </span>
                  <span>
                    总耗时 <b className="font-semibold text-foreground">{formatDuration(detail.durationMs)}</b>
                  </span>
                  {outputRate && (
                    <span>
                      输出速率 <b className="font-semibold text-foreground">{outputRate} tok/s</b>
                    </span>
                  )}
                </p>
                {tokTotal > 0 ? (
                  <>
                    <div className="my-2 flex h-2 overflow-hidden rounded-[5px] bg-border">
                      {cacheHit > 0 && <i className="h-full" style={{ width: `${(cacheHit / tokTotal) * 100}%`, background: 'var(--rose-soft)' }} />}
                      {inputMiss > 0 && <i className="h-full" style={{ width: `${(inputMiss / tokTotal) * 100}%`, background: 'var(--rose)' }} />}
                      {output > 0 && <i className="h-full" style={{ width: `${(output / tokTotal) * 100}%`, background: 'var(--jade)' }} />}
                    </div>
                    <p className="tnum flex flex-wrap gap-x-4 gap-y-1 text-xs text-muted-foreground">
                      <span>
                        <i className="mr-[5px] inline-block h-2 w-2 rounded-[2px] align-[-1px]" style={{ background: 'var(--rose-soft)' }} />
                        缓存命中 {formatNumber(cacheHit)}
                        {inputTotal > 0 && (
                          <span className="text-muted-foreground">（输入的 {Math.round((cacheHit / inputTotal) * 100)}%）</span>
                        )}
                      </span>
                      <span>
                        <i className="mr-[5px] inline-block h-2 w-2 rounded-[2px] align-[-1px]" style={{ background: 'var(--rose)' }} />
                        未命中输入 {formatNumber(inputMiss)}
                      </span>
                      <span>
                        <i className="mr-[5px] inline-block h-2 w-2 rounded-[2px] align-[-1px]" style={{ background: 'var(--jade)' }} />
                        输出 {formatNumber(output)}
                      </span>
                      <span>
                        合计 <b className="font-semibold text-foreground">{formatNumber(tokTotal)}</b>
                      </span>
                      {usage?.estimated && <span className="text-amber">（估算）</span>}
                    </p>
                  </>
                ) : (
                  <p className="text-xs text-muted-foreground">
                    无 token 用量（embedding / 请求未完成或上游未返回 usage）。
                  </p>
                )}
              </section>

              {detail.retryCount > 0 && detail.retryEvents && detail.retryEvents.length > 0 && (
                <section className="mb-5">
                  <SheetSectionTitle>重试事件</SheetSectionTitle>
                  <div className="flex flex-col gap-[7px] text-xs">
                    {detail.retryEvents.map((ev, i) => (
                      <div key={i} className="flex items-baseline gap-2.5">
                        <span className="tnum flex h-[18px] w-[18px] shrink-0 translate-y-[3px] items-center justify-center rounded-full border border-ember font-mono text-2xs text-ember">
                          {ev.attempt}
                        </span>
                        <span className="min-w-0 break-all">
                          <span className="font-mono text-xs">{ev.model}</span>
                          {ev.error && <span className="text-ember"> — {ev.error}</span>}
                        </span>
                      </div>
                    ))}
                  </div>
                </section>
              )}

              <section className="mb-5">
                <SheetSectionTitle>链路原文</SheetSectionTitle>
                <ChainBodies key={detail.requestId} detail={detail} />
                <p className="mt-2 flex items-center gap-1.5 text-2xs text-muted-foreground">
                  <AlertTriangle className="h-3 w-3" aria-hidden />
                  请求 / 响应体可能被截断，并非完整内容
                </p>
              </section>

              <div className="flex justify-end gap-2.5 border-t border-border pt-4">
                <Button variant="outline" onClick={onClose}>
                  关闭
                </Button>
                <Button onClick={handleExport}>
                  <Download /> 导出完整日志
                </Button>
              </div>
            </>
          )}
        </SheetBody>
      </SheetContent>
    </Sheet>
  )
}

/** 四段链路原文：受控折叠动画 + JSON/SSE 着色，头部字节数 + 截断标记。 */
function ChainBodies({ detail }: { detail: UsageLogDetail }) {
  const [openSegments, setOpenSegments] = useState<Set<string>>(() => new Set(['incoming']))
  const segments: { key: string; title: string; body: UsageBody | undefined }[] = [
    { key: 'incoming', title: '① 下游请求', body: detail.incomingBody },
    { key: 'outgoing', title: '② 后端转发', body: detail.outgoingBody },
    { key: 'provider', title: '③ 上游回传', body: detail.providerResponse },
    { key: 'downstream', title: '④ 返回下游', body: detail.downstreamResponse },
  ]
  return (
    <>
      {segments.map((seg) => {
        const content = seg.body?.content ?? ''
        const pretty = prettyPrintBody(content)
        const open = content.length > 0 && openSegments.has(seg.key)
        const panelId = `chain-body-${seg.key}`
        const triggerId = `chain-trigger-${seg.key}`
        return (
          <div key={seg.key} className="border-t border-border first:border-t-0">
            <button
              type="button"
              id={triggerId}
              disabled={!content}
              aria-expanded={open}
              aria-controls={panelId}
              className="flex w-full items-center gap-2 px-0.5 py-2.5 text-left text-sm font-medium transition-colors hover:text-rose disabled:cursor-default disabled:hover:text-inherit"
              onClick={() => {
                setOpenSegments((current) => {
                  const next = new Set(current)
                  if (next.has(seg.key)) next.delete(seg.key)
                  else next.add(seg.key)
                  return next
                })
              }}
            >
              <ChevronRight
                className={cn(
                  'h-3 w-3 shrink-0 transition-transform duration-300 ease-smooth motion-reduce:transition-none',
                  open && 'rotate-90',
                  !content && 'opacity-35',
                )}
                aria-hidden
              />
              {seg.title}
              <span className="ml-auto inline-flex items-center gap-1.5 font-normal text-muted-foreground">
                <span className="tnum font-mono text-2xs">
                  {content ? `${(new Blob([content]).size / 1024).toFixed(1)} KB` : '空'}
                </span>
                {seg.body?.truncated && (
                  <span className="rounded border border-[color-mix(in_srgb,var(--amber)_35%,transparent)] px-[5px] font-mono text-2xs text-amber">
                    已截断
                  </span>
                )}
              </span>
            </button>
            <div
              id={panelId}
              role="region"
              aria-labelledby={triggerId}
              aria-hidden={!open}
              className={cn(
                'grid transition-[grid-template-rows,opacity] duration-300 ease-smooth motion-reduce:transition-none',
                open ? 'grid-rows-[1fr] opacity-100' : 'grid-rows-[0fr] opacity-0',
              )}
            >
              <div className="min-h-0 overflow-hidden">
                {content && (
                  <pre
                    className="mb-3.5 max-h-[clamp(300px,42vh,560px)] overflow-auto whitespace-pre rounded-[7px] border border-border bg-code px-3.5 py-3 font-mono text-xs leading-[1.7]"
                    dangerouslySetInnerHTML={{ __html: colorize(pretty) }}
                  />
                )}
              </div>
            </div>
          </div>
        )
      })}
    </>
  )
}

/**
 * 把链路内容格式化为带换行的可读文本：
 * - 整体是 JSON → 缩进美化；
 * - SSE 流（多行 data: 事件）→ 逐事件美化其 JSON，事件间空行分隔；
 * - 其它 → 原文返回。
 */
function prettyPrintBody(content: string): string {
  if (!content) return ''
  const parsed = tryParseJSON(content)
  if (typeof parsed !== 'string') {
    return JSON.stringify(parsed, null, 2)
  }
  if (content.includes('data:')) {
    const blocks: string[] = []
    for (const rawLine of content.split('\n')) {
      const line = rawLine.trim()
      if (!line.startsWith('data:')) continue
      const payload = line.slice('data:'.length).trim()
      if (!payload || payload === '[DONE]') {
        blocks.push(payload || '')
        continue
      }
      const ev = tryParseJSON(payload)
      blocks.push(typeof ev === 'string' ? ev : JSON.stringify(ev, null, 2))
    }
    if (blocks.length > 0) return blocks.join('\n\n')
  }
  return content
}

/** 把详情重组为带标签的导出结构：总览 + 四段链路（请求体尽量解析为对象）+ 原始记录。 */
function buildExportPayload(detail: UsageLogDetail) {
  const seg = (b: UsageBody | undefined) => ({
    content: tryParseJSON(b?.content ?? ''),
    truncated: b?.truncated ?? false,
  })
  return {
    overview: {
      requestId: detail.requestId,
      api: detail.platform,
      groupName: detail.groupName,
      modelName: detail.modelName,
      conversion: {
        from: detail.sourceFormat || detail.inputFormat || '',
        to: detail.targetFormat || detail.platform || '',
        chain: detail.conversionChain ?? [],
      },
      stream: detail.stream,
      statusCode: detail.statusCode,
      error: detail.error ?? '',
      retryCount: detail.retryCount,
      retryEvents: detail.retryEvents ?? [],
      firstByteMs: detail.firstByteMs,
      durationMs: detail.durationMs,
      usage: detail.usage,
      usageDetail: detail.usageDetail,
      startedAt: detail.startedAt,
      endedAt: detail.endedAt,
    },
    chain: {
      downstreamRequest: seg(detail.incomingBody),
      backendForward: seg(detail.outgoingBody),
      upstreamResponse: seg(detail.providerResponse),
      downstreamResponse: seg(detail.downstreamResponse),
    },
    raw: detail,
  }
}
