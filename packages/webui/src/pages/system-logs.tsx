import { useEffect, useMemo, useRef, useState } from 'react'
import { RefreshCw, Terminal } from 'lucide-react'
import { PageHeader } from '@/components/page-header'
import { TonePill } from '@/components/badges'
import { PaginationBar } from '@/components/pagination'
import { RoleWatermark } from '@/components/role-watermark'
import { Button } from '@/components/ui/button'
import { Seg } from '@/components/ui/seg'
import { TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { Sheet, SheetBody, SheetContent, SheetHeader, SheetSectionTitle, SheetTitle } from '@/components/ui/sheet'
import { AsyncState } from '@/components/ui/states'
import { useSystemLogs } from '@/lib/hooks'
import { colorize } from '@/lib/json-highlight'
import { cn, formatDateTime, tryParseJSON } from '@/lib/utils'

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
    <TonePill color={style.color} className="text-2xs uppercase">
      {level}
    </TonePill>
  )
}

export function SystemLogsPage() {
  const [page, setPage] = useState(0)
  const [level, setLevel] = useState<LevelFilter>('all')
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

  // 翻页后把表格顶部滚回视野：分页按钮在表格底部，换页应从第一行重新读起。
  const tableTopRef = useRef<HTMLDivElement>(null)
  function navigateWithScroll(next: number) {
    setPage(Math.max(0, Math.min(next, totalPages - 1)))
    tableTopRef.current?.scrollIntoView({ behavior: 'smooth', block: 'start' })
  }

  return (
    <>
      <RoleWatermark className="-right-8 top-0 opacity-[0.05] dark:opacity-[0.08]" />

      <div className="relative z-[1] space-y-6">
        <PageHeader
          title="系统日志"          actions={
            <Button onClick={() => mutate()} disabled={isLoading}>
              <RefreshCw className={cn('h-4 w-4', isLoading && 'animate-spin')} /> 刷新日志
            </Button>
          }
        />

        {/* 级别筛选工具条 */}
        <div className="flex flex-wrap items-center justify-between gap-3 py-1">
          <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
            <span className="text-2xs font-semibold uppercase tracking-wider text-muted-foreground">日志级别</span>
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
          </div>
        </div>

        <AsyncState
          isLoading={isLoading}
          error={error}
          data={data?.items}
          onRetry={() => mutate()}
          loadingColumns={3}
          emptyIcon={<Terminal className="h-7 w-7" />}
          emptyTitle="暂无匹配系统日志"
          emptyDescription="当前筛选日志级别下未记录任何运行事件。"
        >
          {(items) => (
            <div className="space-y-3">
              <div className="overflow-x-auto scroll-mt-4" ref={tableTopRef}>
                <table className="w-full text-sm">
                  <TableHeader className="bg-secondary/20">
                    <TableRow className="border-b border-border/60 hover:bg-transparent">
                      <TableHead className="py-3.5 pl-4 w-[190px] font-semibold text-2xs uppercase tracking-wider text-muted-foreground">记录时间</TableHead>
                      <TableHead className="py-3.5 w-[100px] font-semibold text-2xs uppercase tracking-wider text-muted-foreground">级别</TableHead>
                      <TableHead className="py-3.5 pr-4 font-semibold text-2xs uppercase tracking-wider text-muted-foreground">日志消息 / 附加字段 (Fields)</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody className="divide-y divide-border/30">
                    {items.map((log) => {
                      const hasFields = log.fields && log.fields !== '{}' && log.fields !== 'null'
                      return (
                        <TableRow key={log.id} className="border-b-0">
                          <TableCell className="py-3.5 pl-4 whitespace-nowrap font-mono text-xs text-muted-foreground">
                            {formatDateTime(log.createdAt)}
                          </TableCell>
                          <TableCell className="py-3.5">
                            <LevelPill level={log.level} />
                          </TableCell>
                          <TableCell className="py-3.5 pr-4">
                            <p className="text-xs font-medium text-foreground">{log.message}</p>
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
                                className="mt-1 block max-w-[760px] truncate text-left font-mono text-2xs text-muted-foreground underline decoration-dashed underline-offset-2 transition-colors hover:text-primary"
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

              <PaginationBar total={total} page={page} totalPages={totalPages} onNavigate={navigateWithScroll} unitLabel="条" />
            </div>
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
      </div>
    </>
  )
}

function prettyFields(raw: string): string {
  const parsed = tryParseJSON(raw)
  if (typeof parsed !== 'string') return JSON.stringify(parsed, null, 2)
  return raw
}
