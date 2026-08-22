import { useEffect, useMemo, useState } from 'react'
import { ChevronLeft, ChevronRight, RefreshCw, Terminal } from 'lucide-react'
import { PageHeader } from '@/components/page-header'
import { Card } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Label } from '@/components/ui/label'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { AsyncState } from '@/components/ui/states'
import { useSystemLogs } from '@/lib/hooks'
import { formatDateTime, formatNumber } from '@/lib/utils'
import type { BadgeProps } from '@/components/ui/badge'

const PAGE_SIZE = 50

const LEVEL_VARIANT: Record<string, BadgeProps['variant']> = {
  debug: 'muted',
  info: 'default',
  warn: 'secondary',
  error: 'destructive',
}

export function SystemLogsPage() {
  const [level, setLevel] = useState('all')
  const [page, setPage] = useState(0)

  const params = useMemo(
    () => ({
      limit: PAGE_SIZE,
      offset: page * PAGE_SIZE,
      level: level === 'all' ? undefined : level,
    }),
    [level, page],
  )

  const { data, isLoading, error, mutate } = useSystemLogs(params)
  const total = data?.total ?? 0
  const totalPages = Math.max(1, Math.ceil(total / PAGE_SIZE))

  // total 收缩（日志被裁剪）后把超界的页码收敛回最后一页。
  useEffect(() => {
    setPage((p) => Math.min(p, totalPages - 1))
  }, [totalPages])

  return (
    <div className="space-y-6">
      <PageHeader
        title="系统日志"
        description="模型刷新、错误等后端事件"
        actions={
          <Button variant="outline" onClick={() => mutate()}>
            <RefreshCw className="h-4 w-4" /> 刷新
          </Button>
        }
      />

      <Card className="p-4">
        <div className="flex items-center gap-3">
          <Label className="text-xs">级别</Label>
          <Select
            value={level}
            onValueChange={(v) => {
              setLevel(v)
              setPage(0)
            }}
          >
            <SelectTrigger className="w-[150px]">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="all">全部级别</SelectItem>
              <SelectItem value="debug">Debug</SelectItem>
              <SelectItem value="info">Info</SelectItem>
              <SelectItem value="warn">Warn</SelectItem>
              <SelectItem value="error">Error</SelectItem>
            </SelectContent>
          </Select>
        </div>
      </Card>

      <Card>
        <AsyncState
          isLoading={isLoading}
          error={error}
          data={data?.items}
          onRetry={() => mutate()}
          loadingColumns={3}
          emptyIcon={<Terminal className="h-7 w-7" />}
          emptyTitle="暂无系统日志"
          emptyDescription="后端尚未产生该级别的日志。"
        >
          {(items) => (
            <>
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead className="w-[180px]">时间</TableHead>
                    <TableHead className="w-[90px]">级别</TableHead>
                    <TableHead>消息</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {items.map((log) => (
                    <TableRow key={log.id}>
                      <TableCell className="whitespace-nowrap text-xs text-muted-foreground">
                        {formatDateTime(log.createdAt)}
                      </TableCell>
                      <TableCell>
                        <Badge variant={LEVEL_VARIANT[log.level] ?? 'outline'} className="uppercase">
                          {log.level}
                        </Badge>
                      </TableCell>
                      <TableCell>
                        <p className="text-sm">{log.message}</p>
                        {log.fields && log.fields !== '{}' && log.fields !== 'null' && (
                          <p className="mt-0.5 font-mono text-[11px] text-muted-foreground">{log.fields}</p>
                        )}
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
    </div>
  )
}
