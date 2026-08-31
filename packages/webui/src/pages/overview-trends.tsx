import { useCallback, useLayoutEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import {
  Area,
  Bar,
  CartesianGrid,
  ComposedChart,
  Line,
  Tooltip,
  XAxis,
  YAxis,
} from 'recharts'
import { Layers, TrendingUp, type LucideIcon } from 'lucide-react'
import { ChartFrame, ChartTooltip } from '@/components/usage-charts'
import {
  CHART_ENTER_MS,
  MODEL_COLORS,
  OVERVIEW_CHART,
  ticksFor,
  useCommittedRange,
  useEnterAnimation,
} from '@/components/usage-chart-model'
import { Fchip } from '@/components/ui/fchip'
import { Seg } from '@/components/ui/seg'
import { ErrorState } from '@/components/ui/states'
import { ModelBreakdownTooltip } from '@/components/model-breakdown-tooltip'
import { useUsageByModelDaily, useUsageTrend } from '@/lib/hooks'
import type { UsageModelDailyPoint } from '@/lib/types'
import { bucketedTimeISO, CHART_TICK, cn, compactNumber } from '@/lib/utils'
import { offsetDayKey, offsetDayStart } from './overview-time'

export type TrendPerspective = 'overview' | 'breakdown'

const OTHER_KEY = '__other__'
const UNNAMED_KEY = '__unnamed__'

// 轴 tick 样式常量：recharts 对 prop 引用敏感，内联对象会触发无谓的全图 reconcile。
const TOK_TICK = { ...CHART_TICK, fill: 'var(--jade)' }
const REQ_TICK = { ...CHART_TICK, fill: 'var(--rose)' }

function seriesKey(row: UsageModelDailyPoint): string {
  if (row.isOther) return OTHER_KEY
  return row.model || UNNAMED_KEY
}

function seriesLabel(key: string): string {
  if (key === OTHER_KEY) return '其他'
  if (key === UNNAMED_KEY) return '未知模型'
  return key
}

/** 分流图 tooltip：过滤 0 值系列后交给通用气泡。模块级定义，避免每渲染新函数引用。 */
function BreakdownTooltipContent({
  active,
  payload,
  label,
}: {
  active?: boolean
  payload?: Array<{ name?: string; value?: number | string; color?: string; dataKey?: string | number }>
  label?: ReactNode
}) {
  const items = (payload ?? [])
    .filter((e) => Number(e.value) > 0)
    .map((e) => ({
      name: seriesLabel(String(e.name ?? e.dataKey ?? '')),
      value: Number(e.value),
      color: e.color,
    }))
  return <ChartTooltip active={active && items.length > 0} payload={items} label={label} />
}

/** 标题即 Tab：无边框文字切换，底部 2px 瑰梅游标平滑滑移。 */
function TitleTabs<T extends string>({
  options,
  value,
  onChange,
  'aria-label': ariaLabel,
}: {
  options: { value: T; label: ReactNode; icon: LucideIcon }[]
  value: T
  onChange: (value: T) => void
  'aria-label'?: string
}) {
  const listRef = useRef<HTMLDivElement>(null)
  const btnRefs = useRef(new Map<T, HTMLButtonElement>())
  const [bar, setBar] = useState({ left: 0, width: 0 })

  const measure = useCallback(() => {
    const list = listRef.current
    const btn = btnRefs.current.get(value)
    if (!list || !btn) return
    const lr = list.getBoundingClientRect()
    const br = btn.getBoundingClientRect()
    setBar({ left: br.left - lr.left, width: br.width })
  }, [value])

  const optionKey = options.map((o) => `${o.value}:${String(o.label)}`).join('|')
  useLayoutEffect(() => {
    measure()
    const list = listRef.current
    if (!list) return
    const observer = new ResizeObserver(measure)
    observer.observe(list)
    for (const btn of btnRefs.current.values()) observer.observe(btn)
    return () => observer.disconnect()
  }, [measure, optionKey])

  return (
    <div className="relative min-w-0">
      <div ref={listRef} role="tablist" aria-label={ariaLabel} className="flex items-center gap-8">
        {options.map((opt) => {
          const on = opt.value === value
          const Icon = opt.icon
          return (
            <button
              key={opt.value}
              type="button"
              role="tab"
              aria-selected={on}
              ref={(el) => {
                if (el) btnRefs.current.set(opt.value, el)
                else btnRefs.current.delete(opt.value)
              }}
              onClick={() => onChange(opt.value)}
              className={cn(
                'inline-flex items-center gap-2 pb-2.5 text-sm tracking-tight transition-colors duration-200',
                on
                  ? 'font-semibold text-foreground'
                  : 'font-medium text-muted-foreground hover:text-foreground',
              )}
            >
              <Icon className={cn('h-4 w-4', on ? 'text-primary' : 'text-muted-foreground/80')} />
              {opt.label}
            </button>
          )
        })}
      </div>
      <span
        aria-hidden
        className="pointer-events-none absolute bottom-[-1px] h-0.5 rounded-full bg-rose transition-[left,width] duration-300 ease-out"
        style={{ left: bar.left, width: bar.width }}
      />
    </div>
  )
}

/**
 * 时序透视中枢：
 * 将「总量趋势 (Traffic & Tokens)」与「模型日调用分流 (Model Breakdown)」
 * 合并为一个无界时序视窗，避免页面连续堆叠多张重复折线图。
 */
export function TemporalTrendSection({ minuteTick }: { minuteTick: number }) {
  const [perspective, setPerspective] = useState<TrendPerspective>('overview')
  const [range, setRange] = useState<'7d' | '30d'>('30d')

  // Overview 模式下的图例开关
  const [showReqLine, setShowReqLine] = useState(true)
  const [showTokBar, setShowTokBar] = useState(true)
  const [reqGen, setReqGen] = useState(0)
  const [tokGen, setTokGen] = useState(0)

  // 1. Overview 趋势数据源
  const trendParams = useMemo(() => {
    const days = range === '7d' ? 7 : 30
    const now = new Date(minuteTick * 60_000)
    const offsetMinutes = -now.getTimezoneOffset()
    const from = offsetDayStart(now.getTime(), offsetMinutes, -(days - 1))
    return { from: new Date(from).toISOString(), utcOffsetMinutes: offsetMinutes }
  }, [range, minuteTick])

  const {
    data: trendBuckets,
    isLoading: trendLoading,
    error: trendError,
    isValidating: trendValidating,
    mutate: retryTrend,
  } = useUsageTrend(trendParams)

  // 2. Breakdown 模型日调用（to 取下一 5 分钟边界，包含当前桶内新记录）
  //    status=success：调用分布只反映成功请求，失败调用不折算成"调用量"。
  const breakdownParams = useMemo(() => {
    const days = range === '7d' ? 7 : 30
    const nowMs = minuteTick * 60_000
    const toMs = new Date(bucketedTimeISO(nowMs, 5 * 60_000)).getTime()
    const offsetMinutes = -new Date(nowMs).getTimezoneOffset()
    const from = offsetDayStart(nowMs, offsetMinutes, -(days - 1))
    return {
      from: new Date(from).toISOString(),
      to: new Date(toMs).toISOString(),
      utcOffsetMinutes: offsetMinutes,
      top: 8,
      status: 'success' as const,
    }
  }, [range, minuteTick])

  const {
    data: breakdownData,
    isLoading: breakdownLoading,
    error: breakdownError,
    isValidating: breakdownValidating,
    mutate: retryBreakdown,
  } = useUsageByModelDaily(breakdownParams)

  const chartRange = useCommittedRange(range, perspective === 'overview' ? !trendValidating : !breakdownValidating)
  const reqAnimate = useEnterAnimation(`${chartRange}-req-${reqGen}-${perspective}`)
  const tokAnimate = useEnterAnimation(`${chartRange}-tok-${tokGen}-${perspective}`)
  const breakdownAnimate = useEnterAnimation(`${chartRange}-breakdown-${perspective}`)

  // 本地零点锚点：series 的日期键只随本地日变化。分钟 tick 变化时 SWR 若返回
  // 相同数据（引用不变），series 不再重建，图表不会每分钟全量 reconcile。
  const dayAnchorMs = useMemo(() => {
    const d = new Date(minuteTick * 60_000)
    d.setHours(0, 0, 0, 0)
    return d.getTime()
  }, [minuteTick])

  // 综合趋势图时序数据
  const trendSeries = useMemo(() => {
    const byDay = new Map((trendBuckets ?? []).map((b) => [b.date, b]))
    const days = chartRange === '7d' ? 7 : 30
    const now = new Date(dayAnchorMs)
    const offsetMinutes = -now.getTimezoneOffset()
    const out: Array<{
      date: string
      label: string
      req: number
      tok: number
      successReq: number
      failedReq: number
      modelTokens?: Record<string, number>
    }> = []

    for (let i = days - 1; i >= 0; i--) {
      const key = offsetDayKey(offsetDayStart(now.getTime(), offsetMinutes, -i), offsetMinutes)
      const b = byDay.get(key)
      out.push({
        date: key,
        label: key.slice(5).replace('-', '/'),
        req: b?.requests ?? 0,
        tok: b?.tokens ?? 0,
        successReq: b?.successRequests ?? Math.max(0, (b?.requests ?? 0) - (b?.failedRequests ?? 0)),
        failedReq: b?.failedRequests ?? 0,
        modelTokens: b?.modelTokens,
      })
    }
    return out
  }, [trendBuckets, chartRange, dayAnchorMs])

  // 窗口内是否出现过失败调用：无失败时不画零线。失败虚线与「请求折线」同组开关。
  const hasFailedCalls = useMemo(() => trendSeries.some((d) => d.failedReq > 0), [trendSeries])

  // 刻度数组显式缓存并锁定 domain（ticksFor 恒为 3 段）：两个透视的
  // 网格线都落在 1/3、2/3 高度，切换时几何位置一致。
  const reqTicks = useMemo(() => ticksFor(trendSeries.map((d) => d.req)), [trendSeries])
  const tokTicks = useMemo(() => ticksFor(trendSeries.map((d) => d.tok)), [trendSeries])

  // 模型分流堆叠图时序数据
  const { breakdownSeries, models } = useMemo(() => {
    const days = chartRange === '7d' ? 7 : 30
    const now = new Date(dayAnchorMs)
    const offsetMinutes = -now.getTimezoneOffset()
    const totals = new Map<string, number>()
    const byDate = new Map<string, Map<string, number>>()
    for (const row of breakdownData ?? []) {
      const key = seriesKey(row)
      totals.set(key, (totals.get(key) ?? 0) + row.requests)
      let day = byDate.get(row.date)
      if (!day) {
        day = new Map()
        byDate.set(row.date, day)
      }
      day.set(key, row.requests)
    }
    const models = [...totals.entries()]
      .sort((a, b) => {
        if (a[0] === OTHER_KEY) return 1
        if (b[0] === OTHER_KEY) return -1
        return b[1] - a[1] || seriesLabel(a[0]).localeCompare(seriesLabel(b[0]), 'zh-Hans-CN')
      })
      .map(([name]) => name)
    const breakdownSeries: Array<Record<string, string | number>> = []
    for (let i = days - 1; i >= 0; i--) {
      const key = offsetDayKey(offsetDayStart(now.getTime(), offsetMinutes, -i), offsetMinutes)
      const day = byDate.get(key)
      const row: Record<string, string | number> = { date: key, label: key.slice(5).replace('-', '/') }
      for (const name of models) {
        row[name] = day?.get(name) ?? 0
      }
      breakdownSeries.push(row)
    }
    return { breakdownSeries, models }
  }, [breakdownData, chartRange, dayAnchorMs])

  const breakdownTicks = useMemo(
    () => ticksFor(breakdownSeries.flatMap((row) => models.map((name) => Number(row[name] || 0)))),
    [breakdownSeries, models],
  )

  return (
    <section className="space-y-4 pt-1" aria-label="时序洞察与趋势">
      {/* 顶部控制栏：标题即 Tab，瑰梅游标滑移；右侧图例与时间窗 */}
      <div className="flex flex-row flex-wrap items-end justify-between gap-x-6 gap-y-2 border-b border-border/50">
        <TitleTabs
          aria-label="透视维度"
          value={perspective}
          onChange={setPerspective}
          options={[
            { value: 'overview', label: '请求与 Token 趋势', icon: TrendingUp },
            { value: 'breakdown', label: '模型调用日分布', icon: Layers },
          ]}
        />

        <div className="flex flex-wrap items-center gap-2.5 pb-2.5">
          {/* 占位保持顶栏高度：分流态隐藏开关但不收走空间，避免图表上下跳。 */}
          <div
            className={
              perspective === 'overview'
                ? 'flex flex-wrap items-center gap-2.5'
                : 'invisible pointer-events-none flex flex-wrap items-center gap-2.5'
            }
          >
            <Fchip
              label="请求折线"
              color="var(--rose)"
              active={showReqLine}
              onClick={() => {
                if (perspective !== 'overview') return
                if (showReqLine) {
                  setShowReqLine(false)
                } else {
                  setReqGen((g) => g + 1)
                  setShowReqLine(true)
                }
              }}
            />
            <Fchip
              label="Token 柱图"
              color="var(--jade)"
              active={showTokBar}
              onClick={() => {
                if (perspective !== 'overview') return
                if (showTokBar) {
                  setShowTokBar(false)
                } else {
                  setTokGen((g) => g + 1)
                  setShowTokBar(true)
                }
              }}
            />
          </div>

          {/* 时间范围切换 */}
          <Seg
            size="lg"
            aria-label="时间范围"
            options={[
              { value: '7d', label: '近 7 天' },
              { value: '30d', label: '近 30 天' },
            ]}
            value={range}
            onChange={setRange}
          />
        </div>
      </div>

      {/* 图表绘制视窗：固定槽位，切换透视只换数据，不改盒子位置。 */}
      <div className="pt-1">
        <div className="w-full" style={{ height: OVERVIEW_CHART.height }}>
          {perspective === 'overview' ? (
            trendError ? (
              <ErrorState className="h-full py-8" message={(trendError as Error).message} onRetry={() => retryTrend()} />
            ) : trendLoading && !trendBuckets ? (
              <div className="skeleton h-full w-full rounded-md" />
            ) : (
              <ChartFrame height={OVERVIEW_CHART.height}>
                <ComposedChart data={trendSeries} margin={OVERVIEW_CHART.margin} barCategoryGap={0}>
                  <defs>
                    <linearGradient id="reqAreaGrad" x1="0" y1="0" x2="0" y2="1">
                      <stop offset="5%" stopColor="var(--rose)" stopOpacity={0.12} />
                      <stop offset="95%" stopColor="var(--rose)" stopOpacity={0.0} />
                    </linearGradient>
                  </defs>
                  <CartesianGrid stroke="hsl(var(--border) / 0.4)" strokeDasharray="3 3" vertical={false} />
                  <XAxis
                    dataKey="label"
                    tick={CHART_TICK}
                    tickLine={false}
                    axisLine={{ stroke: 'hsl(var(--border) / 0.6)' }}
                    interval="preserveStartEnd"
                    minTickGap={OVERVIEW_CHART.xMinTickGap}
                    scale={OVERVIEW_CHART.xScale}
                    padding={OVERVIEW_CHART.xPadding}
                  />
                  <YAxis
                    yAxisId="tok"
                    tick={TOK_TICK}
                    tickLine={false}
                    axisLine={false}
                    width={OVERVIEW_CHART.yLeftWidth}
                    ticks={tokTicks}
                    domain={[0, tokTicks[tokTicks.length - 1] || 1]}
                    tickFormatter={(v: number) => compactNumber(v)}
                  />
                  <YAxis
                    yAxisId="req"
                    orientation="right"
                    tick={REQ_TICK}
                    tickLine={false}
                    axisLine={false}
                    width={OVERVIEW_CHART.yRightWidth}
                    allowDecimals={false}
                    ticks={reqTicks}
                    domain={[0, reqTicks[reqTicks.length - 1] || 1]}
                  />
                  <Tooltip
                    content={<ModelBreakdownTooltip />}
                    cursor={{ fill: 'var(--wash)' }}
                  />

                  {showTokBar && (
                    <Bar
                      key={`tok-${chartRange}-${tokGen}-${perspective}`}
                      yAxisId="tok"
                      dataKey="tok"
                      name="Token 消耗"
                      fill="var(--jade)"
                      fillOpacity={0.4}
                      radius={[3, 3, 0, 0]}
                      maxBarSize={22}
                      isAnimationActive={tokAnimate}
                      animationDuration={CHART_ENTER_MS}
                    />
                  )}

                  {showReqLine && (
                    <>
                      <Area
                        key={`req-area-${chartRange}-${reqGen}-${perspective}`}
                        yAxisId="req"
                        dataKey="req"
                        type="monotone"
                        fill="url(#reqAreaGrad)"
                        stroke="none"
                        tooltipType="none"
                        isAnimationActive={reqAnimate}
                        animationDuration={CHART_ENTER_MS}
                      />
                      <Line
                        key={`req-line-${chartRange}-${reqGen}-${perspective}`}
                        yAxisId="req"
                        dataKey="req"
                        name="请求数"
                        type="monotone"
                        stroke="var(--rose)"
                        strokeWidth={2.2}
                        dot={false}
                        isAnimationActive={reqAnimate}
                        animationDuration={CHART_ENTER_MS}
                      />
                    </>
                  )}

                  {/* 失败调用虚线：与请求折线同组；关闭「请求折线」时一并隐藏。无失败时不画零线。 */}
                  {showReqLine && hasFailedCalls && (
                    <Line
                      key={`failed-line-${chartRange}-${perspective}`}
                      yAxisId="req"
                      dataKey="failedReq"
                      name="失败调用"
                      type="monotone"
                      stroke="var(--ember)"
                      strokeWidth={1.6}
                      strokeDasharray="5 4"
                      dot={false}
                      isAnimationActive={reqAnimate}
                      animationDuration={CHART_ENTER_MS}
                    />
                  )}
                </ComposedChart>
              </ChartFrame>
            )
          ) : breakdownError ? (
            <ErrorState className="h-full py-8" message={(breakdownError as Error).message} onRetry={() => retryBreakdown()} />
          ) : breakdownLoading && !breakdownData ? (
            <div className="skeleton h-full w-full rounded-md" />
          ) : models.length === 0 ? (
            <p className="flex h-full items-center justify-center text-xs text-muted-foreground">当前窗口暂无模型调用数据</p>
          ) : (
            <ChartFrame height={OVERVIEW_CHART.height}>
              <ComposedChart data={breakdownSeries} margin={OVERVIEW_CHART.margin}>
                <CartesianGrid stroke="hsl(var(--border) / 0.4)" strokeDasharray="3 3" vertical={false} />
                <XAxis
                  dataKey="label"
                  tick={CHART_TICK}
                  tickLine={false}
                  axisLine={{ stroke: 'hsl(var(--border) / 0.6)' }}
                  interval="preserveStartEnd"
                  minTickGap={OVERVIEW_CHART.xMinTickGap}
                  scale={OVERVIEW_CHART.xScale}
                  padding={OVERVIEW_CHART.xPadding}
                />
                {/* 数值轴在左（中性刻度色），右侧等宽占位保持绘图区宽度与
                    「请求与 Token 趋势」一致；domain 锁定后网格线恒在 1/3、2/3 */}
                <YAxis
                  yAxisId="req"
                  tick={CHART_TICK}
                  tickLine={false}
                  axisLine={false}
                  width={OVERVIEW_CHART.yLeftWidth}
                  allowDecimals={false}
                  ticks={breakdownTicks}
                  domain={[0, breakdownTicks[breakdownTicks.length - 1] || 1]}
                />
                <YAxis
                  yAxisId="reqRight"
                  orientation="right"
                  tick={false}
                  tickLine={false}
                  axisLine={false}
                  width={OVERVIEW_CHART.yRightWidth}
                />
                <Tooltip content={<BreakdownTooltipContent />} cursor={{ fill: 'var(--wash)' }} />
                {models.map((name, i) => (
                  <Area
                    key={name}
                    yAxisId="req"
                    dataKey={name}
                    name={seriesLabel(name)}
                    type="monotone"
                    stroke={MODEL_COLORS[i % MODEL_COLORS.length]}
                    fill={MODEL_COLORS[i % MODEL_COLORS.length]}
                    fillOpacity={0.12}
                    strokeWidth={1.4}
                    strokeOpacity={0.85}
                    dot={false}
                    isAnimationActive={breakdownAnimate}
                    animationDuration={CHART_ENTER_MS}
                  />
                ))}
              </ComposedChart>
            </ChartFrame>
          )}
        </div>

        <ul className="flex min-h-[2.25rem] flex-wrap gap-x-4 gap-y-2 pt-2">
          {perspective === 'breakdown' &&
            models.map((name, i) => (
              <li key={name} className="inline-flex items-center gap-1.5 text-xs text-muted-foreground">
                <i
                  className="h-2 w-2 shrink-0 rounded-full"
                  style={{ background: MODEL_COLORS[i % MODEL_COLORS.length] }}
                  aria-hidden
                />
                <span className="font-mono">{seriesLabel(name)}</span>
              </li>
            ))}
        </ul>
      </div>
    </section>
  )
}
