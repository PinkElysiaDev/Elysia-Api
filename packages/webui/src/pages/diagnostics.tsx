import { Activity, Cpu, Database, HardDrive, RefreshCw, Recycle, TerminalSquare } from 'lucide-react'
import { PageHeader } from '@/components/page-header'
import { StatCard } from '@/components/stat-card'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Skeleton } from '@/components/ui/skeleton'
import { ErrorState } from '@/components/ui/states'
import { useHealth } from '@/lib/hooks'
import { formatBytes, formatNumber } from '@/lib/utils'

export function DiagnosticsPage() {
  const { data: health, isLoading, error, mutate } = useHealth(10000)

  return (
    <div className="space-y-6">
      <PageHeader
        title="诊断"
        description="后端内存指标与运行健康度，每 10 秒自动刷新"
        actions={
          <Button variant="outline" onClick={() => mutate()}>
            <RefreshCw className="h-4 w-4" /> 刷新
          </Button>
        }
      />

      {error ? (
        <Card>
          <ErrorState message={(error as Error).message} onRetry={() => mutate()} />
        </Card>
      ) : (
        <>
          <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
            <StatCard
              accent
              label="健康状态"
              value={
                isLoading ? (
                  <Skeleton className="h-7 w-20" />
                ) : (
                  <Badge variant={health?.status === 'ok' ? 'success' : 'destructive'}>
                    {health?.status === 'ok' ? '正常' : '异常'}
                  </Badge>
                )
              }
              icon={<Activity className="h-5 w-5" />}
            />
            <StatCard
              label="数据库"
              value={
                isLoading ? (
                  <Skeleton className="h-7 w-20" />
                ) : (
                  <Badge variant={health?.database ? 'success' : 'destructive'}>
                    {health?.database ? '已连接' : '不可用'}
                  </Badge>
                )
              }
              icon={<Database className="h-5 w-5" />}
            />
            <StatCard
              label="已分配内存"
              value={isLoading ? <Skeleton className="h-7 w-24" /> : formatBytes(health?.memory.alloc)}
              hint="当前堆上活跃对象"
              icon={<Cpu className="h-5 w-5" />}
            />
            <StatCard
              label="系统保留内存"
              value={isLoading ? <Skeleton className="h-7 w-24" /> : formatBytes(health?.memory.sys)}
              hint="向 OS 申请的总量"
              icon={<HardDrive className="h-5 w-5" />}
            />
          </div>

          <div className="grid gap-4 lg:grid-cols-2">
            <Card>
              <CardHeader>
                <CardTitle className="flex items-center gap-2">
                  <Recycle className="h-4 w-4 text-primary" /> GC 指标
                </CardTitle>
                <CardDescription>垃圾回收次数反映分配压力</CardDescription>
              </CardHeader>
              <CardContent>
                <div className="flex items-baseline gap-2">
                  <span className="text-3xl font-semibold tracking-tight">
                    {isLoading ? '—' : formatNumber(health?.memory.numGC)}
                  </span>
                  <span className="text-sm text-muted-foreground">次 GC</span>
                </div>
              </CardContent>
            </Card>

            <Card>
              <CardHeader>
                <CardTitle className="flex items-center gap-2">
                  <TerminalSquare className="h-4 w-4 text-primary" /> pprof 性能分析
                </CardTitle>
                <CardDescription>仅当后端启用 enablePprof 时可用</CardDescription>
              </CardHeader>
              <CardContent className="space-y-3">
                <p className="text-sm text-muted-foreground">
                  若后端以 <code className="rounded bg-muted px-1.5 py-0.5 font-mono text-xs">enablePprof: true</code>{' '}
                  启动，可访问以下路径获取性能分析数据（需 Panel Token 鉴权）：
                </p>
                <div className="flex flex-wrap gap-2">
                  {['/debug/pprof/heap', '/debug/pprof/goroutine', '/debug/pprof/profile'].map((path) => (
                    <Badge key={path} variant="outline" className="font-mono">
                      {path}
                    </Badge>
                  ))}
                </div>
              </CardContent>
            </Card>
          </div>
        </>
      )}
    </div>
  )
}
