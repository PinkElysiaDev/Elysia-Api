import { useEffect, useMemo, useState } from 'react'
import { ChevronLeft, ChevronRight, RefreshCw, Terminal } from 'lucide-react'
import { PageHeader } from '@/components/page-header'
import { Button } from '@/components/ui/button'
import { Seg } from '@/components/ui/seg'
import { TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { Sheet, SheetBody, SheetContent, SheetHeader, SheetSectionTitle, SheetTitle } from '@/components/ui/sheet'
import { AsyncState } from '@/components/ui/states'
import { useSystemLogs } from '@/lib/hooks'
import { colorize } from '@/lib/json-highlight'
import { cn, formatDateTime, formatNumber, tryParseJSON } from '@/lib/utils'

const PAGE_SIZE = 50

type LevelFilter = 'all' | 'debug' | 'info' | 'warn' | 'error'

const LEVEL_STYLE: Record<string, { color: string }> = {
  debug: { color: 'hsl(var(--muted-foreground))' },
  info: { color: 'var(--jade)' },
  warn: { color: 'var(--amber)' },
  error: { color: 'var(--ember)' },
}

function LevelPill({ level }: { level: string }) {
  const style = LEVEL_STYLE[level] ?? LEVEL_STYLE.debug
  return (
    <span
      className={cn(
        'inline-flex items-center rounded-[5px] border px-[7px] py-0.5 font-mono text-2xs font-medium uppercase',
      )}
      style={{
        color: style.color,
        borderColor: `color-mix(in srgb, ${style.color} 28%, transparent)`,
        background: `color-mix(in srgb, ${style.color} 9%, transparent)`,
      }}
    >
      {level}
    </span>
  )
}

export function SystemLogsPage() {
  const [level, setLevel] = useState<LevelFilter>('all')
  const [page, setPage] = useState(0)
  const [detailFields, setDetailFields] = useState<{ message: string; fields: string; createdAt: string } | null>(null)

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
    <>
      <PageHeader
        title="系统日志"
        description="模型刷新、错误等后端事件。点击 fields 查看结构化详情。"
        actions={
          <Button onClick={() => mutate()}>
            <RefreshCw /> 刷新
          </Button>
        }
      />

      {/* 级别筛选 */}
      <div className="flex flex-wrap items-center gap-2.5 border-b border-border py-3">
        <Seg
          aria-label="级别"
          options={[
            { value: 'all', label: '全部' },
            { value: 'debug', label: 'DEBUG' },
            { value: 'info', label: 'INFO' },
            { value: 'warn', label: 'WARN' },
            { value: 'error', label: 'ERROR' },
          ]}
          value={level}
          onChange={(v) => {
            setLevel(v)
            setPage(0)
          }}
        />
        <span className="tnum ml-auto text-xs text-muted-foreground">
          共 <b className="font-semibold text-foreground">{formatNumber(total)}</b> 条
        </span>
      </div>

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
            <div className="overflow-x-auto">
              <table className="w-full text-sm">
                <TableHeader>
                  <TableRow>
                    <TableHead className="w-[180px]">时间</TableHead>
                    <TableHead className="w-[90px]">级别</TableHead>
                    <TableHead>消息 / fields</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {items.map((log) => {
                    const hasFields = log.fields && log.fields !== '{}' && log.fields !== 'null'
                    return (
                      <TableRow key={log.id}>
                        <TableCell className="whitespace-nowrap font-mono text-xs text-muted-foreground">
                          {formatDateTime(log.createdAt)}
                        </TableCell>
                        <TableCell>
                          <LevelPill level={log.level} />
                        </TableCell>
                        <TableCell>
                          <p className="text-sm">{log.message}</p>
                          {hasFields && (
                            <button
                              type="button"
                              onClick={() =>
                                setDetailFields({
                                  message: log.message,
                                  fields: log.fields ?? '',
                                  createdAt: log.createdAt,
                                })
                              }
                              className="mt-0.5 block max-w-[720px] truncate text-left font-mono text-2xs text-muted-foreground underline decoration-dashed underline-offset-2 transition-colors hover:text-rose"
                              title="查看结构化详情"
                            >
                              {log.fields}
                            </button>
                          )}
                        </TableCell>
                      </TableRow>
                    )
                  })}
                </TableBody>
              </table>
            </div>

            <div className="flex items-center justify-between pt-3 text-xs text-muted-foreground">
              <span className="tnum">
                共 {formatNumber(total)} 条 · 第 {page + 1}/{totalPages} 页
              </span>
              <span className="flex items-center gap-2">
                <Button
                  variant="outline"
                  size="sm"
                  disabled={page === 0}
                  onClick={() => setPage((p) => Math.max(0, p - 1))}
                >
                  <ChevronLeft /> 上一页
                </Button>
                <Button
                  variant="outline"
                  size="sm"
                  disabled={page >= totalPages - 1}
                  onClick={() => setPage((p) => p + 1)}
                >
                  下一页 <ChevronRight />
                </Button>
              </span>
            </div>
          </>
        )}
      </AsyncState>

      {/* fields 结构化详情 */}
      <Sheet open={!!detailFields} onOpenChange={(open) => !open && setDetailFields(null)}>
        <SheetContent>
          <SheetHeader>
            <SheetTitle>日志详情</SheetTitle>
          </SheetHeader>
          <SheetBody>
            <SheetSectionTitle>字段</SheetSectionTitle>
            <pre
              className="mb-5 max-h-[50vh] overflow-auto whitespace-pre rounded-[7px] border border-border bg-code px-3.5 py-3 font-mono text-xs leading-[1.7]"
              dangerouslySetInnerHTML={{
                __html: colorize(prettyFields(detailFields?.fields ?? '')),
              }}
            />
            <SheetSectionTitle>上下文</SheetSectionTitle>
            <dl className="grid grid-cols-[max-content_1fr] gap-x-4 gap-y-[7px] text-xs">
              <dt className="text-muted-foreground">时间</dt>
              <dd className="tnum break-all">{detailFields ? formatDateTime(detailFields.createdAt) : '—'}</dd>
              <dt className="text-muted-foreground">消息</dt>
              <dd className="min-w-0 break-all">{detailFields?.message ?? '—'}</dd>
            </dl>
          </SheetBody>
        </SheetContent>
      </Sheet>
    </>
  )
}

function prettyFields(raw: string): string {
  const parsed = tryParseJSON(raw)
  if (typeof parsed !== 'string') return JSON.stringify(parsed, null, 2)
  return raw
}
