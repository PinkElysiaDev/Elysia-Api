import { useMemo, useState } from 'react'
import {
  Cell,
  Pie,
  PieChart,
  ResponsiveContainer,
  Tooltip as RTooltip,
} from 'recharts'
import { BarChart3, CheckCircle2, Cpu, Timer, Zap } from 'lucide-react'
import { PageHeader } from '@/components/page-header'
import { StatCard } from '@/components/stat-card'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Skeleton } from '@/components/ui/skeleton'
import { ErrorState } from '@/components/ui/states'
import { RangeSelect, type RangeKey } from '@/components/range-select'
import { useUsageStats } from '@/lib/hooks'
import { formatDuration, formatNumber, percent, startOfRange, toRFC3339 } from '@/lib/utils'

export function UsageStatsPage() {
  const [range, setRange] = useState<RangeKey>('7d')
  const [groupName, setGroupName] = useState('')
  const [modelName, setModelName] = useState('')
  const [keyName, setKeyName] = useState('')

  const params = useMemo(
    () => ({
      from: startOfRange(range),
      to: toRFC3339(new Date()),
      groupName: groupName.trim() || undefined,
      modelName: modelName.trim() || undefined,
      keyName: keyName.trim() || undefined,
    }),
    [range, groupName, modelName, keyName],
  )

  const { data: stats, isLoading, error, mutate } = useUsageStats(params)

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
      { name: '输出', value: stats?.outputTokens ?? 0, color: 'hsl(330 86% 78%)' },
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
          <FilterInput label="模型组" value={groupName} onChange={setGroupName} placeholder="group name" />
          <FilterInput label="模型" value={modelName} onChange={setModelName} placeholder="model name" />
          <FilterInput label="Key 名称" value={keyName} onChange={setKeyName} placeholder="key name" />
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
              label="请求数"
              value={isLoading ? <Skeleton className="h-7 w-20" /> : formatNumber(stats?.requests)}
              icon={<Zap className="h-5 w-5" />}
            />
            <StatCard
              label="成功率"
              value={
                isLoading ? (
                  <Skeleton className="h-7 w-20" />
                ) : (
                  percent(stats?.success ?? 0, stats?.requests ?? 0)
                )
              }
              hint={stats ? `成功 ${formatNumber(stats.success)} · 失败 ${formatNumber(stats.failed)}` : undefined}
              icon={<CheckCircle2 className="h-5 w-5" />}
            />
            <StatCard
              label="Token 总量"
              value={isLoading ? <Skeleton className="h-7 w-24" /> : formatNumber(stats?.totalTokens)}
              hint={stats ? `入 ${formatNumber(stats.inputTokens)} · 出 ${formatNumber(stats.outputTokens)}` : undefined}
              icon={<Cpu className="h-5 w-5" />}
            />
            <StatCard
              label="平均耗时"
              value={isLoading ? <Skeleton className="h-7 w-20" /> : formatDuration(stats?.avgDurationMs)}
              icon={<Timer className="h-5 w-5" />}
            />
          </div>

          <div className="grid gap-4 lg:grid-cols-2">
            <ChartCard title="请求成功 / 失败" description="状态码 2xx-3xx 视为成功">
              <DonutChart data={pieData} total={stats?.requests ?? 0} centerLabel="请求" />
            </ChartCard>
            <ChartCard title="Token 输入 / 输出" description="累计 token 分布">
              <DonutChart data={tokenData} total={stats?.totalTokens ?? 0} centerLabel="Token" />
            </ChartCard>
          </div>
        </>
      )}
    </div>
  )
}

function FilterInput({
  label,
  value,
  onChange,
  placeholder,
}: {
  label: string
  value: string
  onChange: (value: string) => void
  placeholder?: string
}) {
  return (
    <div className="space-y-1.5">
      <Label className="text-xs">{label}</Label>
      <Input value={value} placeholder={placeholder} onChange={(e) => onChange(e.target.value)} />
    </div>
  )
}

function ChartCard({
  title,
  description,
  children,
}: {
  title: string
  description: string
  children: React.ReactNode
}) {
  return (
    <Card>
      <CardHeader>
        <CardTitle>{title}</CardTitle>
        <CardDescription>{description}</CardDescription>
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
  const hasData = data.some((d) => d.value > 0)
  // 单项数据（只有一个非零扇区）时 paddingAngle=0，让圆环完全闭合；
  // 多项时保留 2° 间隔便于区分扇区。
  const nonZeroCount = data.filter((d) => d.value > 0).length
  const paddingAngle = nonZeroCount <= 1 ? 0 : 2
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
            >
              {data.map((entry) => (
                <Cell key={entry.name} fill={entry.color} />
              ))}
            </Pie>
            <RTooltip
              contentStyle={{
                borderRadius: 12,
                border: '1px solid hsl(var(--border))',
                background: 'hsl(var(--popover))',
                color: 'hsl(var(--popover-foreground))',
                fontSize: 13,
              }}
              formatter={(value: number, name: string) => [formatNumber(value), name]}
            />
          </PieChart>
        </ResponsiveContainer>
      ) : (
        <div className="flex h-full items-center justify-center text-sm text-muted-foreground">
          <BarChart3 className="mr-2 h-4 w-4" /> 暂无数据
        </div>
      )}
      {/* 中心数字仅在有数据时显示，避免与"暂无数据"并列出现 0 */}
      {hasData && (
        <div className="pointer-events-none absolute inset-0 flex flex-col items-center justify-center">
          <span className="text-2xl font-semibold tracking-tight">{formatNumber(total)}</span>
          <span className="text-xs text-muted-foreground">{centerLabel}</span>
        </div>
      )}
      {hasData && (
        <div className="absolute bottom-0 left-0 right-0 flex justify-center gap-4">
          {data.map((entry) => (
            <span key={entry.name} className="flex items-center gap-1.5 text-xs text-muted-foreground">
              <span className="h-2.5 w-2.5 rounded-full" style={{ background: entry.color }} />
              {entry.name} {formatNumber(entry.value)}
            </span>
          ))}
        </div>
      )}
    </div>
  )
}
