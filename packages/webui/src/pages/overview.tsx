import { useLayoutEffect, useMemo, useRef, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import {
  Area,
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
import {
  Activity,
  ArrowUpRight,
  Boxes,
  CheckCircle2,
  Clock,
  Coins,
  Cpu,
  Flame,
  MoveUpRight,
  Radio,
  ShieldAlert,
} from 'lucide-react'
import { PageHeader } from '@/components/page-header'
import { KpiCard, KpiGrid } from '@/components/kpi-card'
import { ElysiaStage } from '@/components/role-watermark'
import { Fchip } from '@/components/ui/fchip'
import { Seg } from '@/components/ui/seg'
import { RANGE_OPTIONS } from '@/components/usage-filter-bar'
import { ErrorState } from '@/components/ui/states'
import { CodePill, Dot, PlatformBadge } from '@/components/badges'
import { ModelBreakdownTooltip } from '@/components/model-breakdown-tooltip'
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
import type { ModelSource } from '@/lib/types'
import { bucketedTimeISO, CHART_TICK, compactNumber, formatDuration, formatHitRate, formatNumber, percent, startOfRange } from '@/lib/utils'
import type { RangeKey } from '@/components/usage-filter-bar'

/** 本地零点（今日 / 昨日），用于 KPI 的"今日 vs 昨日"窗口。 */
function localMidnight(offsetDays = 0, reference = new Date()): Date {
  const d = new Date(reference)
  d.setHours(0, 0, 0, 0)
  d.setDate(d.getDate() + offsetDays)
  return d
}

const DAY_MS = 86_400_000

/** 第 dayOffset 天（0=今天）的桶起点时间戳（负值=往过去偏移）。 */
function offsetDayStart(ts: number, offsetMinutes: number, dayOffset = 0): number {
  const shifted = ts + offsetMinutes * 60_000
  return (Math.floor(shifted / DAY_MS) + dayOffset) * DAY_MS - offsetMinutes * 60_000
}

/** 时间戳在固定 offset 分桶下的 YYYY-MM-DD（与后端 date() 输出同构）。 */
function offsetDayKey(ts: number, offsetMinutes: number): string {
  const shifted = new Date(ts + offsetMinutes * 60_000)
  return `${shifted.getUTCFullYear()}-${String(shifted.getUTCMonth() + 1).padStart(2, '0')}-${String(shifted.getUTCDate()).padStart(2, '0')}`
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

/**
 * 源健康滚动列表：源数超过可视行数（5）时自动垂直滚动轮播展示全部源，
 * 鼠标悬停暂停；点击某一行携带 openSource 状态跳转到模型源页，由该页
 * 自动展开并定位到对应源的配置。
 */
function SourceHealthScroller({
  sources,
  modelStatsBySource,
}: {
  sources: ModelSource[]
  modelStatsBySource: Map<string, { total: number; available: number }>
}) {
  const navigate = useNavigate()
  const viewportRef = useRef<HTMLDivElement>(null)
  const contentRef = useRef<HTMLDivElement>(null)
  // 仅当内容真实超出可用高度（由同排最高的相邻卡片决定）时才滚动，否则静态
  // 展示全部行。ResizeObserver 兜底容器/行高变化（字体加载、响应式重排）。
  const [scrolling, setScrolling] = useState(false)
  const rows = sources.map((source) => {
    const stats = modelStatsBySource.get(source.id) ?? { total: 0, available: 0 }
    return { source, stats, health: summarizeSourceHealth(source.enabled, stats.total, stats.available) }
  })

  useLayoutEffect(() => {
    const viewport = viewportRef.current
    const content = contentRef.current
    if (!viewport || !content) return
    const measure = () => {
      // 静态态比较单份内容高度；滚动态内容为双份，取一半高度比较，判定单调稳定。
      const contentHeight = content.scrollHeight / (scrolling ? 2 : 1)
      setScrolling(contentHeight > viewport.clientHeight + 1)
    }
    measure()
    const observer = new ResizeObserver(measure)
    observer.observe(viewport)
    observer.observe(content)
    return () => observer.disconnect()
  }, [rows.length, scrolling])

  // 滚动时长按行数缩放（每行约 3.05s，比初版放慢 15%），观感均匀。
  const durationSeconds = Math.max(14, Math.round(rows.length * 3.05))
  const renderRow = ({ source, stats, health }: (typeof rows)[number], keyPrefix: string) => (
    <tr
      key={`${keyPrefix}-${source.id}`}
      onClick={() => navigate('/sources', { state: { openSource: source.id } })}
      title={`查看「${source.name}」配置`}
      className="cursor-pointer border-b border-border/30 last:border-b-0 hover:bg-wash/30 transition-colors"
    >
      <td className="py-2.5 pr-2">
        <span className="block font-medium text-foreground">{source.name}</span>
        <span className="sub text-2xs">{source.id}</span>
      </td>
      <td className="py-2.5 pr-2">
        <PlatformBadge platform={source.platform} />
      </td>
      <td className="num py-2.5 pr-2 text-right">{stats.total}</td>
      <td className="py-2.5 pl-2 text-right">
        <span className={`inline-flex items-center gap-1.5 text-2xs ${health.tone}`}>
          <Dot state={health.state} />
          {health.label}
        </span>
      </td>
    </tr>
  )

  if (rows.length === 0) {
    return <p className="py-8 text-center text-xs text-muted-foreground">暂无模型源</p>
  }

  return (
    // 绝对定位让内容不撑高卡片：可用高度完全由同排相邻卡片决定，「能否展示
    // 全部」的判定才不会自我满足（内容撑开容器 → 永远"装得下"）。
    <div ref={viewportRef} className="group/scroller absolute inset-0 overflow-hidden pt-1" title={scrolling ? '悬停暂停滚动' : undefined}>
      <div
        ref={contentRef}
        className={scrolling ? 'animate-source-marquee group-hover/scroller:[animation-play-state:paused]' : undefined}
        style={scrolling ? { animationDuration: `${durationSeconds}s` } : undefined}
      >
        <table className="w-full text-xs">
          <tbody>
            {rows.map((row) => renderRow(row, scrolling ? 'a' : 'static'))}
            {/* 滚动态复制一份内容实现无缝循环（keys 加前缀避免重复）。 */}
            {scrolling && rows.map((row) => renderRow(row, 'b'))}
          </tbody>
        </table>
      </div>
    </div>
  )
}

export function OverviewPage() {
  const minuteTick = useMinuteTick()
  const navigate = useNavigate()

  // 今日 / 昨日窗口（今日的 to 按 5 分钟桶量化：缓存键在桶内稳定，空闲时
  // 不再每分钟重拉全窗聚合；最近一分钟 KPI 保持分钟粒度单独请求）
  const todayParams = useMemo(() => {
    const to = bucketedTimeISO(minuteTick * 60_000, 5 * 60_000)
    return { from: localMidnight(0, new Date(to)).toISOString(), to }
  }, [minuteTick])

  const yesterdayParams = useMemo(() => {
    const now = new Date(minuteTick * 60_000)
    return {
      from: localMidnight(-1, now).toISOString(),
      to: localMidnight(0, now).toISOString(),
    }
  }, [minuteTick])

  const { data: todayData, error: todayError } = useUsageStats(todayParams)
  const { data: yesterdayData, error: yesterdayError } = useUsageStats(yesterdayParams)
  const today = todayError ? undefined : todayData
  const yesterday = yesterdayError ? undefined : yesterdayData

  // 上一完整分钟 [from, to)
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

  const healthState = healthError ? ('err' as const) : health ? (health.database ? ('ok' as const) : ('err' as const)) : ('off' as const)
  const healthLabel = healthError
    ? '无法获取状态'
    : health
      ? health.database ? '网关就绪' : '服务异常'
      : '检查中…'

  // KPI 计算
  const deltaPct =
    today && yesterday && yesterday.requests > 0
      ? ((today.requests - yesterday.requests) / yesterday.requests) * 100
      : null
  const successRate = today ? percent(today.success, today.requests) : '—'

  // 热门模型分布：独立时间窗（右上角 Seg 快捷切换 24小时/7天/30天/全部），
  // to 沿用 5 分钟桶化保持缓存键稳定；与 KPI 的今日统计互不影响。
  const [topRange, setTopRange] = useState<RangeKey>('24h')
  const topModelsParams = useMemo(() => {
    const to = bucketedTimeISO(minuteTick * 60_000, 5 * 60_000)
    return { from: startOfRange(topRange, to), to }
  }, [topRange, minuteTick])
  const {
    data: byModel,
    isLoading: byModelLoading,
    error: byModelError,
    mutate: retryByModel,
  } = useUsageByModel(topModelsParams)

  const topModels = useMemo(() => {
    const sorted = byModel ?? []
    const top = sorted.slice(0, 5).map((row) => ({ name: row.model || '—', count: row.requests, muted: false }))
    const rest = sorted.slice(5).reduce((sum, row) => sum + row.requests, 0)
    if (rest > 0) top.push({ name: `其他 ${sorted.length - 5} 个模型`, count: rest, muted: true })
    const max = top[0]?.count || 1
    return top.map((it) => ({ ...it, ratio: it.count / max }))
  }, [byModel])

  // 最近失败（最新 3 条，今日窗口、与 KPI 相同的 5 分钟桶化）
  const failuresParams = useMemo(() => {
    const to = bucketedTimeISO(minuteTick * 60_000, 5 * 60_000)
    return {
      from: localMidnight(0, new Date(to)).toISOString(),
      to,
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

  // 角色光环状态共鸣
  const stageStatus =
    healthState === 'err'
      ? 'err'
      : lastMinute && lastMinute.requests > 0
        ? 'active'
        : 'ok'

  return (
    <>
      {/* 爱莉希雅视觉中枢立绘舞台 */}
      <ElysiaStage statusState={stageStatus} className="-right-6 -top-4 rail:-right-10" />

      <div className="relative z-[1] space-y-7">
        <PageHeader
          title="总览"
          actions={
            // 诊断胶囊：默认隐身（仅文字与指标裸排，与标题对齐），hover 时过渡出
            // 胶囊壳（边框/底色/阴影/毛玻璃）与右上箭头。
            <button
              type="button"
              onClick={() => navigate('/diagnostics')}
              className="group inline-flex items-center gap-1.5 rounded-full border border-transparent bg-transparent py-1.5 pl-1 pr-2.5 text-xs text-muted-foreground transition-all duration-200 hover:border-border/70 hover:bg-card/80 hover:shadow-soft hover:backdrop-blur-md"
              title="点击查看系统诊断"
            >
              <Dot state={healthState} />
              <span className="font-semibold text-foreground">{healthLabel}</span>
              <span className="h-3 w-px bg-border/80" />
              <span className="tnum font-mono text-muted-foreground">
                堆内存 {health ? (health.memory.alloc / 1024 / 1024).toFixed(1) : '—'} MB
              </span>
              <span className="h-3 w-px bg-border/80" />
              <span className="tnum font-mono text-muted-foreground">GC {health ? `${health.memory.numGC} 次` : '—'}</span>
              <ArrowUpRight className="h-3.5 w-3.5 text-muted-foreground/0 transition-all duration-200 group-hover:translate-x-0.5 group-hover:-translate-y-0.5 group-hover:text-muted-foreground/70" />
            </button>
          }
        />

        {/* 核心 KPI 指标区（无界灵动数据锚点） */}
        <section aria-label="今日核心用量">
          <KpiGrid>
            {/* HERO 主卡片：今日请求数 */}
            <KpiCard
              variant="hero"
              label="今日请求总数"
              value={today ? formatNumber(today.requests) : '—'}
              icon={<Flame className="h-4 w-4 text-rose animate-pulse" />}
              delta={
                todayError ? (
                  <em className="not-italic">加载失败</em>
                ) : !today ? undefined : yesterdayError ? (
                  <em className="not-italic">昨日对比暂不可用</em>
                ) : !yesterday ? undefined : deltaPct == null ? (
                  <em className="not-italic">— 无昨日基线</em>
                ) : (
                  <>
                    <span>{deltaPct >= 0 ? '▲' : '▼'} {Math.abs(deltaPct).toFixed(1)}%</span>
                    <span className="text-muted-foreground/70">对比昨日</span>
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

            {/* 支撑指标 1：请求成功率 */}
            <KpiCard
              label="成功率"
              value={today ? successRate.replace('%', '') : '—'}
              unit={today ? '%' : undefined}
              icon={<CheckCircle2 className="h-4 w-4 text-jade" />}
              delta={today ? `成功 ${formatNumber(today.success)} · 失败 ${formatNumber(today.failed)}` : todayError ? '加载失败' : undefined}
              deltaTone={todayError ? 'down' : 'neutral'}
            />

            {/* 支撑指标 2：Token 吞吐 */}
            <KpiCard
              label="Token 总消耗"
              value={today ? compactNumber(today.totalTokens) : '—'}
              icon={<Coins className="h-4 w-4 text-amber" />}
              delta={today ? `缓存命中 ${formatHitRate(today.cacheHitRate)}` : todayError ? '加载失败' : undefined}
              deltaTone={todayError ? 'down' : 'neutral'}
            />

            {/* 支撑指标 3：时延表现 */}
            <KpiCard
              label="平均请求时延"
              value={today ? formatDuration(today.avgDurationMs) : '—'}
              icon={<Clock className="h-4 w-4 text-muted-foreground" />}
              delta={today ? `首字 ${formatDuration(today.avgFirstByteMs)}` : todayError ? '加载失败' : undefined}
              deltaTone={todayError ? 'down' : 'neutral'}
            />

            {/* 支撑指标 4：实时速率 */}
            <KpiCard
              label="实时速率"
              value={lastMinute ? formatNumber(lastMinute.requests) : '—'}
              unit={lastMinute ? 'rpm' : undefined}
              icon={<Radio className="h-4 w-4 text-primary animate-pulse" />}
              delta={
                lastMinute
                  ? `${compactNumber(lastMinute.totalTokens)} tpm`
                  : lastMinuteError
                    ? '加载失败'
                    : '静止'
              }
              deltaTone={lastMinuteError ? 'down' : 'neutral'}
            />
          </KpiGrid>
        </section>

        {/* 趋势图表区：双流引擎 (请求折线图 + Token柱状图悬停模型明细) */}
        <TrendSection minuteTick={minuteTick} />

        {/* 底部 Bento 三栏面板：开放式流式栅格 */}
        <section aria-label="服务状态与热点" className="grid grid-cols-1 gap-8 md:grid-cols-2 lg:grid-cols-3 pt-2">
          {/* Bento 1: 热门模型 */}
          <div className="space-y-4">
            <div className="flex items-center justify-between border-b border-border/50 pb-3">
              <div className="flex items-center gap-2">
                <Cpu className="h-4 w-4 text-primary" />
                <h3 className="text-sm font-semibold text-foreground">热门模型分布</h3>
              </div>
              <Seg
                aria-label="热门模型时间窗"
                className="h-7"
                options={RANGE_OPTIONS}
                value={topRange}
                onChange={setTopRange}
              />
            </div>
            <div className="pt-1">
              {byModelError ? (
                <ErrorState className="py-6" message={(byModelError as Error).message} onRetry={() => retryByModel()} />
              ) : byModelLoading && !byModel ? (
                <div className="skeleton h-36 rounded-md" />
              ) : topModels.length === 0 ? (
                <p className="py-8 text-center text-xs text-muted-foreground">所选时间窗内暂无调用数据</p>
              ) : (
                <div className="space-y-3.5">
                  {topModels.map((m, idx) => (
                    <div key={m.name} className="space-y-1.5">
                      <div className="flex items-center justify-between gap-2 text-xs">
                        <div className="flex items-center gap-1.5 truncate">
                          <span className="flex h-4 w-4 shrink-0 items-center justify-center rounded bg-secondary/80 text-2xs font-mono font-medium text-muted-foreground">
                            {idx + 1}
                          </span>
                          <span className={`truncate font-mono ${m.muted ? 'text-muted-foreground' : 'font-medium text-foreground'}`} title={m.name}>
                            {m.name}
                          </span>
                        </div>
                        <span className="tnum font-mono text-2xs font-medium text-muted-foreground">
                          {formatNumber(m.count)}
                        </span>
                      </div>
                      <div className="h-1.5 w-full overflow-hidden rounded-full bg-secondary/70">
                        <div
                          className={m.muted ? 'h-full rounded-full bg-muted-foreground/40' : 'h-full rounded-full bg-brand-grad'}
                          style={{ width: `${Math.max(m.ratio * 100, 3)}%` }}
                        />
                      </div>
                    </div>
                  ))}
                </div>
              )}
            </div>
          </div>

          {/* Bento 2: 模型源健康状态 */}
          <div className="flex h-full flex-col space-y-4">
            <div className="flex items-center justify-between border-b border-border/50 pb-3">
              <div className="flex items-center gap-2">
                <Boxes className="h-4 w-4 text-jade" />
                <h3 className="text-sm font-semibold text-foreground">模型源健康</h3>
              </div>
              <button
                onClick={() => navigate('/sources')}
                className="group inline-flex items-center gap-1 text-xs text-muted-foreground transition-colors hover:text-rose"
              >
                全部 <MoveUpRight className="h-3 w-3 transition-transform duration-200 group-hover:translate-x-0.5 group-hover:-translate-y-0.5" />
              </button>
            </div>
            {sourceHealthError ? (
              <div className="pt-1">
                <ErrorState
                  className="py-6"
                  message={(sourceHealthError as Error).message}
                  onRetry={() => {
                    void Promise.all([retrySources(), retryModels()])
                  }}
                />
              </div>
            ) : sourceHealthLoading ? (
              <div className="pt-1">
                <div className="skeleton h-36 rounded-md" />
              </div>
            ) : (
              /* 高度由同排最高的相邻卡片拉伸决定（单列布局时 min-h 兜底）；
                 滚动器绝对定位其中，据实测溢出决定静态展示还是滚动。 */
              <div className="relative min-h-[12.5rem] flex-1">
                <SourceHealthScroller sources={sources ?? []} modelStatsBySource={modelStatsBySource} />
              </div>
            )}
          </div>

          {/* Bento 3: 最近失败调用 */}
          <div className="space-y-4 md:col-span-2 lg:col-span-1">
            <div className="flex items-center justify-between border-b border-border/50 pb-3">
              <div className="flex items-center gap-2">
                <ShieldAlert className="h-4 w-4 text-ember" />
                <h3 className="text-sm font-semibold text-foreground">最近异常调用</h3>
              </div>
              <button
                onClick={() => navigate('/usage-logs')}
                className="group inline-flex items-center gap-1 text-xs text-muted-foreground transition-colors hover:text-rose"
              >
                日志 <MoveUpRight className="h-3 w-3 transition-transform duration-200 group-hover:translate-x-0.5 group-hover:-translate-y-0.5" />
              </button>
            </div>
            <div className="pt-1">
              {failuresError ? (
                <ErrorState className="py-6" message={(failuresError as Error).message} onRetry={() => retryFailures()} />
              ) : failuresLoading && !failuresPage ? (
                <div className="skeleton h-36 rounded-md" />
              ) : recentFailures.length === 0 ? (
                <div className="flex flex-col items-center justify-center py-8 text-center text-xs text-muted-foreground">
                  <CheckCircle2 className="h-8 w-8 text-jade/70 mb-2" />
                  <span>近期无任何调用异常记录</span>
                </div>
              ) : (
                <div className="space-y-2">
                  {recentFailures.map((item) => (
                    <button
                      key={item.requestId}
                      onClick={() => navigate('/usage-logs', { state: { openDetail: item.requestId } })}
                      className="group flex w-full flex-col justify-between gap-1.5 border-b border-border/30 pb-2.5 last:border-b-0 text-left hover:bg-wash/30 p-1.5 rounded transition-colors"
                    >
                      <div className="flex items-center justify-between gap-2 text-xs">
                        <div className="flex items-center gap-1.5 truncate">
                          <CodePill code={item.statusCode} />
                          <span className="font-mono text-2xs font-medium text-foreground truncate">
                            {item.modelName || '—'}
                          </span>
                        </div>
                        <span className="font-mono text-2xs text-muted-foreground/70 shrink-0">
                          {localTimeOf(item.startedAt)}
                        </span>
                      </div>
                      {item.error && (
                        <p className="text-2xs text-ember/90 truncate font-mono" title={item.error}>
                          {item.error}
                        </p>
                      )}
                    </button>
                  ))}
                </div>
              )}
            </div>
          </div>
        </section>
      </div>
    </>
  )
}

/* ---------------- 双流趋势图表引擎 (请求折线图 + Token 柱状图 + 模型悬停明细) ---------------- */

function ticksFor(values: number[]): number[] {
  const max = Math.max(0, ...values)
  if (max <= 0) return [0]
  const step = Math.max(1, Math.ceil(max / 3))
  return [0, step, step * 2, step * 3]
}

function TrendSection({ minuteTick }: { minuteTick: number }) {
  const [range, setRange] = useState<'7d' | '30d'>('7d')
  const [showReqLine, setShowReqLine] = useState(true)
  const [showTokBar, setShowTokBar] = useState(true)

  const params = useMemo(() => {
    const days = range === '7d' ? 7 : 30
    const now = new Date(minuteTick * 60_000)
    const offsetMinutes = -now.getTimezoneOffset()
    const from = offsetDayStart(now.getTime(), offsetMinutes, -(days - 1))
    return { from: new Date(from).toISOString(), utcOffsetMinutes: offsetMinutes }
  }, [range, minuteTick])

  const { data: buckets, isLoading, error, mutate } = useUsageTrend(params)

  const series = useMemo(() => {
    const byDay = new Map((buckets ?? []).map((b) => [b.date, b]))
    const days = range === '7d' ? 7 : 30
    const now = new Date(minuteTick * 60_000)
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
        successReq: b?.successRequests ?? b?.requests ?? 0,
        failedReq: b?.failedRequests ?? 0,
        modelTokens: b?.modelTokens,
      })
    }
    return out
  }, [buckets, range, minuteTick])

  const last = series.length > 0 ? series[series.length - 1] : null

  return (
    <section className="space-y-4 pt-1">
      <div className="flex flex-row flex-wrap items-center justify-between gap-4 border-b border-border/50 pb-3.5">
        <div className="flex items-center gap-2">
          <Activity className="h-4 w-4 text-primary" />
          <h2 className="text-sm font-semibold text-foreground">请求流与 Token 消耗趋势</h2>
        </div>
        <div className="flex flex-wrap items-center gap-2.5">
          <Fchip label="请求折线" color="var(--rose)" active={showReqLine} onClick={() => setShowReqLine(!showReqLine)} />
          <Fchip label="Token 柱图" color="var(--jade)" active={showTokBar} onClick={() => setShowTokBar(!showTokBar)} />
          <Seg
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

      <div className="pt-2">
        <div className="h-[290px] w-full">
          {error ? (
            <ErrorState className="h-full py-8" message={(error as Error).message} onRetry={() => mutate()} />
          ) : isLoading && !buckets ? (
            <div className="skeleton h-full w-full rounded-md" />
          ) : (
            <ResponsiveContainer width="100%" height="100%">
              <ComposedChart data={series} margin={{ top: 12, right: 36, bottom: 0, left: 0 }}>
                <defs>
                  {/* 请求折线平滑光芒区域渐变 */}
                  <linearGradient id="reqAreaGrad" x1="0" y1="0" x2="0" y2="1">
                    <stop offset="5%" stopColor="var(--rose)" stopOpacity={0.18} />
                    <stop offset="95%" stopColor="var(--rose)" stopOpacity={0.0} />
                  </linearGradient>
                </defs>
                <CartesianGrid stroke="hsl(var(--border) / 0.45)" strokeDasharray="3 3" vertical={false} />
                <XAxis
                  dataKey="label"
                  tick={CHART_TICK}
                  tickLine={false}
                  axisLine={{ stroke: 'hsl(var(--border) / 0.6)' }}
                  interval="preserveStartEnd"
                  minTickGap={24}
                />
                <YAxis
                  yAxisId="tok"
                  tick={{ ...CHART_TICK, fill: 'var(--jade)' }}
                  tickLine={false}
                  axisLine={false}
                  width={52}
                  ticks={ticksFor(series.map((d) => d.tok))}
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

                {/* Token 消耗柱状图（柱子悬停展示模型细分） */}
                {showTokBar && (
                  <Bar
                    yAxisId="tok"
                    dataKey="tok"
                    name="Token 消耗"
                    fill="var(--jade)"
                    fillOpacity={0.45}
                    radius={[4, 4, 0, 0]}
                    maxBarSize={24}
                  />
                )}

                {/* 请求数平滑折线与微光面积 */}
                {showReqLine && (
                  <>
                    <Area
                      yAxisId="req"
                      dataKey="req"
                      type="monotone"
                      fill="url(#reqAreaGrad)"
                      stroke="none"
                      tooltipType="none"
                    />
                    <Line
                      yAxisId="req"
                      dataKey="req"
                      name="请求数"
                      type="monotone"
                      stroke="var(--rose)"
                      strokeWidth={2.4}
                      dot={false}
                      activeDot={{ r: 4.5, fill: 'var(--rose)', stroke: 'hsl(var(--card))', strokeWidth: 2 }}
                    />
                  </>
                )}

                {/* 终点锚点标记 */}
                {showReqLine && last && last.req > 0 && (
                  <ReferenceDot
                    yAxisId="req"
                    x={last.label}
                    y={last.req}
                    r={3.5}
                    fill="var(--rose)"
                    stroke="none"
                    label={{
                      value: formatNumber(last.req),
                      position: 'top',
                      fill: 'var(--rose)',
                      fontSize: CHART_TICK.fontSize,
                      fontFamily: CHART_TICK.fontFamily,
                    }}
                  />
                )}
              </ComposedChart>
            </ResponsiveContainer>
          )}
        </div>
      </div>
    </section>
  )
}
