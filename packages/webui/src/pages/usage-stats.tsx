import { useMemo, useRef, useState } from 'react'
import {
  Bar,
  BarChart,
  CartesianGrid,
  Cell,
  Pie,
  PieChart,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from 'recharts'
import { BarChart3, RefreshCw } from 'lucide-react'
import { PageHeader } from '@/components/page-header'
import { KpiCard, KpiGrid } from '@/components/kpi-card'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { ErrorState } from '@/components/ui/states'
import { MultiSelect } from '@/components/ui/multi-select'
import { RangeSelect, type RangeKey } from '@/components/range-select'
import { useUsageStats, useUsageByModel, useUsageFilterOptions, useMinuteTick } from '@/lib/hooks'
import {
  CHART_TICK,
  compactNumber,
  formatDuration,
  formatHitRate,
  formatNumber,
  percent,
  startOfRange,
} from '@/lib/utils'

export function UsageStatsPage() {
  const [range, setRange] = useState<RangeKey>('7d')
  const [groupNames, setGroupNames] = useState<string[]>([])
  const [modelNames, setModelNames] = useState<string[]>([])
  const [keyNames, setKeyNames] = useState<string[]>([])
  const minuteTick = useMinuteTick()

  const { groupOptions, modelOptions, keyOptions } = useUsageFilterOptions()

  const params = useMemo(() => {
    // 时间参数按 5 分钟桶量化：缓存键在桶内稳定，切走再切回直接命中缓存秒开，
    // 不再因分钟粒度的 to 抖动导致每次返回都重新等待慢查询。
    const bucketMs = 5 * 60_000
    const to = new Date(Math.floor((minuteTick * 60_000) / bucketMs) * bucketMs).toISOString()
    return {
      from: startOfRange(range, to),
      to,
      groupNames: groupNames.length ? groupNames : undefined,
      modelNames: modelNames.length ? modelNames : undefined,
      keyNames: keyNames.length ? keyNames : undefined,
    }
  }, [range, groupNames, modelNames, keyNames, minuteTick])

  const { data: stats, isLoading, error, mutate, isValidating } = useUsageStats(params)

  // 按模型聚合（后端 SQL 分组，与 stats 同窗口同筛选）
  const {
    data: byModelRows,
    isLoading: byModelLoading,
    error: byModelError,
    mutate: retryByModel,
    isValidating: byModelValidating,
  } = useUsageByModel(params)
  const byModel = byModelRows ?? []
  const updating = (isLoading && !!stats) || isValidating || byModelValidating

  const pieData = useMemo(
    () => [
      { name: '成功', value: stats?.success ?? 0, color: 'var(--jade)' },
      { name: '失败', value: stats?.failed ?? 0, color: 'var(--ember)' },
    ],
    [stats],
  )

  const tokenData = useMemo(
    () => [
      { name: '输入', value: stats?.inputTokens ?? 0, color: 'var(--rose)' },
      { name: '输出', value: stats?.outputTokens ?? 0, color: 'var(--jade)' },
    ],
    [stats],
  )

  const tokenHoverData = useMemo(() => {
    const input = stats?.inputTokens ?? 0
    const hit = stats?.cacheHitTokens ?? 0
    const miss = Math.max(0, input - hit)
    return [
      { name: '缓存未命中', value: miss, color: 'var(--rose)' },
      { name: '缓存命中', value: hit, color: 'var(--rose-soft)' },
      { name: '输出', value: stats?.outputTokens ?? 0, color: 'var(--jade)' },
    ]
  }, [stats])

  return (
    <div className="space-y-6">
      <PageHeader
        title="Usage 统计"
        description="多维度用量聚合分析，支持按时间窗口、模型组、模型与调用凭证筛选汇总。"
      />

      {/* 过滤条卡片：中屏整齐换行（筛选组整块，指示器随行） */}
      <div className="flex flex-wrap items-center justify-between gap-x-6 gap-y-3 rounded-xl border border-border/70 bg-card p-3.5 shadow-soft">
        <div className="flex flex-wrap items-center gap-2.5">
          <div className="w-[130px]">
            <RangeSelect value={range} onChange={setRange} />
          </div>
          <div className="min-w-[120px] flex-1 sm:flex-none">
            <MultiSelect options={groupOptions} value={groupNames} onChange={setGroupNames} placeholder="全部模型组" searchPlaceholder="搜索模型组" />
          </div>
          <div className="min-w-[120px] flex-1 sm:flex-none">
            <MultiSelect options={modelOptions} value={modelNames} onChange={setModelNames} placeholder="全部模型" searchPlaceholder="搜索模型" />
          </div>
          <div className="min-w-[110px] flex-1 sm:flex-none">
            <MultiSelect options={keyOptions} value={keyNames} onChange={setKeyNames} placeholder="全部调用方" searchPlaceholder="搜索调用方" />
          </div>
        </div>
        {updating && (
          <span className="flex items-center gap-1.5 text-xs text-muted-foreground">
            <RefreshCw className="h-3.5 w-3.5 animate-spin text-primary" /> 聚合计算中…
          </span>
        )}
      </div>

      {error ? (
        <ErrorState message={(error as Error).message} onRetry={() => mutate()} />
      ) : (
        <>
          <section aria-label="核心指标">
            <KpiGrid cols={6}>
              <KpiCard
                variant="hero"
                label="总请求数"
                value={stats ? formatNumber(stats.requests) : '—'}
              />
              <KpiCard
                label="成功请求"
                value={stats ? formatNumber(stats.success) : '—'}
                deltaTone="up"
                delta={stats ? `占比 ${percent(stats.success, stats.requests)}` : undefined}
              />
              <KpiCard
                label="失败请求"
                value={stats ? formatNumber(stats.failed) : '—'}
                deltaTone={stats?.failed ? 'down' : 'neutral'}
                delta={stats ? (stats.failed ? `占比 ${percent(stats.failed, stats.requests)}` : '0 次失败') : undefined}
              />
              <KpiCard
                label="Token 消耗总量"
                value={stats ? compactNumber(stats.totalTokens) : '—'}
              />
              <KpiCard
                label="Prompt 缓存命中"
                value={stats ? compactNumber(stats.cacheHitTokens) : '—'}
                delta={stats ? `命中率 ${formatHitRate(stats.cacheHitRate)}` : undefined}
              />
              <KpiCard
                label="平均请求耗时"
                value={stats ? formatDuration(stats.avgDurationMs) : '—'}
                delta={stats ? `首字耗时 ${formatDuration(stats.avgFirstByteMs)}` : undefined}
              />
            </KpiGrid>
          </section>

          {/* 分布环图卡片：与全站 Card/CardHeader 统一（含卡头分隔线规格） */}
          <div className="grid grid-cols-1 gap-6 lg:grid-cols-2">
            <Card>
              <CardHeader className="border-b border-border/70 pb-3 pt-4 sm:pb-3 sm:pt-4">
                <CardTitle className="text-sm">请求成功 / 失败分布</CardTitle>
              </CardHeader>
              <CardContent className="pt-4">
                {isLoading && !stats ? (
                  <div className="skeleton h-64 rounded-md" />
                ) : (
                  <DonutChart data={pieData} total={stats?.requests ?? 0} centerLabel="请求" />
                )}
              </CardContent>
            </Card>

            <Card>
              <CardHeader className="border-b border-border/70 pb-3 pt-4 sm:pb-3 sm:pt-4">
                <CardTitle className="text-sm">累计 Token 结构分布</CardTitle>
              </CardHeader>
              <CardContent className="pt-4">
                {isLoading && !stats ? (
                  <div className="skeleton h-64 rounded-md" />
                ) : (
                  <DonutChart data={tokenData} hoverData={tokenHoverData} total={stats?.totalTokens ?? 0} centerLabel="Token" />
                )}
              </CardContent>
            </Card>
          </div>

          {/* 按模型聚合卡片 */}
          <Card>
            {byModelError ? (
              <ErrorState className="py-8" message={(byModelError as Error).message} onRetry={() => retryByModel()} />
            ) : byModelLoading && !byModelRows ? (
              <div className="p-5">
                <div className="skeleton h-[240px] rounded-md" />
              </div>
            ) : (
              <>
                <CardHeader className="flex-row items-center justify-between border-b border-border/70 pb-3 pt-4 sm:pb-3 sm:pt-4">
                  <CardTitle className="text-sm">Top 8 模型调用热度</CardTitle>
                  {byModel.length > 0 && (
                    <span className="text-xs text-muted-foreground">共聚合 {byModel.length} 个模型</span>
                  )}
                </CardHeader>
                <CardContent className="pt-4">
                {byModel.length === 0 ? (
                  <p className="py-8 text-center text-sm text-muted-foreground">当前筛选范围内暂无模型调用记录</p>
                ) : (
                  <>
                    <div className="h-[240px]">
                      <ResponsiveContainer width="100%" height="100%">
                        <BarChart data={byModel.slice(0, 8)} margin={{ top: 8, right: 8, bottom: 0, left: 4 }}>
                          <CartesianGrid stroke="hsl(var(--border) / 0.6)" strokeDasharray="3 3" vertical={false} />
                          <XAxis
                            dataKey="model"
                            tick={CHART_TICK}
                            tickLine={false}
                            axisLine={{ stroke: 'hsl(var(--border))' }}
                            interval={0}
                            tickFormatter={(v: string) => {
                              const label = v || '—'
                              return label.length > 14 ? label.slice(0, 13) + '…' : label
                            }}
                          />
                          <YAxis
                            tick={CHART_TICK}
                            tickLine={false}
                            axisLine={false}
                            width={40}
                            allowDecimals={false}
                          />
                          <Tooltip cursor={{ fill: 'var(--wash)' }} content={<ModelBarTooltip />} />
                          <Bar dataKey="requests" name="请求数" radius={[4, 4, 0, 0]} maxBarSize={36}>
                            {byModel.slice(0, 8).map((entry) => (
                              <Cell key={entry.model || '__unknown__'} fill="var(--rose)" fillOpacity={0.65} />
                            ))}
                          </Bar>
                        </BarChart>
                      </ResponsiveContainer>
                    </div>

                    <div className="mt-6 overflow-hidden rounded-lg border border-border/70">
                      <div className="overflow-x-auto">
                        <table className="w-full text-sm">
                          <TableHeader className="bg-secondary/40">
                            <TableRow className="border-b border-border/70 hover:bg-transparent">
                              <TableHead className="py-3 pl-4 font-semibold text-xs uppercase tracking-wider text-muted-foreground">模型标识</TableHead>
                              <TableHead className="py-3 num font-semibold text-xs uppercase tracking-wider text-muted-foreground">请求次数</TableHead>
                              <TableHead className="py-3 num font-semibold text-xs uppercase tracking-wider text-muted-foreground">失败次数</TableHead>
                              <TableHead className="py-3 num font-semibold text-xs uppercase tracking-wider text-muted-foreground">总 Tokens</TableHead>
                              <TableHead className="py-3 pr-4 num font-semibold text-xs uppercase tracking-wider text-muted-foreground">用量占比</TableHead>
                            </TableRow>
                          </TableHeader>
                          <TableBody className="divide-y divide-border/50">
                            {byModel.map((row) => (
                              <TableRow key={row.model || '__unknown__'} className="transition-colors hover:bg-secondary/30">
                                <TableCell className="py-2.5 pl-4 max-w-[280px] truncate font-mono text-xs text-foreground font-medium" title={row.model || '未知模型'}>
                                  {row.model || '—'}
                                </TableCell>
                                <TableCell className="py-2.5 num font-medium text-foreground">{formatNumber(row.requests)}</TableCell>
                                <TableCell className="py-2.5 num">
                                  <span className={row.failed > 0 ? 'text-ember font-semibold' : 'text-muted-foreground'}>{row.failed}</span>
                                </TableCell>
                                <TableCell className="py-2.5 num font-mono text-xs">{formatNumber(row.tokens)}</TableCell>
                                <TableCell className="py-2.5 pr-4 num font-mono text-xs text-muted-foreground">{percent(row.requests, stats?.requests ?? 0)}</TableCell>
                              </TableRow>
                            ))}
                          </TableBody>
                        </table>
                      </div>
                    </div>
                  </>
                )}
                </CardContent>
              </>
            )}
          </Card>
        </>
      )}
    </div>
  )
}

function ModelBarTooltip({ active, payload, label }: {
  active?: boolean
  payload?: { name?: string; value?: number | string }[]
  label?: string
}) {
  if (!active || !payload?.length) return null
  return (
    <div className="rounded-[7px] border border-input bg-card px-[11px] py-2 text-xs shadow-soft tnum">
      <p className="font-mono text-2xs text-muted-foreground">{label}</p>
      <p>
        {payload[0]?.name} <b className="font-semibold">{formatNumber(Number(payload[0]?.value))}</b>
      </p>
    </div>
  )
}

function DonutChart({
  data,
  hoverData,
  total,
  centerLabel,
}: {
  data: { name: string; value: number; color: string }[]
  hoverData?: { name: string; value: number; color: string }[]
  total: number
  centerLabel: string
}) {
  // 悬停状态与悬停的扇区下标；null 时圆环中央显示总计。鼠标移开圆环即恢复总计。
  const [isHovered, setIsHovered] = useState(false)
  const [activeIndex, setActiveIndex] = useState<number | null>(null)
  const chartRef = useRef<HTMLDivElement>(null)

  const activeData = isHovered && hoverData ? hoverData : data
  const hasData = activeData.some((d) => d.value > 0)
  // 单项数据（只有一个非零扇区）时 paddingAngle=0，让圆环完全闭合；
  // 多项时保留 2° 间隔便于区分扇区。
  const nonZeroCount = activeData.filter((d) => d.value > 0).length
  const paddingAngle = nonZeroCount <= 1 ? 0 : 2
  const active = activeIndex != null ? activeData[activeIndex] : null

  function updateHoverFromMouse(clientX: number, clientY: number) {
    const container = chartRef.current
    if (!container) return
    const rect = container.getBoundingClientRect()
    const cx = rect.left + rect.width / 2
    const cy = rect.top + rect.height / 2
    const dx = clientX - cx
    const dy = clientY - cy
    const distance = Math.sqrt(dx * dx + dy * dy)
    // outerRadius=92，允许少量容差让触发更自然。
    setIsHovered(distance <= 98)
  }

  return (
    <div
      ref={chartRef}
      className="relative h-64"
      onMouseMove={(e) => updateHoverFromMouse(e.clientX, e.clientY)}
      onMouseLeave={() => {
        setIsHovered(false)
        setActiveIndex(null)
      }}
    >
      {hasData ? (
        <ResponsiveContainer width="100%" height="100%">
          <PieChart>
            <Pie
              data={activeData}
              dataKey="value"
              nameKey="name"
              innerRadius={64}
              outerRadius={92}
              paddingAngle={paddingAngle}
              stroke="none"
              onMouseEnter={(_, index) => setActiveIndex(index)}
              onMouseLeave={() => setActiveIndex(null)}
              isAnimationActive={false}
            >
              {activeData.map((entry, index) => (
                <Cell
                  key={entry.name}
                  fill={entry.color}
                  // 悬停时降低其余扇区不透明度，突出当前环。
                  opacity={activeIndex == null || activeIndex === index ? 1 : 0.35}
                  style={{ transition: 'opacity 150ms ease' }}
                />
              ))}
            </Pie>
          </PieChart>
        </ResponsiveContainer>
      ) : (
        <div className="flex h-full items-center justify-center text-sm text-muted-foreground">
          <BarChart3 className="mr-2 h-4 w-4" /> 暂无数据
        </div>
      )}
      {/* 圆环中央：悬停某环时显示该环信息，否则显示总计。 */}
      {hasData && (
        <div className="pointer-events-none absolute inset-0 flex flex-col items-center justify-center">
          {active ? (
            <>
              <span className="tnum max-w-[8rem] truncate font-display text-xl font-semibold" style={{ color: active.color }}>
                {formatNumber(active.value)}
              </span>
              <span className="text-xs text-muted-foreground">
                {active.name} · {percent(active.value, total)}
              </span>
            </>
          ) : (
            <>
              <span className="tnum font-display text-xl font-semibold">{formatNumber(total)}</span>
              <span className="text-xs text-muted-foreground">{centerLabel}</span>
            </>
          )}
        </div>
      )}
      {hasData && (
        <div className="absolute bottom-0 left-0 right-0 flex justify-center gap-4">
          {activeData.map((entry, index) => (
            <span
              key={entry.name}
              className="tnum flex cursor-default items-center gap-1.5 text-xs text-muted-foreground transition-opacity"
              style={{ opacity: activeIndex == null || activeIndex === index ? 1 : 0.45 }}
              onMouseEnter={() => setActiveIndex(index)}
              onMouseLeave={() => setActiveIndex(null)}
            >
              <span className="h-2 w-2 rounded-[2px]" style={{ background: entry.color }} />
              {entry.name} {formatNumber(entry.value)}
            </span>
          ))}
        </div>
      )}
    </div>
  )
}
