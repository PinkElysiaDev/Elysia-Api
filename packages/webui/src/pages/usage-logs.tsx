import { useEffect, useMemo, useState } from 'react'
import {
  AlertTriangle,
  ChevronDown,
  ChevronLeft,
  ChevronRight,
  Download,
  Eye,
  RotateCcw,
  ScrollText,
} from 'lucide-react'
import { PageHeader } from '@/components/page-header'
import { Card } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { MultiSelect } from '@/components/ui/multi-select'
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
import { PlatformBadge, StatusCodeBadge } from '@/components/badges'
import { CopyButton } from '@/components/copy-button'
import { RangeSelect, type RangeKey } from '@/components/range-select'
import { useConfirm } from '@/components/ui/confirm-dialog'
import { useToast } from '@/components/ui/use-toast'
import { useUsageLogs, useGroups, useModels, useTokens, revalidate } from '@/lib/hooks'
import { api } from '@/lib/api'
import type { UsageBody, UsageLogDetail } from '@/lib/types'
import {
  cn,
  downloadJSON,
  formatDateTime,
  formatDuration,
  formatNumber,
  normalizedNow,
  startOfRange,
  tryParseJSON,
  uniqueSorted,
} from '@/lib/utils'

const PAGE_SIZE = 20

export function UsageLogsPage() {
  const toast = useToast()
  const { confirm, dialog } = useConfirm()
  const [range, setRange] = useState<RangeKey>('7d')
  const [groupNames, setGroupNames] = useState<string[]>([])
  const [modelNames, setModelNames] = useState<string[]>([])
  const [keyNames, setKeyNames] = useState<string[]>([])
  const [statusCode, setStatusCode] = useState('')
  const [page, setPage] = useState(0)
  const [detailId, setDetailId] = useState<string | null>(null)

  const { data: groups } = useGroups()
  const { data: models } = useModels()
  const { data: tokens } = useTokens()

  const groupOptions = useMemo(() => uniqueSorted((groups ?? []).map((g) => g.name)), [groups])
  const modelOptions = useMemo(() => uniqueSorted((models ?? []).map((m) => m.name)), [models])
  const keyOptions = useMemo(() => uniqueSorted((tokens ?? []).map((t) => t.name)), [tokens])

  const params = useMemo(() => {
    const to = normalizedNow()
    return {
      from: startOfRange(range, to),
      to,
      groupNames: groupNames.length ? groupNames : undefined,
      modelNames: modelNames.length ? modelNames : undefined,
      keyNames: keyNames.length ? keyNames : undefined,
      statusCode: statusCode.trim() ? Number(statusCode) : undefined,
      limit: PAGE_SIZE,
      offset: page * PAGE_SIZE,
    }
  }, [range, groupNames, modelNames, keyNames, statusCode, page])

  const { data, isLoading, error, mutate } = useUsageLogs(params)
  const total = data?.total ?? 0
  const totalPages = Math.max(1, Math.ceil(total / PAGE_SIZE))

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

  function resetFilters() {
    setPage(0)
  }

  return (
    <div className="space-y-6">
      <PageHeader
        title="Usage 日志"
        description="逐条请求记录。请求/响应体可能被截断，仅供排查参考"
        actions={
          <Button variant="destructive" onClick={handleReset}>
            <RotateCcw className="h-4 w-4" /> 重置 Usage
          </Button>
        }
      />

      <Card className="p-4">
        <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-5">
          <div className="space-y-1.5">
            <Label className="text-xs">时间范围</Label>
            <RangeSelect
              value={range}
              onChange={(v) => {
                setRange(v)
                resetFilters()
              }}
            />
          </div>
          <div className="space-y-1.5">
            <Label className="text-xs">模型组</Label>
            <MultiSelect
              options={groupOptions}
              value={groupNames}
              onChange={(v) => {
                setGroupNames(v)
                resetFilters()
              }}
              placeholder="全部模型组"
              searchPlaceholder="搜索模型组"
            />
          </div>
          <div className="space-y-1.5">
            <Label className="text-xs">模型</Label>
            <MultiSelect
              options={modelOptions}
              value={modelNames}
              onChange={(v) => {
                setModelNames(v)
                resetFilters()
              }}
              placeholder="全部模型"
              searchPlaceholder="搜索模型"
            />
          </div>
          <div className="space-y-1.5">
            <Label className="text-xs">调用方 API Key</Label>
            <MultiSelect
              options={keyOptions}
              value={keyNames}
              onChange={(v) => {
                setKeyNames(v)
                resetFilters()
              }}
              placeholder="全部调用方"
              searchPlaceholder="搜索调用方"
            />
          </div>
          <div className="space-y-1.5">
            <Label className="text-xs">状态码</Label>
            <Input
              value={statusCode}
              placeholder="200 / 500"
              onChange={(e) => {
                setStatusCode(e.target.value.replace(/[^0-9]/g, ''))
                resetFilters()
              }}
            />
          </div>
        </div>
      </Card>

      <Card>
        <AsyncState
          isLoading={isLoading}
          error={error}
          data={data?.items}
          onRetry={() => mutate()}
          loadingColumns={6}
          emptyIcon={<ScrollText className="h-7 w-7" />}
          emptyTitle="暂无 Usage 日志"
          emptyDescription="该时间范围内还没有请求记录。"
        >
          {(items) => (
            <>
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>时间</TableHead>
                    <TableHead>模型组 / 模型</TableHead>
                    <TableHead>状态</TableHead>
                    <TableHead>Token</TableHead>
                    <TableHead>耗时</TableHead>
                    <TableHead className="text-right">详情</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {items.map((log) => (
                    <TableRow key={log.requestId}>
                      <TableCell className="whitespace-nowrap text-xs text-muted-foreground">
                        {formatDateTime(log.startedAt)}
                      </TableCell>
                      <TableCell>
                        <div className="font-medium">{log.groupName || '—'}</div>
                        <div className="font-mono text-xs text-muted-foreground">{log.modelName || '—'}</div>
                      </TableCell>
                      <TableCell>
                        <div className="flex items-center gap-1.5">
                          <StatusCodeBadge code={log.statusCode} />
                          {log.stream && <Badge variant="outline">流式</Badge>}
                        </div>
                      </TableCell>
                      <TableCell className="text-sm">
                        {formatNumber(log.totalTokens)}
                        <div className="text-xs text-muted-foreground">
                          ↑{formatNumber(log.inputTokens)} ↓{formatNumber(log.outputTokens)}
                        </div>
                      </TableCell>
                      <TableCell className="text-sm">{formatDuration(log.durationMs)}</TableCell>
                      <TableCell className="text-right">
                        <Button variant="ghost" size="iconSm" onClick={() => setDetailId(log.requestId)}>
                          <Eye className="h-4 w-4" />
                        </Button>
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>

              <div className="flex items-center justify-between border-t border-border px-4 py-3 text-sm">
                <span className="text-muted-foreground">
                  共 {formatNumber(total)} 条 · 第 {page + 1}/{totalPages} 页
                </span>
                <div className="flex items-center gap-2">
                  <Button
                    variant="outline"
                    size="sm"
                    disabled={page === 0}
                    onClick={() => setPage((p) => Math.max(0, p - 1))}
                  >
                    <ChevronLeft className="h-4 w-4" /> 上一页
                  </Button>
                  <Button
                    variant="outline"
                    size="sm"
                    disabled={page >= totalPages - 1}
                    onClick={() => setPage((p) => p + 1)}
                  >
                    下一页 <ChevronRight className="h-4 w-4" />
                  </Button>
                </div>
              </div>
            </>
          )}
        </AsyncState>
      </Card>

      <LogDetailDialog id={detailId} onClose={() => setDetailId(null)} />
      {dialog}
    </div>
  )
}

function LogDetailDialog({ id, onClose }: { id: string | null; onClose: () => void }) {
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
    downloadJSON(`usage-log-${detail.requestId}.json`, buildExportPayload(detail))
  }

  return (
    <Dialog open={!!id} onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="max-w-3xl">
        <DialogHeader>
          <DialogTitle>Usage 详情</DialogTitle>
          <DialogDescription className="flex items-center gap-1.5">
            <AlertTriangle className="h-3.5 w-3.5 text-primary" />
            请求 / 响应体可能被截断，并非完整内容
          </DialogDescription>
        </DialogHeader>

        {loading && <div className="skeleton h-64 rounded-xl" />}
        {err && <p className="text-sm text-destructive">{err}</p>}
        {!loading && !err && detail && (
          <div className="max-h-[64vh] space-y-4 overflow-auto pr-1">
            <LogOverview detail={detail} />
            <LogChainSection detail={detail} />
          </div>
        )}

        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            关闭
          </Button>
          <Button onClick={handleExport} disabled={!detail}>
            <Download className="h-4 w-4" /> 导出完整日志
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

/** 总览字段：键值对小项。 */
function Field({ label, children, className }: { label: string; children: React.ReactNode; className?: string }) {
  return (
    <div className={cn('space-y-0.5', className)}>
      <div className="text-xs text-muted-foreground">{label}</div>
      <div className="text-sm">{children}</div>
    </div>
  )
}

function LogOverview({ detail }: { detail: UsageLogDetail }) {
  const u = detail.usage ?? {}
  const sourceFmt = detail.sourceFormat || detail.inputFormat || ''
  const targetFmt = detail.targetFormat || detail.platform || ''
  // 透传模式：后端未做格式转换（relay_mode = "passthrough"）
  const passthrough = detail.relayMode === 'passthrough'
  return (
    <div className="rounded-xl border border-border bg-background/60 p-4">
      <div className="mb-3 flex items-center gap-2">
        <span className="text-sm font-medium">总览</span>
        <StatusCodeBadge code={detail.statusCode} />
        {detail.stream && <Badge variant="outline">流式</Badge>}
        {passthrough && <Badge variant="outline">透传</Badge>}
      </div>

      <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
        <Field label="请求 ID">
          <span className="inline-flex items-center gap-1">
            <span className="font-mono text-xs">{detail.requestId}</span>
            <CopyButton value={detail.requestId} />
          </span>
        </Field>
        <Field label="协议转换" className="lg:col-span-2">
          <span className="inline-flex flex-wrap items-center gap-1.5">
            <PlatformBadge platform={sourceFmt || '—'} />
            <ChevronRight className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
            <PlatformBadge platform={targetFmt || '—'} />
          </span>
        </Field>
        <Field label="模型组">{detail.groupName || '—'}</Field>
        <Field label="模型">
          <span className="font-mono text-xs">{detail.modelName || '—'}</span>
        </Field>
        <Field label="重试次数">
          {detail.retryCount > 0 ? `${detail.retryCount} 次` : '无'}
        </Field>
        <Field label="首字延迟">{formatDuration(detail.firstByteMs)}</Field>
        <Field label="总耗时">{formatDuration(detail.durationMs)}</Field>
        <Field label="开始时间">
          <span className="text-xs">{formatDateTime(detail.startedAt)}</span>
        </Field>
        <Field label="Token 总量">{formatNumber(u.totalTokens ?? 0)}</Field>
        <Field label="输入 / 输出">
          ↑{formatNumber(u.inputTokens ?? 0)} ↓{formatNumber(u.outputTokens ?? 0)}
        </Field>
        <Field label="缓存命中">
          {formatNumber(u.cacheHitTokens ?? 0)}
          {u.estimated && <span className="ml-1 text-xs text-muted-foreground">(估算)</span>}
        </Field>
      </div>

      {detail.error && (
        <div className="mt-3 rounded-lg border border-destructive/40 bg-destructive/10 p-2.5 text-xs text-destructive">
          {detail.error}
        </div>
      )}

      {detail.retryCount > 0 && detail.retryEvents && detail.retryEvents.length > 0 && (
        <div className="mt-3 space-y-1">
          <div className="text-xs text-muted-foreground">重试历史</div>
          {detail.retryEvents.map((ev, i) => (
            <div key={i} className="rounded-md bg-muted/40 px-2 py-1 text-xs">
              <span className="font-medium">#{ev.attempt}</span>{' '}
              <span className="font-mono">{ev.model}</span>
              {ev.error && <span className="text-destructive"> — {ev.error}</span>}
            </div>
          ))}
        </div>
      )}
    </div>
  )
}

/** 四段链路：下游请求 → 后端转发 → 上游回传 → 返回下游，默认折叠。 */
function LogChainSection({ detail }: { detail: UsageLogDetail }) {
  const segments: { key: string; title: string; body: UsageBody }[] = [
    { key: 'incoming', title: '① 下游请求', body: detail.incomingBody },
    { key: 'outgoing', title: '② 后端转发', body: detail.outgoingBody },
    { key: 'provider', title: '③ 上游回传', body: detail.providerResponse },
    { key: 'downstream', title: '④ 返回下游', body: detail.downstreamResponse },
  ]
  return (
    <div className="space-y-2">
      <div className="text-sm font-medium">完整链路</div>
      {segments.map((seg) => (
        <ChainBlock key={seg.key} title={seg.title} body={seg.body} />
      ))}
    </div>
  )
}

function ChainBlock({ title, body }: { title: string; body: UsageBody | undefined }) {
  const [open, setOpen] = useState(false)
  const content = body?.content ?? ''
  const pretty = useMemo(() => prettyPrintBody(content), [content])

  return (
    <div className="rounded-xl border border-border bg-background/60">
      <button
        type="button"
        className="flex w-full items-center justify-between px-4 py-2.5 text-left text-sm"
        onClick={() => setOpen((v) => !v)}
      >
        <span className="flex items-center gap-2">
          {title}
          {body?.truncated && <Badge variant="outline">已截断</Badge>}
          {!content && <span className="text-xs text-muted-foreground">（空）</span>}
        </span>
        <ChevronDown className={cn('h-4 w-4 transition-transform', open && 'rotate-180')} />
      </button>
      {open && content && (
        <pre className="max-h-[40vh] overflow-auto whitespace-pre-wrap break-words border-t border-border px-4 py-3 font-mono text-xs leading-relaxed">
          {pretty}
        </pre>
      )}
    </div>
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
  // 不是整体 JSON：尝试按 SSE 事件逐条美化
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
