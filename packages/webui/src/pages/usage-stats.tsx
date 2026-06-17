import { useMemo, useState } from 'react'
import { Cell, Pie, PieChart, ResponsiveContainer } from 'recharts'
import { BarChart3, CheckCircle2, Cpu, Gauge, Timer } from 'lucide-react'
import { PageHeader } from '@/components/page-header'
import { StatCard } from '@/components/stat-card'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Label } from '@/components/ui/label'
import { Skeleton } from '@/components/ui/skeleton'
import { ErrorState } from '@/components/ui/states'
import { MultiSelect } from '@/components/ui/multi-select'
import { RangeSelect, type RangeKey } from '@/components/range-select'
import { useUsageStats, useGroups, useModels, useTokens } from '@/lib/hooks'
import {
  compactNumber,
  formatDuration,
  formatNumber,
  percent,
  ratePerMinute,
  startOfRange,
  toRFC3339,
  uniqueSorted,
} from '@/lib/utils'

export function UsageStatsPage() {
  const [range, setRange] = useState<RangeKey>('7d')
  const [groupNames, setGroupNames] = useState<string[]>([])
  const [modelNames, setModelNames] = useState<string[]>([])
  const [keyNames, setKeyNames] = useState<string[]>([])

  const { data: groups } = useGroups()
  const { data: models } = useModels()
  const { data: tokens } = useTokens()

  const groupOptions = useMemo(() => uniqueSorted((groups ?? []).map((g) => g.name)), [groups])
  const modelOptions = useMemo(() => uniqueSorted((models ?? []).map((m) => m.name)), [models])
  const keyOptions = useMemo(() => uniqueSorted((tokens ?? []).map((t) => t.name)), [tokens])

  const params = useMemo(
    () => ({
      from: startOfRange(range),
      to: toRFC3339(new Date()),
      groupNames: groupNames.length ? groupNames : undefined,
      modelNames: modelNames.length ? modelNames : undefined,
      keyNames: keyNames.length ? keyNames : undefined,
    }),
    [range, groupNames, modelNames, keyNames],
  )

  const { data: stats, isLoading, error, mutate } = useUsageStats(params)

  const successRate = percent(stats?.success ?? 0, stats?.requests ?? 0)
  // 范围跨度优先用实际首末调用时间（更贴近真实速率），缺失时回退到查询窗口。
  const spanFrom = stats?.firstUsedAt || params.from
  const spanTo = stats?.lastUsedAt || params.to
  const rpm = stats ? ratePerMinute(stats.requests, spanFrom, spanTo) : null
  const tpm = stats ? ratePerMinute(stats.totalTokens, spanFrom, spanTo) : null

  const pieData = useMemo(
    () => [
      { name: '成功', value: stats?.success ?? 0, color: 'hsl(var(--success))' },
      { name: '失败', value: stats?.failed ?? 0, color: 'hsl(var(--destructive))' },
    ],
    [stats],
  )

  const tokenData = useMemo(
    () => [
      { name: '输入', value: stats?.inputTokens ?? 0, color: 'hsl(var(--primary))' },
      // 输出用高饱和青绿，与输入的粉色形成强区分；缓存命中沿用旧输出的浅粉，区分度更低。
      { name: '输出', value: stats?.outputTokens ?? 0, color: 'hsl(160 84% 45%)' },
      { name: '缓存命中', value: stats?.cacheHitTokens ?? 0, color: 'hsl(330 86% 78%)' },
    ],
    [stats],
  )

  return (
    <div className="space-y-6">
      <PageHeader title="Usage 统计" description="按时间范围与过滤条件汇总的请求与 token 用量" />

      {/* 过滤条件 */}
      <Card className="p-4">
        <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
          <div className="space-y-1.5">
            <Label className="text-xs">时间范围</Label>
            <RangeSelect value={range} onChange={setRange} />
          </div>
          <div className="space-y-1.5">
            <Label className="text-xs">模型组</Label>
            <MultiSelect options={groupOptions} value={groupNames} onChange={setGroupNames} placeholder="全部模型组" searchPlaceholder="搜索模型组" />
          </div>
          <div className="space-y-1.5">
            <Label className="text-xs">模型</Label>
            <MultiSelect options={modelOptions} value={modelNames} onChange={setModelNames} placeholder="全部模型" searchPlaceholder="搜索模型" />
          </div>
          <div className="space-y-1.5">
            <Label className="text-xs">调用方 API Key</Label>
            <MultiSelect options={keyOptions} value={keyNames} onChange={setKeyNames} placeholder="全部调用方" searchPlaceholder="搜索调用方" />
          </div>
        </div>
      </Card>

      {error ? (
        <Card>
          <ErrorState message={(error as Error).message} onRetry={() => mutate()} />
        </Card>
      ) : (
        <>
          <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
            <StatCard
              accent
              label="成功率"
              value={isLoading ? <Skeleton className="h-7 w-20" /> : successRate}
              hint={
                stats
                  ? `成功 ${formatNumber(stats.success)} · 失败 ${formatNumber(stats.failed)} · 共 ${formatNumber(stats.requests)}`
                  : undefined
              }
              icon={<CheckCircle2 className="h-5 w-5" />}
            />
            <StatCard
              label="Token 总量"
              value={isLoading ? <Skeleton className="h-7 w-24" /> : formatNumber(stats?.totalTokens)}
              hint={stats ? `缓存命中率 ${percent(stats.cacheHitTokens, stats.inputTokens)}` : undefined}
              icon={<Cpu className="h-5 w-5" />}
            />
            <StatCard
              label="平均耗时"
              value={isLoading ? <Skeleton className="h-7 w-20" /> : formatDuration(stats?.avgDurationMs)}
              hint={stats ? `平均首字 ${formatDuration(stats.avgFirstByteMs)}` : undefined}
              icon={<Timer className="h-5 w-5" />}
            />
            <StatCard
              label="平均吞吐"
              value={isLoading ? <Skeleton className="h-7 w-20" /> : tpm == null ? '—' : `${compactNumber(tpm)} tpm`}
              hint={rpm == null ? undefined : `${rpm.toFixed(rpm < 10 ? 2 : rpm < 100 ? 1 : 0)} rpm`}
              icon={<Gauge className="h-5 w-5" />}
            />
          </div>

          <div className="grid gap-4 lg:grid-cols-2">
            <ChartCard title="请求成功 / 失败">
              <DonutChart data={pieData} total={stats?.requests ?? 0} centerLabel="请求" />
            </ChartCard>
            <ChartCard title="累计 token 分布">
              <DonutChart data={tokenData} total={stats?.totalTokens ?? 0} centerLabel="Token" />
            </ChartCard>
          </div>
        </>
      )}
    </div>
  )
}

function ChartCard({
  title,
  description,
  children,
}: {
  title: string
  description?: string
  children: React.ReactNode
}) {
  return (
    <Card>
      <CardHeader>
        <CardTitle>{title}</CardTitle>
        {description && <CardDescription>{description}</CardDescription>}
      </CardHeader>
      <CardContent>{children}</CardContent>
    </Card>
  )
}

function DonutChart({
  data,
  total,
  centerLabel,
}: {
  data: { name: string; value: number; color: string }[]
  total: number
  centerLabel: string
}) {
  // 悬停的扇区下标；null 时圆环中央显示总计。鼠标移开圆环即恢复总计。
  const [activeIndex, setActiveIndex] = useState<number | null>(null)
  const hasData = data.some((d) => d.value > 0)
  // 单项数据（只有一个非零扇区）时 paddingAngle=0，让圆环完全闭合；
  // 多项时保留 2° 间隔便于区分扇区。
  const nonZeroCount = data.filter((d) => d.value > 0).length
  const paddingAngle = nonZeroCount <= 1 ? 0 : 2
  const active = activeIndex != null ? data[activeIndex] : null

  return (
    <div className="relative h-64">
      {hasData ? (
        <ResponsiveContainer width="100%" height="100%">
          <PieChart>
            <Pie
              data={data}
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
              {data.map((entry, index) => (
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
              <span className="max-w-[8rem] truncate text-2xl font-semibold tracking-tight" style={{ color: active.color }}>
                {formatNumber(active.value)}
              </span>
              <span className="text-xs text-muted-foreground">
                {active.name} · {percent(active.value, total)}
              </span>
            </>
          ) : (
            <>
              <span className="text-2xl font-semibold tracking-tight">{formatNumber(total)}</span>
              <span className="text-xs text-muted-foreground">{centerLabel}</span>
            </>
          )}
        </div>
      )}
      {hasData && (
        <div className="absolute bottom-0 left-0 right-0 flex justify-center gap-4">
          {data.map((entry, index) => (
            <span
              key={entry.name}
              className="flex cursor-default items-center gap-1.5 text-xs text-muted-foreground transition-opacity"
              style={{ opacity: activeIndex == null || activeIndex === index ? 1 : 0.45 }}
              onMouseEnter={() => setActiveIndex(index)}
              onMouseLeave={() => setActiveIndex(null)}
            >
              <span className="h-2.5 w-2.5 rounded-full" style={{ background: entry.color }} />
              {entry.name} {formatNumber(entry.value)}
            </span>
          ))}
        </div>
      )}
    </div>
  )
}
