import { useMemo } from 'react'
import { Link } from 'react-router-dom'
import {
  Activity,
  ArrowRight,
  Boxes,
  CheckCircle2,
  Cpu,
  Database,
  Gauge,
  Layers,
  ScrollText,
  Server,
  Terminal,
  Timer,
  XCircle,
} from 'lucide-react'
import { PageHeader } from '@/components/page-header'
import { StatCard } from '@/components/stat-card'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { useHealth, useUsageStats, useSources, useModels, useGroups } from '@/lib/hooks'
import {
  compactNumber,
  formatBytes,
  formatDuration,
  formatNumber,
  percent,
  ratePerMinute,
  startOfRange,
  toRFC3339,
} from '@/lib/utils'

export function OverviewPage() {
  const { data: health, isLoading: healthLoading } = useHealth(15000)
  // 必须 memo：否则 toRFC3339(new Date()) 每次渲染都生成新时间戳，
  // 导致 useUsageStats 的 SWR key 每帧都变、永远重新请求、卡在 loading。
  const usageParams = useMemo(
    () => ({ from: startOfRange('7d'), to: toRFC3339(new Date()) }),
    [],
  )
  const { data: stats, isLoading: statsLoading } = useUsageStats(usageParams)
  const { data: sources, isLoading: sourcesLoading } = useSources()
  const { data: models, isLoading: modelsLoading } = useModels()
  const { data: groups, isLoading: groupsLoading } = useGroups()

  const successRate = stats ? percent(stats.success, stats.requests) : '—'
  // 近 7 天窗口跨度固定，直接用查询的 from/to 估算每分钟速率。
  const rpm = stats ? ratePerMinute(stats.requests, usageParams.from, usageParams.to) : null
  const tpm = stats ? ratePerMinute(stats.totalTokens, usageParams.from, usageParams.to) : null

  return (
    <div className="space-y-6">
      <PageHeader title="概览" description="后端运行状态与近 7 天用量速览" />

      {/* 聚合规模 */}
      <div className="grid gap-4 sm:grid-cols-3">
        <StatCard
          accent
          label="模型源"
          value={sourcesLoading ? <Skeleton className="h-7 w-12" /> : formatNumber(sources?.length ?? 0)}
          hint="已配置的上游供应商"
          icon={<Server className="h-5 w-5" />}
        />
        <StatCard
          label="聚合模型"
          value={modelsLoading ? <Skeleton className="h-7 w-12" /> : formatNumber(models?.length ?? 0)}
          hint="各源拉取到的可用模型"
          icon={<Boxes className="h-5 w-5" />}
        />
        <StatCard
          label="模型组"
          value={groupsLoading ? <Skeleton className="h-7 w-12" /> : formatNumber(groups?.length ?? 0)}
          hint="对客户端暴露的模型"
          icon={<Layers className="h-5 w-5" />}
        />
      </div>

      {/* 后端状态 */}
      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <StatCard
          accent
          label="后端状态"
          value={
            healthLoading ? (
              <Skeleton className="h-7 w-20" />
            ) : health?.status === 'ok' ? (
              <span className="flex items-center gap-2 text-success">
                <CheckCircle2 className="h-5 w-5" /> 正常
              </span>
            ) : (
              <span className="text-destructive">异常</span>
            )
          }
          icon={<Activity className="h-5 w-5" />}
        />
        <StatCard
          label="SQLite"
          value={
            healthLoading ? (
              <Skeleton className="h-7 w-20" />
            ) : health?.database ? (
              <span className="flex items-center gap-2 text-success">
                <CheckCircle2 className="h-5 w-5" /> 已连接
              </span>
            ) : (
              <span className="flex items-center gap-2 text-destructive">
                <XCircle className="h-5 w-5" /> 不可用
              </span>
            )
          }
          icon={<Database className="h-5 w-5" />}
        />
        <StatCard
          label="内存占用"
          value={healthLoading ? <Skeleton className="h-7 w-24" /> : formatBytes(health?.memory.alloc)}
          hint={health ? `系统保留 ${formatBytes(health.memory.sys)}` : undefined}
          icon={<Cpu className="h-5 w-5" />}
        />
        <StatCard
          label="GC 次数"
          value={healthLoading ? <Skeleton className="h-7 w-16" /> : formatNumber(health?.memory.numGC)}
          hint="自启动以来"
          icon={<Gauge className="h-5 w-5" />}
        />
      </div>

      {/* 用量速览 */}
      <Card>
        <CardHeader className="flex-row items-center justify-between">
          <div>
            <CardTitle>近 7 天用量</CardTitle>
            <CardDescription>请求量、成功率与 token 消耗</CardDescription>
          </div>
          <Button variant="ghost" size="sm" asChild>
            <Link to="/usage">
              查看详情 <ArrowRight className="h-4 w-4" />
            </Link>
          </Button>
        </CardHeader>
        <CardContent>
          <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
            <MiniStat
              loading={statsLoading}
              icon={<CheckCircle2 className="h-4 w-4" />}
              label="成功率"
              value={successRate}
              sub={
                stats
                  ? `成功 ${formatNumber(stats.success)} · 失败 ${formatNumber(stats.failed)} · 共 ${formatNumber(stats.requests)}`
                  : undefined
              }
            />
            <MiniStat
              loading={statsLoading}
              icon={<Cpu className="h-4 w-4" />}
              label="Token 总量"
              value={formatNumber(stats?.totalTokens)}
              sub={stats ? `缓存命中率 ${percent(stats.cacheHitTokens, stats.inputTokens)}` : undefined}
            />
            <MiniStat
              loading={statsLoading}
              icon={<Timer className="h-4 w-4" />}
              label="平均耗时"
              value={formatDuration(stats?.avgDurationMs)}
              sub={stats ? `平均首字 ${formatDuration(stats.avgFirstByteMs)}` : undefined}
            />
            <MiniStat
              loading={statsLoading}
              icon={<Gauge className="h-4 w-4" />}
              label="平均吞吐"
              value={tpm == null ? '—' : `${compactNumber(tpm)} tpm`}
              sub={rpm == null ? undefined : `${rpm.toFixed(rpm < 10 ? 2 : rpm < 100 ? 1 : 0)} rpm`}
            />
          </div>
        </CardContent>
      </Card>

      {/* 快捷入口 */}
      <div className="grid gap-4 sm:grid-cols-3">
        <QuickLink to="/usage-logs" icon={<ScrollText className="h-5 w-5" />} title="Usage 日志" desc="逐条请求明细与详情" />
        <QuickLink to="/logs" icon={<Terminal className="h-5 w-5" />} title="系统日志" desc="刷新、错误等事件" />
        <QuickLink to="/diagnostics" icon={<Activity className="h-5 w-5" />} title="诊断" desc="内存指标与 pprof" />
      </div>
    </div>
  )
}

function MiniStat({
  icon,
  label,
  value,
  sub,
  loading,
}: {
  icon: React.ReactNode
  label: string
  value: string
  sub?: string
  loading?: boolean
}) {
  return (
    <div className="rounded-xl border border-border/70 bg-background/40 p-4">
      <div className="flex items-center gap-2 text-sm text-muted-foreground">
        <span className="text-primary">{icon}</span>
        {label}
      </div>
      {loading ? (
        <Skeleton className="mt-2 h-7 w-24" />
      ) : (
        <p className="mt-1.5 text-xl font-semibold tracking-tight">{value}</p>
      )}
      {sub && !loading && <p className="mt-1 text-xs text-muted-foreground">{sub}</p>}
    </div>
  )
}

function QuickLink({
  to,
  icon,
  title,
  desc,
}: {
  to: string
  icon: React.ReactNode
  title: string
  desc: string
}) {
  return (
    <Link to={to}>
      <Card className="group h-full p-5 transition-all duration-200 hover:-translate-y-0.5 hover:shadow-glow">
        <div className="flex items-center gap-3">
          <span className="flex h-10 w-10 items-center justify-center rounded-xl bg-primary/10 text-primary">
            {icon}
          </span>
          <div className="flex-1">
            <p className="font-medium">{title}</p>
            <p className="text-xs text-muted-foreground">{desc}</p>
          </div>
          <ArrowRight className="h-4 w-4 text-muted-foreground transition-transform group-hover:translate-x-1" />
        </div>
      </Card>
    </Link>
  )
}
