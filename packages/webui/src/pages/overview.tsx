import { useLayoutEffect, useMemo, useRef, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import {
  Boxes,
  CheckCircle2,
  Coins,
  Cpu,
  Database,
  Flame,
  MoveUpRight,
  RefreshCcw,
  Server,
  ShieldAlert,
} from 'lucide-react'
import { PageHeader } from '@/components/page-header'
import { KpiCard, KpiGrid } from '@/components/kpi-card'
import { ElysiaStage } from '@/components/role-watermark'
import { Seg } from '@/components/ui/seg'
import { RANGE_OPTIONS } from '@/lib/range-options'
import { ErrorState } from '@/components/ui/states'
import { CodePill, PlatformBadge } from '@/components/badges'
import {
  useHealth,
  useUsageStats,
  useUsageByModel,
  useUsageLogs,
  useSources,
  useModels,
  useMinuteTick,
} from '@/lib/hooks'
import type { ModelSource } from '@/lib/types'
import { bucketedTimeISO, cn, compactNumber, formatHitRate, formatNumber, percent, startOfRange, USAGE_BUCKET_MS } from '@/lib/utils'
import type { RangeKey } from '@/components/usage-filter-bar'
import { TemporalTrendSection } from './overview-trends'
import { PulseSection } from './overview-pulse'

/** 本地零点（今日 / 昨日），用于 KPI 的"今日 vs 昨日"窗口。 */
function localMidnight(offsetDays = 0, reference = new Date()): Date {
  const d = new Date(reference)
  d.setHours(0, 0, 0, 0)
  d.setDate(d.getDate() + offsetDays)
  return d
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
 * 状态胶囊配色：与 CodePill 同一套 color-mix 底色语言（绿=正常 / 红=部分可用 /
 * 灰=停用），后台拉取进行中时覆盖为玫红「拉取中」。
 */
const HEALTH_TONE: Record<'ok' | 'err' | 'off', string> = {
  ok: 'border-[color-mix(in_srgb,var(--jade)_26%,transparent)] bg-[color-mix(in_srgb,var(--jade)_9%,transparent)] text-jade',
  err: 'border-[color-mix(in_srgb,var(--ember)_28%,transparent)] bg-[color-mix(in_srgb,var(--ember)_9%,transparent)] text-ember',
  off: 'border-border bg-secondary/60 text-muted-foreground',
}

/** 源健康表列定义：静态表头与轮播表体共用同一组列宽（table-fixed + colgroup），保证上下对齐。 */
const HEALTH_COLUMNS = [
  { key: 'name', label: '源名称', width: '30%', align: 'left' },
  { key: 'platform', label: '协议类型', width: '30%', align: 'left' },
  { key: 'models', label: '模型', width: '16%', align: 'right' },
  { key: 'status', label: '状态', width: '24%', align: 'right' },
] as const

function HealthColGroup() {
  return (
    <colgroup>
      {HEALTH_COLUMNS.map((col) => (
        <col key={col.key} style={{ width: col.width }} />
      ))}
    </colgroup>
  )
}

/**
 * 源健康滚动列表：源数超过可视行数时自动垂直轮播展示全部源，悬停暂停；
 * 点击某一行携带 openSource 状态跳转到模型源页，由该页自动展开并定位。
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
  const renderRow = ({ source, stats, health }: (typeof rows)[number], keyPrefix: string) => {
    const refreshing = !!source.refreshState?.refreshing
    return (
      <tr
        key={`${keyPrefix}-${source.id}`}
        onClick={() => navigate('/sources', { state: { openSource: source.id } })}
        title={`查看「${source.name}」配置`}
        className="cursor-pointer border-b border-border/30 last:border-b-0 hover:bg-wash/30 transition-colors"
      >
        <td className="overflow-hidden py-3 pr-2">
          <span className="block truncate font-medium text-foreground" title={source.id}>
            {source.name}
          </span>
        </td>
        <td className="overflow-hidden py-3 pr-2">
          <PlatformBadge platform={source.platform} />
        </td>
        <td className="num overflow-hidden py-3 pr-2 text-right font-medium text-foreground">
          {formatNumber(stats.total)}
        </td>
        <td className="overflow-hidden py-3 pl-2 text-right">
          <span
            className={cn(
              'inline-flex min-w-[3.5rem] items-center justify-center gap-1 rounded-[5px] border px-[7px] py-0.5 text-2xs font-medium',
              refreshing ? 'border-rose/30 bg-wash text-rose' : HEALTH_TONE[health.state],
            )}
          >
            {refreshing && <RefreshCcw className="h-2.5 w-2.5 animate-spin" aria-hidden />}
            {refreshing ? '拉取中' : health.label}
          </span>
        </td>
      </tr>
    )
  }

  if (rows.length === 0) {
    return <p className="py-8 text-center text-xs text-muted-foreground">暂无模型源</p>
  }

  return (
    // 绝对定位让内容不撑高卡片：可用高度完全由同排相邻卡片决定，「能否展示
    // 全部」的判定才不会自我满足（内容撑开容器 → 永远"装得下"）。
    <div className="absolute inset-0 flex flex-col">
      {/* 列名：静态表头，不随轮播滚动。首行文字与其他两栏面板的内容起始线对齐。 */}
      <table className="w-full table-fixed text-xs">
        <HealthColGroup />
        <thead>
          <tr className="border-b border-border/40">
            {HEALTH_COLUMNS.map((col) => (
              <th
                key={col.key}
                scope="col"
                className={cn(
                  'pb-2 pt-3 text-2xs font-semibold uppercase tracking-wider text-muted-foreground',
                  col.align === 'right' ? 'text-right' : 'text-left',
                )}
              >
                {col.label}
              </th>
            ))}
          </tr>
        </thead>
      </table>
      <div
        ref={viewportRef}
        className="group/scroller relative min-h-0 flex-1 overflow-hidden"
        title={scrolling ? '悬停暂停滚动' : undefined}
      >
        <div
          ref={contentRef}
          className={cn(scrolling && 'animate-source-marquee group-hover/scroller:[animation-play-state:paused]')}
          style={scrolling ? { animationDuration: `${durationSeconds}s` } : undefined}
        >
          <table className="w-full table-fixed text-xs">
            <HealthColGroup />
            <tbody>
              {rows.map((row) => renderRow(row, scrolling ? 'a' : 'static'))}
              {/* 滚动态复制一份内容实现无缝循环（keys 加前缀避免重复）。 */}
              {scrolling && rows.map((row) => renderRow(row, 'b'))}
            </tbody>
          </table>
        </div>
      </div>
    </div>
  )
}

export function OverviewPage() {
  const minuteTick = useMinuteTick()
  const navigate = useNavigate()

  // 今日 / 昨日窗口（今日 to 取下一 5 分钟边界：键稳定且包含当前桶内新记录）。
  // from 必须用当前时刻算本地日，不能用 ceiled to：临近午夜 to 会落到次日 00:00。
  const todayParams = useMemo(() => {
    const nowMs = minuteTick * 60_000
    const to = bucketedTimeISO(nowMs, USAGE_BUCKET_MS)
    return { from: localMidnight(0, new Date(nowMs)).toISOString(), to }
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

  // KPI 计算
  const deltaPct =
    today && yesterday && yesterday.requests > 0
      ? ((today.requests - yesterday.requests) / yesterday.requests) * 100
      : null
  const successRate = today ? percent(today.success, today.requests) : '—'

  // 热门模型分布：独立时间窗（右上角 Seg 快捷切换 24小时/7天/30天/全部），
  // to 取下一 5 分钟边界，缓存键稳定且包含当前桶内新记录
  const [topRange, setTopRange] = useState<RangeKey>('24h')
  const topModelsParams = useMemo(() => {
    const nowMs = minuteTick * 60_000
    const to = bucketedTimeISO(nowMs, USAGE_BUCKET_MS)
    // 热门模型只反映成功请求，失败调用不计入排名。
    return { from: startOfRange(topRange, new Date(nowMs).toISOString()), to, status: 'success' as const }
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
    return top.map((model) => ({ ...model, ratio: model.count / max }))
  }, [byModel])

  // 最近失败（最新 3 条，今日窗口；to 取下一 5 分钟边界）
  const failuresParams = useMemo(() => {
    const nowMs = minuteTick * 60_000
    const to = bucketedTimeISO(nowMs, USAGE_BUCKET_MS)
    return {
      from: localMidnight(0, new Date(nowMs)).toISOString(),
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
        <PageHeader title="总览" />

        {/* 核心 KPI 指标区（无界灵动数据锚点，精选 6 大核心指标，宽敞大气） */}
        <section aria-label="今日核心用量">
          <KpiGrid cols={6}>
            {/* HERO 主卡片：今日请求数 */}
            <KpiCard
              variant="hero"
              label="今日请求总数"
              value={today ? formatNumber(today.requests) : '—'}
              icon={<Flame className="h-4 w-4 text-rose" />}
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
              delta={today ? `输入 ${compactNumber(today.inputTokens)} · 输出 ${compactNumber(today.outputTokens)}` : todayError ? '加载失败' : undefined}
              deltaTone={todayError ? 'down' : 'neutral'}
            />

            {/* 支撑指标 3：缓存命中率 */}
            <KpiCard
              label="缓存命中率"
              value={today ? formatHitRate(today.cacheHitRate).replace('%', '') : '—'}
              unit={today ? '%' : undefined}
              icon={<Database className="h-4 w-4 text-jade" />}
              delta={today ? `命中 ${compactNumber(today.cacheHitTokens)} tokens` : todayError ? '加载失败' : undefined}
              deltaTone={todayError ? 'down' : 'neutral'}
            />

            {/* 支撑指标 4：内存占用 */}
            <KpiCard
              label="内存占用"
              value={health ? (health.memory.alloc / 1024 / 1024).toFixed(1) : '—'}
              unit={health ? 'MB' : undefined}
              icon={<Server className="h-4 w-4 text-muted-foreground" />}
              delta={healthError ? '加载失败' : health ? `向 OS 申请 ${(health.memory.sys / 1024 / 1024).toFixed(1)} MB` : undefined}
              deltaTone={healthError ? 'down' : 'neutral'}
              title="查看诊断"
              onClick={() => navigate('/diagnostics')}
            />

            {/* 支撑指标 5：GC 次数 */}
            <KpiCard
              label="GC 次数"
              value={health ? formatNumber(health.memory.numGC) : '—'}
              icon={<RefreshCcw className="h-4 w-4 text-muted-foreground" />}
              delta={healthError ? '加载失败' : '进程启动以来'}
              deltaTone={healthError ? 'down' : 'neutral'}
              title="查看诊断"
              onClick={() => navigate('/diagnostics')}
            />
          </KpiGrid>
        </section>

        <PulseSection minuteTick={minuteTick} />

        {/* 时序透视中枢：智能整合请求趋势与模型分流 */}
        <TemporalTrendSection minuteTick={minuteTick} />

        {/* 底部 Bento 三栏面板：开放式流式栅格 */}
        <section aria-label="服务状态与热点" className="grid grid-cols-1 gap-8 md:grid-cols-2 lg:grid-cols-3 pt-2">
          {/* Bento 1: 热门模型 */}
          <div className="space-y-4">
            <div className="flex flex-wrap items-center justify-between gap-x-4 gap-y-2 border-b border-border/50 pb-3">
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
            <div className="pt-3">
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
            <div className="flex flex-wrap items-center justify-between gap-x-4 gap-y-2 border-b border-border/50 pb-3">
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
              <div className="pt-3">
                <ErrorState
                  className="py-6"
                  message={(sourceHealthError as Error).message}
                  onRetry={() => {
                    void Promise.all([retrySources(), retryModels()])
                  }}
                />
              </div>
            ) : sourceHealthLoading ? (
              <div className="pt-3">
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
            <div className="flex flex-wrap items-center justify-between gap-x-4 gap-y-2 border-b border-border/50 pb-3">
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
            <div className="pt-1.5">
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
