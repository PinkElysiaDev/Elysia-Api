import { useMemo, useRef, useState } from 'react'
import {
  Area,
  Bar,
  BarChart,
  CartesianGrid,
  Cell,
  ComposedChart,
  Line,
  Pie,
  PieChart,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from 'recharts'
import {
  Activity,
  BarChart3,
  CheckCircle2,
  Clock,
  Coins,
  Cpu,
  Flame,
  PieChart as PieIcon,
  RefreshCw,
} from 'lucide-react'
import { PageHeader } from '@/components/page-header'
import { KpiCard, KpiGrid } from '@/components/kpi-card'
import { RoleWatermark } from '@/components/role-watermark'
import { TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { ErrorState } from '@/components/ui/states'
import { Fchip } from '@/components/ui/fchip'
import { UsageFilterBar, type RangeKey } from '@/components/usage-filter-bar'
import { ModelBreakdownTooltip } from '@/components/model-breakdown-tooltip'
import {
  useUsageStats,
  useUsageTrend,
  useUsageByModel,
  useUsageFilterOptions,
  useMinuteTick,
  useSources,
  useModels,
} from '@/lib/hooks'
import {
  bucketedTimeISO,
  CHART_TICK,
  compactNumber,
  effectiveModelFilter,
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
  const [sourceNames, setSourceNames] = useState<string[]>([])
  const [keyNames, setKeyNames] = useState<string[]>([])
  const [showReqLine, setShowReqLine] = useState(true)
  const [showTokBar, setShowTokBar] = useState(true)
  const minuteTick = useMinuteTick()

  const { groupOptions, modelOptions, keyOptions } = useUsageFilterOptions()
  const { data: sources } = useSources()
  const { data: allModels } = useModels()

  const sourceOptions = useMemo(
    () => (sources ?? []).filter((s) => s.enabled).map((s) => s.name),
    [sources],
  )

  // 模型源筛选（sourceNames）与模型筛选取交集后随 modelNames 下发。
  const effectiveModelNames = useMemo(
    () => effectiveModelFilter(modelNames, sourceNames, allModels ?? []),
    [modelNames, sourceNames, allModels],
  )

  const params = useMemo(() => {
    // 时间参数按 5 分钟桶量化：缓存键在桶内稳定，切走再切回直接命中缓存秒开
    const to = bucketedTimeISO(minuteTick * 60_000, 5 * 60_000)
    return {
      from: startOfRange(range, to),
      to,
      groupNames: groupNames.length ? groupNames : undefined,
      modelNames: effectiveModelNames.length ? effectiveModelNames : undefined,
      keyNames: keyNames.length ? keyNames : undefined,
    }
  }, [range, groupNames, effectiveModelNames, keyNames, minuteTick])

  const { data: stats, isLoading, error, mutate, isValidating } = useUsageStats(params)

  // 趋势图聚合（支持模型级细分 hover）
  const trendParams = useMemo(() => {
    const now = new Date(minuteTick * 60_000)
    const offsetMinutes = -now.getTimezoneOffset()
    return {
      ...params,
      utcOffsetMinutes: offsetMinutes,
    }
  }, [params, minuteTick])
  const {
    data: trendBuckets,
    isLoading: trendLoading,
    error: trendError,
    mutate: retryTrend,
  } = useUsageTrend(trendParams)

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
    <>
      <RoleWatermark className="-right-8 top-0 opacity-[0.05] dark:opacity-[0.08]" />

      <div className="relative z-[1] space-y-6">
        <PageHeader title="Usage 统计" />

        {/* 共用筛选条（含模型源维度）+ 更新指示 */}
        <UsageFilterBar
          range={range}
          onRangeChange={setRange}
          groupOptions={groupOptions}
          modelOptions={modelOptions}
          keyOptions={keyOptions}
          sourceOptions={sourceOptions}
          groupNames={groupNames}
          onGroupNamesChange={setGroupNames}
          modelNames={modelNames}
          onModelNamesChange={setModelNames}
          sourceNames={sourceNames}
          onSourceNamesChange={setSourceNames}
          keyNames={keyNames}
          onKeyNamesChange={setKeyNames}
          right={
            updating ? (
              <span className="flex items-center gap-1.5 font-mono text-xs text-muted-foreground">
                <RefreshCw className="h-3.5 w-3.5 animate-spin text-primary" /> 聚合计算中…
              </span>
            ) : undefined
          }
        />

        {error ? (
          <ErrorState message={(error as Error).message} onRetry={() => mutate()} />
        ) : (
          <>
            {/* 核心 KPI 锚点 */}
            <section aria-label="核心指标">
              <KpiGrid cols={6}>
                <KpiCard
                  variant="hero"
                  label="总请求数"
                  value={stats ? formatNumber(stats.requests) : '—'}
                  icon={<Flame className="h-4 w-4 text-rose" />}
                />
                <KpiCard
                  label="成功请求"
                  value={stats ? formatNumber(stats.success) : '—'}
                  deltaTone="up"
                  icon={<CheckCircle2 className="h-4 w-4 text-jade" />}
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
                  icon={<Coins className="h-4 w-4 text-amber" />}
                />
                <KpiCard
                  label="Prompt 缓存命中"
                  value={stats ? compactNumber(stats.cacheHitTokens) : '—'}
                  delta={stats ? `命中率 ${formatHitRate(stats.cacheHitRate)}` : undefined}
                />
                <KpiCard
                  label="平均请求耗时"
                  value={stats ? formatDuration(stats.avgDurationMs) : '—'}
                  icon={<Clock className="h-4 w-4 text-muted-foreground" />}
                  delta={stats ? `首字耗时 ${formatDuration(stats.avgFirstByteMs)}` : undefined}
                />
              </KpiGrid>
            </section>

            {/* 双流时序趋势图表 */}
            <section className="space-y-4 pt-2">
              <div className="flex flex-row flex-wrap items-center justify-between gap-4 border-b border-border/50 pb-3.5">
                <div className="flex items-center gap-2">
                  <Activity className="h-4 w-4 text-primary" />
                  <h3 className="text-sm font-semibold text-foreground">时间序列双流趋势</h3>
                </div>
                <div className="flex flex-wrap items-center gap-2.5">
                  <Fchip label="请求折线" color="var(--rose)" active={showReqLine} onClick={() => setShowReqLine(!showReqLine)} />
                  <Fchip label="Token 柱图" color="var(--jade)" active={showTokBar} onClick={() => setShowTokBar(!showTokBar)} />
                </div>
              </div>
              <div className="pt-2">
                <div className="h-[280px] w-full">
                  {trendError ? (
                    <ErrorState className="h-full py-8" message={(trendError as Error).message} onRetry={() => retryTrend()} />
                  ) : trendLoading && !trendBuckets ? (
                    <div className="skeleton h-full w-full rounded-md" />
                  ) : (
                    <ResponsiveContainer width="100%" height="100%">
                      <ComposedChart data={trendBuckets ?? []} margin={{ top: 12, right: 36, bottom: 0, left: 0 }}>
                        <defs>
                          <linearGradient id="usageReqAreaGrad" x1="0" y1="0" x2="0" y2="1">
                            <stop offset="5%" stopColor="var(--rose)" stopOpacity={0.16} />
                            <stop offset="95%" stopColor="var(--rose)" stopOpacity={0.0} />
                          </linearGradient>
                        </defs>
                        <CartesianGrid stroke="hsl(var(--border) / 0.45)" strokeDasharray="3 3" vertical={false} />
                        <XAxis
                          dataKey="date"
                          tick={CHART_TICK}
                          tickLine={false}
                          axisLine={{ stroke: 'hsl(var(--border) / 0.6)' }}
                          interval="preserveStartEnd"
                          minTickGap={24}
                          tickFormatter={(v: string) => v.slice(5).replace('-', '/')}
                        />
                        {/* 双流纵轴按各自流色着色（左 Token=jade / 右 请求=rose），便于区分 */}
                        <YAxis
                          yAxisId="tok"
                          tick={{ ...CHART_TICK, fill: 'var(--jade)' }}
                          tickLine={false}
                          axisLine={false}
                          width={52}
                          tickFormatter={(v: number) => compactNumber(v)}
                        />
                        <YAxis
                          yAxisId="req"
                          orientation="right"
                          tick={{ ...CHART_TICK, fill: 'var(--rose)' }}
                          tickLine={false}
                          axisLine={false}
                          width={40}
                          allowDecimals={false}
                        />
                        <Tooltip
                          content={<ModelBreakdownTooltip />}
                          cursor={{ fill: 'var(--wash)' }}
                        />
                        {showTokBar && (
                          <Bar
                            yAxisId="tok"
                            dataKey="tokens"
                            name="Token 消耗"
                            fill="var(--jade)"
                            fillOpacity={0.45}
                            radius={[4, 4, 0, 0]}
                            maxBarSize={24}
                          />
                        )}
                        {showReqLine && (
                          <>
                            <Area
                              yAxisId="req"
                              dataKey="requests"
                              type="monotone"
                              fill="url(#usageReqAreaGrad)"
                              stroke="none"
                            />
                            <Line
                              yAxisId="req"
                              dataKey="requests"
                              name="请求数"
                              type="monotone"
                              stroke="var(--rose)"
                              strokeWidth={2.2}
                              dot={false}
                              activeDot={{ r: 4.5, fill: 'var(--rose)', stroke: 'hsl(var(--card))', strokeWidth: 2 }}
                            />
                          </>
                        )}
                      </ComposedChart>
                    </ResponsiveContainer>
                  )}
                </div>
              </div>
            </section>

            {/* 分布环图面板 */}
            <div className="grid grid-cols-1 gap-8 lg:grid-cols-2 pt-2">
              <div className="space-y-4">
                <div className="border-b border-border/50 pb-3 flex items-center gap-2">
                  <PieIcon className="h-4 w-4 text-jade" />
                  <h3 className="text-sm font-semibold text-foreground">请求成功 / 失败分布</h3>
                </div>
                <div className="pt-2">
                  {isLoading && !stats ? (
                    <div className="skeleton h-64 rounded-md" />
                  ) : (
                    <DonutChart data={pieData} total={stats?.requests ?? 0} centerLabel="请求" />
                  )}
                </div>
              </div>

              <div className="space-y-4">
                <div className="border-b border-border/50 pb-3 flex items-center gap-2">
                  <Coins className="h-4 w-4 text-rose" />
                  <h3 className="text-sm font-semibold text-foreground">累计 Token 结构分布</h3>
                </div>
                <div className="pt-2">
                  {isLoading && !stats ? (
                    <div className="skeleton h-64 rounded-md" />
                  ) : (
                    <DonutChart data={tokenData} hoverData={tokenHoverData} total={stats?.totalTokens ?? 0} centerLabel="Token" />
                  )}
                </div>
              </div>
            </div>

            {/* 按模型聚合面板 */}
            <section className="space-y-4 pt-2">
              {byModelError ? (
                <ErrorState className="py-8" message={(byModelError as Error).message} onRetry={() => retryByModel()} />
              ) : byModelLoading && !byModelRows ? (
                <div className="p-5">
                  <div className="skeleton h-[240px] rounded-md" />
                </div>
              ) : (
                <>
                  <div className="flex flex-row items-center justify-between border-b border-border/50 pb-3">
                    <div className="flex items-center gap-2">
                      <Cpu className="h-4 w-4 text-primary" />
                      <h3 className="text-sm font-semibold text-foreground">Top 模型调用热度与消耗明细</h3>
                    </div>
                    {byModel.length > 0 && (
                      <span className="text-xs text-muted-foreground font-mono">共聚合 {byModel.length} 个模型</span>
                    )}
                  </div>
                  <div className="pt-2">
                    {byModel.length === 0 ? (
                      <p className="py-8 text-center text-sm text-muted-foreground">当前筛选范围内暂无模型调用记录</p>
                    ) : (
                      <>
                        <div className="h-[220px]">
                          <ResponsiveContainer width="100%" height="100%">
                            <BarChart data={byModel.slice(0, 8)} margin={{ top: 8, right: 8, bottom: 0, left: 4 }}>
                              <CartesianGrid stroke="hsl(var(--border) / 0.45)" strokeDasharray="3 3" vertical={false} />
                              <XAxis
                                dataKey="model"
                                tick={CHART_TICK}
                                tickLine={false}
                                axisLine={{ stroke: 'hsl(var(--border) / 0.6)' }}
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

                        <div className="mt-6 overflow-x-auto">
                          <table className="w-full text-sm">
                            <TableHeader className="bg-secondary/20">
                              <TableRow className="border-b border-border/60 hover:bg-transparent">
                                <TableHead className="py-3 pl-4 font-semibold text-2xs uppercase tracking-wider text-muted-foreground">模型标识</TableHead>
                                <TableHead className="py-3 num font-semibold text-2xs uppercase tracking-wider text-muted-foreground">请求次数</TableHead>
                                <TableHead className="py-3 num font-semibold text-2xs uppercase tracking-wider text-muted-foreground">失败次数</TableHead>
                                <TableHead className="py-3 num font-semibold text-2xs uppercase tracking-wider text-muted-foreground">总 Tokens</TableHead>
                                <TableHead className="py-3 pr-4 num font-semibold text-2xs uppercase tracking-wider text-muted-foreground">用量占比</TableHead>
                              </TableRow>
                            </TableHeader>
                            <TableBody className="divide-y divide-border/30">
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
                      </>
                    )}
                  </div>
                </>
              )}
            </section>
          </>
        )}
      </div>
    </>
  )
}

function ModelBarTooltip({ active, payload, label }: {
  active?: boolean
  payload?: { name?: string; value?: number | string }[]
  label?: string
}) {
  if (!active || !payload?.length) return null
  return (
    <div className="rounded-xl border border-border/80 bg-card/95 px-3 py-2 text-xs shadow-md backdrop-blur-sm tnum">
      <p className="font-mono text-2xs font-semibold text-muted-foreground">{label}</p>
      <p className="mt-1">
        {payload[0]?.name}: <b className="font-semibold text-foreground">{formatNumber(Number(payload[0]?.value))}</b>
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
              <span className="text-xs text-muted-foreground font-mono">
                {active.name} · {percent(active.value, total)}
              </span>
            </>
          ) : (
            <>
              <span className="tnum font-display text-xl font-semibold">{formatNumber(total)}</span>
              <span className="text-xs text-muted-foreground font-mono">{centerLabel}</span>
            </>
          )}
        </div>
      )}
      {hasData && (
        <div className="absolute bottom-0 left-0 right-0 flex justify-center gap-4">
          {activeData.map((entry, index) => (
            <span
              key={entry.name}
              className="tnum flex cursor-default items-center gap-1.5 text-xs text-muted-foreground transition-opacity font-mono"
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
