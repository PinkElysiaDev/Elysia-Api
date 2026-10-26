import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react'
import { Area, AreaChart, CartesianGrid, Tooltip, XAxis, YAxis } from 'recharts'
import { AudioLines, HeartPulse } from 'lucide-react'
import { ChartFrame } from '@/components/usage-charts'
import { CHART_ENTER_MS, evenIndexTicks, niceTicks, useChartTickSize, useEnterAnimation } from '@/components/usage-chart-model'
import { Seg } from '@/components/ui/seg'
import { ErrorState } from '@/components/ui/states'
import { useUsagePulse } from '@/lib/hooks'
import { cn, CHART_TICK, compactNumber, formatNumber } from '@/lib/utils'
import { formatPulseCaption, formatPulseInterval, PULSE_OPTIONS, pulseWindow, pulseXTicksFit, type PulseRange } from './overview-time'

const MINUTE_MS = 60_000

const SINGLE_PULSE_CHART = {
  height: 188,
  margin: { top: 14, right: 28, bottom: 4, left: 8 },
  yWidth: 40,
  xPadding: { left: 8, right: 8 },
} as const

const PULSE_X_CHROME =
  SINGLE_PULSE_CHART.yWidth +
  SINGLE_PULSE_CHART.margin.left +
  SINGLE_PULSE_CHART.margin.right +
  SINGLE_PULSE_CHART.xPadding.left +
  SINGLE_PULSE_CHART.xPadding.right

const PULSE_X_MAX_TICKS = 13

function useElementWidth<T extends HTMLElement>() {
  const ref = useRef<T>(null)
  const [width, setWidth] = useState(0)
  useLayoutEffect(() => {
    const el = ref.current
    if (!el) return
    const update = () => {
      const next = el.clientWidth
      setWidth((prev) => (prev === next ? prev : next))
    }
    update()
    const ro = new ResizeObserver(update)
    ro.observe(el)
    return () => ro.disconnect()
  }, [])
  return [ref, width] as const
}

/** 首刻度左对齐、末刻度右对齐；昨天/更早的刻度一律带日信息，由上层按宽度抽稀避免重叠。 */
function PulseXTick({
  x,
  y,
  payload,
  nowMs,
  firstT,
  lastT,
}: {
  x?: number
  y?: number
  payload?: { value?: number | string }
  nowMs: number
  firstT: number | undefined
  lastT: number | undefined
}) {
  if (x == null || y == null) return null
  const ms = Number(payload?.value)
  if (!Number.isFinite(ms)) return null
  const isFirst = firstT != null && ms === firstT
  const isLast = lastT != null && ms === lastT
  return (
    <text
      x={x}
      y={y}
      dy={8}
      textAnchor={isFirst ? 'start' : isLast ? 'end' : 'middle'}
      fill={CHART_TICK.fill}
      fontFamily={CHART_TICK.fontFamily}
      fontSize={CHART_TICK.fontSize}
      fontWeight={CHART_TICK.fontWeight}
      pointerEvents="none"
    >
      {formatPulseCaption(ms, nowMs)}
    </text>
  )
}

function durationParts(ms: number | undefined): { value: string; unit: string } {
  if (ms == null) return { value: '—', unit: '' }
  if (!(ms > 0)) return { value: '0', unit: 'ms' }
  if (ms < 1000) return { value: String(Math.round(ms)), unit: 'ms' }
  return { value: (ms / 1000).toFixed(2), unit: 's' }
}

function PulseStat({
  label,
  value,
  unit,
  dotClass,
  valueClass,
}: {
  label: string
  value: string
  unit: string
  dotClass: string
  valueClass: string
}) {
  return (
    <div className="flex items-baseline justify-between gap-3">
      <span className="inline-flex min-w-0 items-center gap-2 text-sm text-foreground">
        <i className={cn('h-2 w-2 shrink-0 rounded-full', dotClass)} aria-hidden />
        {label}
      </span>
      <span className={cn('tnum shrink-0 font-mono text-sm font-semibold', valueClass)}>
        {value} <span className="font-medium">{unit}</span>
      </span>
    </div>
  )
}

interface MinuteUsage {
  from: string
  to: string
  totalTokens: number | undefined
  isLoading: boolean
  hasError: boolean
  onRetry: () => void
}

/** Historical rates use their query window; idle token throughput uses the last complete minute. */
export function PulseSection({ minuteTick, lastMinute }: { minuteTick: number; lastMinute: MinuteUsage }) {
  const [range, setRange] = useState<PulseRange>('60m')
  const { spanMs, bucketMinutes } = pulseWindow(range)

  // 脉搏序列参数
  const pulseParams = useMemo(() => {
    const toMs = minuteTick * MINUTE_MS
    const now = new Date(toMs)
    return {
      from: new Date(toMs - spanMs).toISOString(),
      to: now.toISOString(),
      utcOffsetMinutes: -now.getTimezoneOffset(),
      bucketMinutes,
    }
  }, [minuteTick, spanMs, bucketMinutes])
  const { data: snapshot, isLoading, error, isValidating, mutate } = useUsagePulse(pulseParams)
  const pulseData = error ? undefined : snapshot
  const chartQuery = pulseData?.query ?? pulseParams
  const chartSpanMs = Date.parse(chartQuery.to) - Date.parse(chartQuery.from)
  const animate = useEnterAnimation(chartQuery.bucketMinutes)
  const rangeLabel = PULSE_OPTIONS.find((option) => pulseWindow(option.value).spanMs === chartSpanMs)?.label ?? '当前窗口'
  const requestKey = `${range}:${minuteTick}:${pulseParams.utcOffsetMinutes}`
  const chartKey = `${chartQuery.from}/${chartQuery.to}/${chartQuery.bucketMinutes}/${chartQuery.utcOffsetMinutes}`

  const series = useMemo(() => {
    if (!pulseData) return []
    const { query, result } = pulseData
    const fromMs = Date.parse(query.from)
    const toMs = Date.parse(query.to)
    const offsetMs = query.utcOffsetMinutes * MINUTE_MS
    const bucketMs = query.bucketMinutes * MINUTE_MS
    const byT = new Map((result.points ?? []).map((p) => [p.t, p]))
    let t = Math.floor((fromMs + offsetMs) / bucketMs) * bucketMs - offsetMs
    const out: { t: number; fromMs: number; toMs: number; minutes: number; rpm: number; avgMs: number; p95Ms: number; requests: number; tpm: number }[] = []
    while (t < toMs) {
      const p = byT.get(t)
      const requests = p?.requests ?? 0
      const tokens = p?.totalTokens ?? 0
      const startMs = Math.max(t, fromMs)
      const endMs = Math.min(t + bucketMs, toMs)
      const minutes = (endMs - startMs) / MINUTE_MS
      out.push({
        t,
        fromMs: startMs,
        toMs: endMs,
        minutes,
        rpm: requests / minutes,
        avgMs: p?.avgDurationMs ?? 0,
        p95Ms: p?.p95DurationMs ?? 0,
        requests,
        tpm: tokens / minutes,
      })
      t += bucketMs
    }
    return out
  }, [pulseData])

  const windowStats = useMemo(() => {
    if (!pulseData?.result.window) return null
    const minutes = chartSpanMs / MINUTE_MS
    const w = pulseData.result.window
    return {
      rpm: w.requests / minutes,
      requests: w.requests,
      avgMs: w.avgDurationMs,
      p95Ms: w.p95DurationMs,
      caption: null,
    }
  }, [pulseData, chartSpanMs])

  const yTicks = useMemo(() => niceTicks(Math.max(0, ...series.map((d) => d.rpm)), 2), [series])
  const [chartBoxRef, chartWidth] = useElementWidth<HTMLDivElement>()
  const tickSpan = Math.max(0, chartWidth - PULSE_X_CHROME)
  const tickFontPx = useChartTickSize()
  const nowMs = minuteTick * MINUTE_MS
  const xTicks = useMemo(() => {
    if (series.length === 0) return []
    const maxN = Math.min(PULSE_X_MAX_TICKS, series.length)
    const pick = (d: (typeof series)[number]) => d.t
    // 尚未量到宽度时先用较少刻度，避免小屏第一帧把「昨日」叠到后续时间上。
    if (!(tickSpan > 0)) return evenIndexTicks(series, Math.min(5, maxN), pick)
    for (let n = maxN; n >= 2; n--) {
      const ticks = evenIndexTicks(series, n, pick)
      if (pulseXTicksFit(ticks, nowMs, tickSpan, tickFontPx)) return ticks
    }
    return evenIndexTicks(series, 2, pick)
  }, [series, nowMs, tickSpan, tickFontPx])
  const [hover, setHover] = useState<{ t: number; requestKey: string; chartKey: string } | null>(null)
  useEffect(() => {
    setHover(null)
  }, [requestKey, chartKey, error])
  // Invalidate on the render that changes windows, before the effect clears state.
  const hoverPoint = !error && hover?.requestKey === requestKey && hover.chartKey === chartKey
    ? series.find((point) => point.t === hover.t)
    : undefined
  const shown = hoverPoint
    ? {
        rpm: hoverPoint.rpm,
        requests: hoverPoint.requests,
        avgMs: hoverPoint.avgMs,
        p95Ms: hoverPoint.p95Ms,
        caption: formatPulseInterval(hoverPoint.fromMs, hoverPoint.toMs, nowMs),
      }
    : windowStats
  const minuteTokens = lastMinute.hasError || lastMinute.isLoading ? undefined : lastMinute.totalTokens
  const tpm = hoverPoint?.tpm ?? minuteTokens
  const tpmInterval = hoverPoint
    ? formatPulseInterval(hoverPoint.fromMs, hoverPoint.toMs, nowMs)
    : formatPulseInterval(Date.parse(lastMinute.from), Date.parse(lastMinute.to), nowMs)
  const tpmMinutes = hoverPoint?.minutes ?? 1
  const historyCaption = shown ? shown.caption ?? `${rangeLabel}内` : error ? '加载失败' : '加载中'

  const firstT = xTicks[0]
  const lastT = xTicks[xTicks.length - 1]
  const renderXTick = useCallback(
    (props: Omit<Parameters<typeof PulseXTick>[0], 'nowMs' | 'firstT' | 'lastT'>) => (
      <PulseXTick {...props} nowMs={nowMs} firstT={firstT} lastT={lastT} />
    ),
    [nowMs, firstT, lastT],
  )

  return (
    <section className="space-y-4 pt-2" aria-label="实时脉搏">
      {/* 脉搏顶栏 */}
      <div className="flex flex-row flex-wrap items-center justify-between gap-4 border-b border-border/50 pb-3">
        <div className="flex items-center gap-2">
          <HeartPulse className="h-4 w-4 text-primary" />
          <h2 className="text-sm font-semibold text-foreground">实时脉搏</h2>
        </div>
        <Seg size="lg" aria-label="脉搏时间范围" options={PULSE_OPTIONS} value={range} onChange={setRange} />
      </div>

      {/* 左右布局：左侧读数 + 右侧波形 */}
      <div className="grid grid-cols-1 items-end gap-6 lg:grid-cols-[220px_minmax(0,1fr)] xl:grid-cols-[240px_minmax(0,1fr)]">
        <div className="flex flex-col gap-9 border-b lg:border-b-0 lg:border-r border-border/40 pb-4 lg:pb-[26px] pr-0 lg:pr-6">
          <div>
            <div className="flex items-baseline gap-1.5">
              <span className="tnum font-display text-[length:clamp(1.5rem,0.875rem+0.78125vw,1.875rem)] font-medium leading-none tracking-tight text-foreground">
                {shown ? shown.rpm.toFixed(1) : '—'}
              </span>
              <span className="font-mono text-xs font-semibold text-muted-foreground/80">
                RPM
              </span>
            </div>
            <p className="mt-2 font-mono text-xs text-muted-foreground">
              <span className={shown?.caption ? 'font-semibold text-rose' : undefined}>{historyCaption}</span>
              {shown && <> {formatNumber(shown.requests)} 次请求</>}
            </p>
          </div>

          <div className="space-y-2.5">
            <PulseStat
              label="平均时延"
              {...durationParts(shown?.avgMs)}
              dotClass="bg-jade"
              valueClass="text-jade"
            />
            <PulseStat
              label="P95 时延"
              {...durationParts(shown?.p95Ms)}
              dotClass="bg-orchid"
              valueClass="text-orchid"
            />
            <div role="group" aria-label="Token 吞吐">
              <PulseStat
                label={hoverPoint ? '时段平均吞吐' : '上一分钟吞吐'}
                value={tpm == null ? '—' : compactNumber(tpm)}
                unit="tpm"
                dotClass="bg-amber"
                valueClass="text-amber"
              />
              <p aria-label="吞吐时间范围" className="mt-1 text-xs text-muted-foreground">
                {tpmInterval} · {tpmMinutes}分钟平均
                {!hoverPoint && lastMinute.isLoading && ' · 加载中'}
                {!hoverPoint && lastMinute.hasError && (
                  <> · 加载失败 <button type="button" className="underline underline-offset-2" aria-label="重试上一分钟统计" onClick={lastMinute.onRetry}>重试</button></>
                )}
              </p>
            </div>
          </div>
        </div>

        {/* 右侧：请求/分钟波形贴在区域底部，与左侧读数底边对齐 */}
        <div className="flex min-w-0 flex-col justify-end">
          <div ref={chartBoxRef} className="space-y-2">
          <div className="flex items-center justify-between text-xs font-semibold text-muted-foreground">
            <span className="flex items-center gap-1.5">
              <AudioLines className="h-3.5 w-3.5 text-rose" />
              请求 / 分钟
            </span>
            {isValidating && pulseData && <span role="status">更新中</span>}
          </div>

          {error ? (
            <ErrorState className="h-[188px] py-6" message={(error as Error).message} onRetry={() => mutate()} />
          ) : isLoading && !pulseData ? (
            <div className="skeleton h-[188px] w-full rounded-md" />
          ) : (
            <ChartFrame key={chartQuery.bucketMinutes} height={SINGLE_PULSE_CHART.height}>
              <AreaChart
                data={series}
                margin={SINGLE_PULSE_CHART.margin}
                onMouseMove={(state) => {
                  const t = state?.activePayload?.[0]?.payload?.t
                  if (typeof t !== 'number') {
                    setHover(null)
                    return
                  }
                  setHover((prev) => prev?.t === t && prev.requestKey === requestKey && prev.chartKey === chartKey ? prev : { t, requestKey, chartKey })
                }}
                onMouseLeave={() => setHover(null)}
              >
                <defs>
                  <linearGradient id="pulseSingleFill" x1="0" y1="0" x2="0" y2="1">
                    <stop offset="5%" stopColor="var(--rose)" stopOpacity={0.16} />
                    <stop offset="95%" stopColor="var(--rose)" stopOpacity={0.0} />
                  </linearGradient>
                </defs>
                <CartesianGrid stroke="hsl(var(--border) / 0.4)" strokeDasharray="3 3" vertical={false} />
                <XAxis
                  dataKey="t"
                  ticks={xTicks}
                  interval={0}
                  minTickGap={0}
                  tick={renderXTick}
                  tickLine={false}
                  axisLine={{ stroke: 'hsl(var(--border) / 0.6)' }}
                  scale="point"
                  padding={SINGLE_PULSE_CHART.xPadding}
                />
                <YAxis
                  tick={{ ...CHART_TICK, fill: 'var(--rose)' }}
                  tickLine={false}
                  axisLine={false}
                  width={SINGLE_PULSE_CHART.yWidth}
                  ticks={yTicks}
                  domain={[0, yTicks[yTicks.length - 1] ?? 1]}
                />
                <Tooltip
                  content={() => null}
                  wrapperStyle={{ display: 'none' }}
                  cursor={{ stroke: 'var(--rose)', strokeWidth: 1, strokeDasharray: '3 3' }}
                />
                <Area
                  dataKey="rpm"
                  name="RPM"
                  type="monotone"
                  stroke="var(--rose)"
                  strokeWidth={2.4}
                  fill="url(#pulseSingleFill)"
                  dot={false}
                  activeDot={{ r: 4, fill: 'var(--rose)', stroke: 'hsl(var(--card))', strokeWidth: 2 }}
                  isAnimationActive={animate}
                  animationDuration={CHART_ENTER_MS}
                />
              </AreaChart>
            </ChartFrame>
          )}
          </div>
        </div>
      </div>
    </section>
  )
}
