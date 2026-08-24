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
import { BarChart3 } from 'lucide-react'
import { PageHeader } from '@/components/page-header'
import { SectionHeader } from '@/components/section-header'
import { KpiCard, KpiGrid } from '@/components/kpi-card'
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
    const to = new Date(minuteTick * 60_000).toISOString()
    return {
      from: startOfRange(range, to),
      to,
      groupNames: groupNames.length ? groupNames : undefined,
      modelNames: modelNames.length ? modelNames : undefined,
      keyNames: keyNames.length ? keyNames : undefined,
    }
  }, [range, groupNames, modelNames, keyNames, minuteTick])

  const { data: stats, isLoading, error, mutate } = useUsageStats(params)

  // 按模型聚合（后端 SQL 分组，与 stats 同窗口同筛选）
  const {
    data: byModelRows,
    isLoading: byModelLoading,
    error: byModelError,
    mutate: retryByModel,
  } = useUsageByModel(params)
  const byModel = byModelRows ?? []

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
      <PageHeader title="Usage 统计" description="按时间范围与过滤条件汇总的请求与 token 用量" />

      {/* 过滤条 */}
      <div className="flex flex-wrap items-center gap-2.5 border-b border-border py-3">
        <div className="w-[150px]">
          <RangeSelect value={range} onChange={setRange} />
        </div>
        <div className="min-w-[150px]">
          <MultiSelect options={groupOptions} value={groupNames} onChange={setGroupNames} placeholder="全部模型组" searchPlaceholder="搜索模型组" />
        </div>
        <div className="min-w-[150px]">
          <MultiSelect options={modelOptions} value={modelNames} onChange={setModelNames} placeholder="全部模型" searchPlaceholder="搜索模型" />
        </div>
        <div className="min-w-[140px]">
          <MultiSelect options={keyOptions} value={keyNames} onChange={setKeyNames} placeholder="全部调用方" searchPlaceholder="搜索调用方" />
        </div>
      </div>

      {error ? (
        <ErrorState message={(error as Error).message} onRetry={() => mutate()} />
      ) : (
        <>
          <section aria-label="汇总">
            <KpiGrid cols={6}>
              <KpiCard label="请求" value={stats ? formatNumber(stats.requests) : '—'} />
              <KpiCard
                label="成功"
                value={stats ? formatNumber(stats.success) : '—'}
                deltaTone="up"
                delta={stats ? `占比 ${percent(stats.success, stats.requests)}` : undefined}
              />
              <KpiCard
                label="失败"
                value={stats ? formatNumber(stats.failed) : '—'}
                deltaTone={stats?.failed ? 'down' : 'neutral'}
                delta={stats ? (stats.failed ? `占比 ${percent(stats.failed, stats.requests)}` : '无失败') : undefined}
              />
              <KpiCard label="Token 总量" value={stats ? compactNumber(stats.totalTokens) : '—'} />
              <KpiCard
                label="缓存命中"
                value={stats ? compactNumber(stats.cacheHitTokens) : '—'}
                delta={stats ? `命中率 ${formatHitRate(stats.cacheHitRate)}` : undefined}
              />
              <KpiCard
                label="平均耗时"
                value={stats ? formatDuration(stats.avgDurationMs) : '—'}
                delta={stats ? `平均首字 ${formatDuration(stats.avgFirstByteMs)}` : undefined}
              />
            </KpiGrid>
          </section>

          {/* 分布环图 */}
          <section className="border-t border-border pt-6" aria-label="分布">
            <div className="grid grid-cols-[repeat(auto-fit,minmax(min(320px,100%),1fr))] gap-9">
              <div>
                <SectionHeader sm title="请求成功 / 失败" />
                {isLoading && !stats ? (
                  <div className="skeleton h-64 rounded-md" />
                ) : (
                  <DonutChart data={pieData} total={stats?.requests ?? 0} centerLabel="请求" />
                )}
              </div>
              <div>
                <SectionHeader sm title="累计 Token 分布" />
                {isLoading && !stats ? (
                  <div className="skeleton h-64 rounded-md" />
                ) : (
                  <DonutChart data={tokenData} hoverData={tokenHoverData} total={stats?.totalTokens ?? 0} centerLabel="Token" />
                )}
              </div>
            </div>
          </section>

          <section className="border-t border-border pt-6" aria-label="按模型">
            {byModelError ? (
              <ErrorState className="py-8" message={(byModelError as Error).message} onRetry={() => retryByModel()} />
            ) : byModelLoading && !byModelRows ? (
              <div className="skeleton h-[240px] rounded-md" />
            ) : (
              <>
                <SectionHeader
                  sm
                  title="按模型请求量"
                  count={byModel.length > 0 ? `共 ${byModel.length} 个模型` : undefined}
                />
                {byModel.length === 0 ? (
                  <p className="py-8 text-center text-sm text-muted-foreground">当前筛选范围内暂无模型调用</p>
                ) : (
                  <>
                    <div className="h-[240px]">
                      <ResponsiveContainer width="100%" height="100%">
                        <BarChart data={byModel.slice(0, 8)} margin={{ top: 8, right: 8, bottom: 0, left: 4 }}>
                          <CartesianGrid stroke="var(--border)" strokeDasharray="1 5" vertical={false} />
                          <XAxis
                            dataKey="model"
                            tick={CHART_TICK}
                            tickLine={false}
                            axisLine={{ stroke: 'var(--border)' }}
                            interval={0}
                            tickFormatter={(v: string) => {
                              const label = v || '—'
                              return label.length > 12 ? label.slice(0, 11) + '…' : label
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
                          <Bar dataKey="requests" name="请求数" radius={[2, 2, 0, 0]} maxBarSize={36}>
                            {byModel.slice(0, 8).map((entry) => (
                              <Cell key={entry.model || '__unknown__'} fill="var(--rose)" fillOpacity={0.55} />
                            ))}
                          </Bar>
                        </BarChart>
                      </ResponsiveContainer>
                    </div>
                    <div className="mt-6 overflow-x-auto">
                      <table className="w-full text-sm">
                        <TableHeader>
                          <TableRow>
                            <TableHead>模型</TableHead>
                            <TableHead className="num">请求数</TableHead>
                            <TableHead className="num">失败</TableHead>
                            <TableHead className="num">Tokens</TableHead>
                            <TableHead className="num">占比</TableHead>
                          </TableRow>
                        </TableHeader>
                        <TableBody>
                          {byModel.map((row) => (
                            <TableRow key={row.model || '__unknown__'}>
                              <TableCell className="max-w-[280px] truncate font-mono text-xs" title={row.model || '未知模型'}>
                                {row.model || '—'}
                              </TableCell>
                              <TableCell className="num">{formatNumber(row.requests)}</TableCell>
                              <TableCell className="num">
                                <span className={row.failed > 0 ? 'text-ember' : undefined}>{row.failed}</span>
                              </TableCell>
                              <TableCell className="num">{formatNumber(row.tokens)}</TableCell>
                              <TableCell className="num">{percent(row.requests, stats?.requests ?? 0)}</TableCell>
                            </TableRow>
                          ))}
                        </TableBody>
                      </table>
                    </div>
                  </>
                )}
              </>
            )}
          </section>
        </>
      )}
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
