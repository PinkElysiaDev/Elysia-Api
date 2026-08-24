import { useMemo, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import {
  Bar,
  CartesianGrid,
  ComposedChart,
  Line,
  ReferenceDot,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from 'recharts'
import { MoveUpRight } from 'lucide-react'
import { PageHeader } from '@/components/page-header'
import { SectionHeader } from '@/components/section-header'
import { KpiCard, KpiGrid } from '@/components/kpi-card'
import { RoleWatermark } from '@/components/role-watermark'
import { Fchip } from '@/components/ui/fchip'
import { Seg } from '@/components/ui/seg'
import { ErrorState } from '@/components/ui/states'
import { CodePill, Dot, PlatformBadge } from '@/components/badges'
import {
  useHealth,
  useUsageStats,
  useUsageTrend,
  useUsageByModel,
  useUsageLogs,
  useSources,
  useModels,
  useMinuteTick,
} from '@/lib/hooks'
import { CHART_TICK, compactNumber, formatDuration, formatHitRate, formatNumber, percent } from '@/lib/utils'

/** 本地零点（今日 / 昨日），用于 KPI 的"今日 vs 昨日"窗口。 */
function localMidnight(offsetDays = 0, reference = new Date()): Date {
  const d = new Date(reference)
  d.setHours(0, 0, 0, 0)
  d.setDate(d.getDate() + offsetDays)
  return d
}

/** 本地时区的 YYYY-MM-DD（后端趋势按相同固定 UTC offset 分桶）。 */
function localDateKey(d: Date): string {
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`
}

/** RFC3339 → 本地 HH:mm:ss（日志时间戳统一走本地时区展示）。 */
function localTimeOf(iso: string): string {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return iso
  return `${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}:${String(d.getSeconds()).padStart(2, '0')}`
}

function summarizeSourceHealth(enabled: boolean, total: number, available: number) {
  if (!enabled) return { state: 'off' as const, label: '已停用', tone: 'text-muted-foreground' }
  if (total === 0) return { state: 'off' as const, label: '无模型', tone: 'text-muted-foreground' }
  if (available === total) return { state: 'ok' as const, label: '正常', tone: 'text-jade' }
  return { state: 'err' as const, label: `${available}/${total} 可用`, tone: 'text-ember' }
}

export function OverviewPage() {
  const minuteTick = useMinuteTick()
  const navigate = useNavigate()

  // 今日 / 昨日窗口；to 随 minuteTick 写入查询 key。
  const todayParams = useMemo(() => {
    const now = new Date(minuteTick * 60_000)
    return { from: localMidnight(0, now).toISOString(), to: now.toISOString() }
  }, [minuteTick])
  const yesterdayParams = useMemo(
    () => {
      const now = new Date(minuteTick * 60_000)
      return {
        from: localMidnight(-1, now).toISOString(),
        to: localMidnight(0, now).toISOString(),
      }
    },
    [minuteTick],
  )
  const { data: todayData, error: todayError } = useUsageStats(todayParams)
  const { data: yesterdayData, error: yesterdayError } = useUsageStats(yesterdayParams)
  const today = todayError ? undefined : todayData
  const yesterday = yesterdayError ? undefined : yesterdayData

  // 上一完整分钟 [from, to)。
  const lastMinuteParams = useMemo(() => {
    const toMs = minuteTick * 60_000
    return {
      from: new Date(toMs - 60_000).toISOString(),
      to: new Date(toMs).toISOString(),
    }
  }, [minuteTick])
  const { data: lastMinuteData, error: lastMinuteError } = useUsageStats(lastMinuteParams)
  const lastMinute = lastMinuteError ? undefined : lastMinuteData

  const {
    data: sources,
    isLoading: sourcesLoading,
    error: sourcesError,
    mutate: retrySources,
  } = useSources(60_000)
  const {
    data: models,
    isLoading: modelsLoading,
    error: modelsError,
    mutate: retryModels,
  } = useModels(60_000)
  const { data: healthData, error: healthError } = useHealth(15_000)
  const health = healthError ? undefined : healthData

  const healthState = healthError ? 'err' as const : health ? (health.database ? 'ok' as const : 'err' as const) : 'off' as const
  const healthLabel = healthError
    ? '无法获取状态'
    : health
      ? health.database ? '网关运行中' : '服务异常'
      : '检查中…'

  // ---- KPI：仅在成功返回后展示数字，loading/error 不折叠成 0 ----
  const deltaPct =
    today && yesterday && yesterday.requests > 0
      ? ((today.requests - yesterday.requests) / yesterday.requests) * 100
      : null
  const successRate = today ? percent(today.success, today.requests) : '—'

  // ---- 热门模型（按模型聚合，今日窗口） ----
  const {
    data: byModel,
    isLoading: byModelLoading,
    error: byModelError,
    mutate: retryByModel,
  } = useUsageByModel(todayParams)

  const topModels = useMemo(() => {
    const sorted = byModel ?? []
    const top = sorted.slice(0, 5).map((row) => ({ name: row.model || '—', count: row.requests, muted: false }))
    const rest = sorted.slice(5).reduce((sum, row) => sum + row.requests, 0)
    if (rest > 0) top.push({ name: `其他 ${sorted.length - 5} 个模型`, count: rest, muted: true })
    const max = top[0]?.count ?? 1
    return top.map((it) => ({ ...it, ratio: it.count / max }))
  }, [byModel])

  // ---- 最近失败（后端语义状态过滤后的最新 3 条） ----
  const failuresParams = useMemo(() => {
    const now = new Date(minuteTick * 60_000)
    return {
      from: localMidnight(0, now).toISOString(),
      to: now.toISOString(),
      status: 'failed' as const,
      limit: 3,
    }
  }, [minuteTick])
  const {
    data: failuresPage,
    isLoading: failuresLoading,
    error: failuresError,
    mutate: retryFailures,
  } = useUsageLogs(failuresParams)
  const recentFailures = failuresPage?.items ?? []

  const modelStatsBySource = useMemo(() => {
    const map = new Map<string, { total: number; available: number }>()
    for (const model of models ?? []) {
      const sourceId = model.sourceId || ''
      const current = map.get(sourceId) ?? { total: 0, available: 0 }
      current.total += 1
      if (model.available) current.available += 1
      map.set(sourceId, current)
    }
    return map
  }, [models])
  const sourceHealthError = sourcesError ?? modelsError
  const sourceHealthLoading = (sourcesLoading && !sources) || (modelsLoading && !models)

  return (
    <>
      <RoleWatermark className="-right-4 top-4 rail:-right-8" />

      <div className="relative z-[1]">
        <PageHeader
          title="总览"
          description="今日累计、实时速率与近 7 日趋势。"
          actions={
            <button
              type="button"
              onClick={() => navigate('/diagnostics')}
              className="inline-flex -translate-y-1 items-center gap-2 rounded-full border border-transparent px-3 py-1.5 text-xs text-muted-foreground transition-all duration-200 hover:border-border hover:bg-card/90 hover:text-foreground hover:shadow-soft hover:backdrop-blur"
              title="点击查看系统诊断"
            >
              <Dot state={healthState} />
              <span className="font-medium text-foreground">{healthLabel}</span>
              <span className="h-3 w-px bg-border" />
              <span className="tnum font-mono">
                内存 {health ? (health.memory.alloc / 1024 / 1024).toFixed(1) : '—'} MB
              </span>
              <span className="h-3 w-px bg-border" />
              <span className="tnum font-mono">GC {health ? `${health.memory.numGC} 次` : '—'}</span>
              <MoveUpRight className="h-3 w-3 text-muted-foreground/70" />
            </button>
          }
        />

        {/* 今日用量 KPI 五卡 */}
        <section aria-label="今日用量">
          <KpiGrid cols={5}>
            <KpiCard
              label="今日请求"
              value={today ? formatNumber(today.requests) : '—'}
              delta={
                todayError ? (
                  <em className="not-italic">加载失败</em>
                ) : !today ? undefined : yesterdayError ? (
                  <em className="not-italic">昨日对比暂不可用</em>
                ) : !yesterday ? undefined : deltaPct == null ? (
                  <em className="not-italic">— 无昨日基线</em>
                ) : (
                  <>
                    {deltaPct >= 0 ? '▲' : '▼'} {Math.abs(deltaPct).toFixed(1)}% 对昨日
                  </>
                )
              }
              deltaTone={
                todayError || yesterdayError
                  ? 'down'
                  : deltaPct == null
                    ? 'neutral'
                    : deltaPct >= 0
                      ? 'up'
                      : 'down'
              }
            />
            <KpiCard
              label="成功率"
              value={today ? successRate.replace('%', '') : '—'}
              unit={today ? '%' : undefined}
              delta={today ? `成功 ${formatNumber(today.success)} · 失败 ${formatNumber(today.failed)}` : todayError ? '加载失败' : undefined}
              deltaTone={todayError ? 'down' : 'neutral'}
            />
            <KpiCard
              label="Token 总量"
              value={today ? compactNumber(today.totalTokens) : '—'}
              delta={today ? `缓存命中率 ${formatHitRate(today.cacheHitRate)}` : todayError ? '加载失败' : undefined}
              deltaTone={todayError ? 'down' : 'neutral'}
            />
            <KpiCard
              label="平均时延"
              value={today ? formatDuration(today.avgDurationMs) : '—'}
              delta={today ? `平均首字 ${formatDuration(today.avgFirstByteMs)}` : todayError ? '加载失败' : undefined}
              deltaTone={todayError ? 'down' : 'neutral'}
            />
            <KpiCard
              label="实时速率"
              value={lastMinute ? formatNumber(lastMinute.requests) : '—'}
              unit={lastMinute ? 'rpm' : undefined}
              delta={
                lastMinute
                  ? `${compactNumber(lastMinute.totalTokens)} tpm`
                  : lastMinuteError
                    ? '加载失败'
                    : undefined
              }
              deltaTone={lastMinuteError ? 'down' : 'neutral'}
            />
          </KpiGrid>
        </section>

        {/* 趋势图 */}
        <TrendSection minuteTick={minuteTick} />

        {/* 热点与异常三栏 */}
        <section className="border-t border-border pb-2 pt-6" aria-label="热点与异常">
          <div className="grid grid-cols-[repeat(auto-fit,minmax(min(290px,100%),1fr))] gap-9">
            <div>
              <SectionHeader sm title="热门模型" />
              {byModelError ? (
                <ErrorState className="py-8" message={(byModelError as Error).message} onRetry={() => retryByModel()} />
              ) : byModelLoading && !byModel ? (
                <div className="skeleton h-36 rounded-md" />
              ) : topModels.length === 0 ? (
                <p className="py-8 text-center text-sm text-muted-foreground">今日暂无调用</p>
              ) : (
                topModels.map((m) => (
                  <div key={m.name} className="border-b border-dashed border-border py-[9px] last:border-b-0">
                    <div className="flex items-baseline gap-2">
                      <span
                        className={`truncate font-mono text-xs ${m.muted ? 'font-normal text-muted-foreground' : 'font-medium'}`}
                        title={m.name}
                      >
                        {m.name}
                      </span>
                      <span className="tnum ml-auto shrink-0 font-mono text-2xs text-muted-foreground">
                        {formatNumber(m.count)}
                      </span>
                    </div>
                    <div className="mt-1.5 h-1.5 overflow-hidden rounded bg-border">
                      <div
                        className={m.muted ? 'h-full rounded bg-input' : 'h-full rounded bg-brand-grad'}
                        style={{ width: `${Math.max(m.ratio * 100, 2)}%` }}
                      />
                    </div>
                  </div>
                ))
              )}
            </div>

            <div className="min-w-0">
              <SectionHeader
                sm
                title="模型源健康"
                tools={
                  <button
                    onClick={() => navigate('/sources')}
                    className="inline-flex items-center gap-1 text-xs text-muted-foreground transition-colors hover:text-rose"
                  >
                    查看全部 <MoveUpRight className="h-3 w-3" />
                  </button>
                }
              />
              {sourceHealthError ? (
                <ErrorState
                  className="py-8"
                  message={(sourceHealthError as Error).message}
                  onRetry={() => {
                    void Promise.all([retrySources(), retryModels()])
                  }}
                />
              ) : sourceHealthLoading ? (
                <div className="skeleton h-36 rounded-md" />
              ) : (
                <div className="overflow-x-auto">
                  <table className="w-full text-sm">
                    <tbody>
                      {(sources ?? []).slice(0, 6).map((source) => {
                        const stats = modelStatsBySource.get(source.id) ?? { total: 0, available: 0 }
                        const sourceHealth = summarizeSourceHealth(source.enabled, stats.total, stats.available)
                        return (
                          <tr key={source.id} className="border-b border-border last:border-b-0">
                            <td className="py-2 pr-2">
                              <span className="block font-semibold">{source.name}</span>
                              <span className="sub">{source.id}</span>
                            </td>
                            <td className="py-2 pr-2">
                              <PlatformBadge platform={source.platform} />
                            </td>
                            <td className="num py-2 pr-2">{stats.total}</td>
                            <td className="py-2 pl-2">
                              <span
                                className={`inline-flex items-center gap-1.5 text-2xs ${sourceHealth.tone}`}
                                title={`健康状态：${sourceHealth.label}`}
                              >
                                <Dot state={sourceHealth.state} />
                                {sourceHealth.label}
                              </span>
                            </td>
                          </tr>
                        )
                      })}
                      {(sources ?? []).length === 0 && (
                        <tr>
                          <td className="py-8 text-center text-sm text-muted-foreground" colSpan={4}>
                            还没有模型源
                          </td>
                        </tr>
                      )}
                    </tbody>
                  </table>
                </div>
              )}
            </div>

            <div className="min-w-0">
              <SectionHeader
                sm
                title="最近失败"
                tools={
                  <button
                    onClick={() => navigate('/usage-logs')}
                    className="inline-flex items-center gap-1 text-xs text-muted-foreground transition-colors hover:text-rose"
                  >
                    全部日志 <MoveUpRight className="h-3 w-3" />
                  </button>
                }
              />
              {failuresError ? (
                <ErrorState className="py-8" message={(failuresError as Error).message} onRetry={() => retryFailures()} />
              ) : failuresLoading && !failuresPage ? (
                <div className="skeleton h-36 rounded-md" />
              ) : recentFailures.length === 0 ? (
                <p className="py-8 text-center text-sm text-muted-foreground">今日没有失败请求</p>
              ) : (
                recentFailures.map((log) => (
                  <button
                    key={log.requestId}
                    onClick={() => navigate('/usage-logs', { state: { openDetail: log.requestId } })}
                    className="-mx-2.5 mb-px block w-[calc(100%+20px)] rounded-md border-b border-dashed border-border px-2.5 py-[9px] text-left transition-colors last:border-b-0 hover:bg-wash"
                  >
                    <span className="flex items-center gap-[9px]">
                      <span className="shrink-0 font-mono text-2xs text-muted-foreground">
                        {localTimeOf(log.startedAt)}
                      </span>
                      <span className="truncate text-xs font-semibold">{log.modelName || '—'}</span>
                      <span className="ml-auto flex shrink-0">
                        <CodePill code={log.statusCode} />
                      </span>
                    </span>
                    {log.error && (
                      <span className="mt-0.5 block truncate text-xs text-ember">{log.error}</span>
                    )}
                  </button>
                ))
              )}
            </div>
          </div>
        </section>
      </div>
    </>
  )
}

/* ---------------- 趋势图 ---------------- */

/** 生成 0..max 的约 4 档整刻度，让横向网格线减半、更清爽。 */
function ticksFor(values: number[]): number[] {
  const max = Math.max(0, ...values)
  if (max <= 0) return [0]
  const step = Math.max(1, Math.ceil(max / 3))
  return [0, step, step * 2, step * 3]
}

function TrendSection({ minuteTick }: { minuteTick: number }) {
  const [range, setRange] = useState<'7d' | '30d'>('7d')
  const [showReq, setShowReq] = useState(true)
  const [showTok, setShowTok] = useState(true)

  // 后端按日聚合（固定 UTC offset 与浏览器本地日对齐），不受明细 limit 钳制影响
  const params = useMemo(() => {
    const days = range === '7d' ? 7 : 30
    const now = new Date(minuteTick * 60_000)
    const from = localMidnight(-(days - 1), now)
    return { from: from.toISOString(), utcOffsetMinutes: -now.getTimezoneOffset() }
  }, [range, minuteTick])
  const { data: buckets, isLoading, error, mutate } = useUsageTrend(params)

  // 补齐空白天，让刻度连续；label 用本地日期
  const series = useMemo(() => {
    const byDay = new Map((buckets ?? []).map((b) => [b.date, b]))
    const days = range === '7d' ? 7 : 30
    const now = new Date(minuteTick * 60_000)
    const out: { date: string; label: string; req: number; tok: number }[] = []
    for (let i = days - 1; i >= 0; i--) {
      const key = localDateKey(localMidnight(-i, now))
      const b = byDay.get(key)
      out.push({
        date: key,
        label: key.slice(5).replace('-', '/'),
        req: b?.requests ?? 0,
        tok: b?.tokens ?? 0,
      })
    }
    return out
  }, [buckets, range, minuteTick])

  const last = series.length > 0 ? series[series.length - 1] : null

  return (
    <section className="border-t border-border pb-8 pt-6" aria-label="请求与 Token 趋势">
      <SectionHeader
        title="请求与 Token 趋势"
        tools={
          <>
            <Fchip label="请求数" color="var(--rose)" active={showReq} onClick={() => setShowReq(!showReq)} />
            <Fchip label="Tokens" color="var(--jade)" active={showTok} onClick={() => setShowTok(!showTok)} />
            <Seg
              aria-label="时间范围"
              options={[
                { value: '7d', label: '近 7 天' },
                { value: '30d', label: '近 30 天' },
              ]}
              value={range}
              onChange={setRange}
            />
          </>
        }
      />
      <div className="h-[300px] w-full">
        {error ? (
          <ErrorState className="h-full py-8" message={(error as Error).message} onRetry={() => mutate()} />
        ) : isLoading && !buckets ? (
          <div className="skeleton h-full w-full rounded-md" />
        ) : (
          <ResponsiveContainer width="100%" height="100%">
            <ComposedChart data={series} margin={{ top: 12, right: 46, bottom: 0, left: 4 }}>
              <CartesianGrid stroke="var(--border)" strokeDasharray="1 5" vertical={false} />
              <XAxis
                dataKey="label"
                tick={CHART_TICK}
                tickLine={false}
                axisLine={{ stroke: 'var(--border)' }}
                interval="preserveStartEnd"
                minTickGap={24}
              />
              {/* 左轴 Tokens，右轴请求数（次数刻度） */}
              <YAxis
                yAxisId="tok"
                tick={CHART_TICK}
                tickLine={false}
                axisLine={false}
                width={52}
                ticks={ticksFor(series.map((d) => d.tok))}
                tickFormatter={(v: number) => compactNumber(v)}
              />
              <YAxis
                yAxisId="req"
                orientation="right"
                tick={CHART_TICK}
                tickLine={false}
                axisLine={false}
                width={40}
                allowDecimals={false}
              />
              <Tooltip content={<TrendTooltip />} cursor={{ fill: 'var(--wash)' }} />
              {showReq && (
                <Bar
                  yAxisId="req"
                  dataKey="req"
                  name="请求数"
                  fill="var(--rose)"
                  fillOpacity={0.18}
                  radius={[3, 3, 0, 0]}
                  maxBarSize={20}
                />
              )}
              {showTok && (
                <Line
                  yAxisId="tok"
                  dataKey="tok"
                  name="Tokens"
                  type="monotone"
                  stroke="var(--jade)"
                  strokeWidth={2.2}
                  dot={false}
                  activeDot={{ r: 3, fill: 'var(--jade)', strokeWidth: 0 }}
                />
              )}
              {showTok && last && last.tok > 0 && (
                <ReferenceDot
                  yAxisId="tok"
                  x={last.label}
                  y={last.tok}
                  r={3}
                  fill="var(--jade)"
                  stroke="none"
                  label={{
                    value: compactNumber(last.tok),
                    position: 'right',
                    fill: 'var(--jade)',
                    fontSize: CHART_TICK.fontSize,
                    fontFamily: CHART_TICK.fontFamily,
                  }}
                />
              )}
            </ComposedChart>
          </ResponsiveContainer>
        )}
      </div>
    </section>
  )
}

function TrendTooltip({ active, payload, label }: {
  active?: boolean
  payload?: { name?: string; value?: number | string; color?: string }[]
  label?: string
}) {
  if (!active || !payload?.length) return null
  return (
    <div className="rounded-[7px] border border-input bg-card px-[11px] py-2 text-xs shadow-soft tnum">
      <p className="font-mono text-2xs text-muted-foreground">{label}</p>
      {payload.map((entry) => (
        <p key={entry.name} className="flex items-center gap-[7px] leading-[1.7]">
          <i className="h-2 w-2 rounded-[2px]" style={{ background: entry.color }} aria-hidden />
          {entry.name} <b className="ml-auto pl-3 font-semibold">{formatNumber(Number(entry.value))}</b>
        </p>
      ))}
    </div>
  )
}
