import { useEffect, useMemo, useState } from 'react'
import { AlertTriangle, ChevronLeft, ChevronRight, Eye, RotateCcw, ScrollText } from 'lucide-react'
import { PageHeader } from '@/components/page-header'
import { Card } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { AsyncState } from '@/components/ui/states'
import { StatusCodeBadge } from '@/components/badges'
import { RangeSelect, type RangeKey } from '@/components/range-select'
import { useConfirm } from '@/components/ui/confirm-dialog'
import { useToast } from '@/components/ui/use-toast'
import { useUsageLogs, revalidate } from '@/lib/hooks'
import { api } from '@/lib/api'
import { formatDateTime, formatDuration, formatNumber, startOfRange, toRFC3339 } from '@/lib/utils'

const PAGE_SIZE = 20

export function UsageLogsPage() {
  const toast = useToast()
  const { confirm, dialog } = useConfirm()
  const [range, setRange] = useState<RangeKey>('7d')
  const [groupName, setGroupName] = useState('')
  const [statusCode, setStatusCode] = useState('')
  const [page, setPage] = useState(0)
  const [detailId, setDetailId] = useState<string | null>(null)

  const params = useMemo(
    () => ({
      from: startOfRange(range),
      to: toRFC3339(new Date()),
      groupName: groupName.trim() || undefined,
      statusCode: statusCode.trim() ? Number(statusCode) : undefined,
      limit: PAGE_SIZE,
      offset: page * PAGE_SIZE,
    }),
    [range, groupName, statusCode, page],
  )

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
        <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
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
            <Input
              value={groupName}
              placeholder="group name"
              onChange={(e) => {
                setGroupName(e.target.value)
                resetFilters()
              }}
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
  const [detail, setDetail] = useState<unknown>(null)
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
        {!loading && !err && (
          <pre className="max-h-[60vh] overflow-auto rounded-xl border border-border bg-background/60 p-4 font-mono text-xs leading-relaxed">
            {JSON.stringify(detail, null, 2)}
          </pre>
        )}
      </DialogContent>
    </Dialog>
  )
}
