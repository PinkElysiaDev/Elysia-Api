import { useState } from 'react'
import { Activity, Cpu, Database, HardDrive, RefreshCw, Recycle, TerminalSquare } from 'lucide-react'
import { PageHeader } from '@/components/page-header'
import { StatCard } from '@/components/stat-card'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Skeleton } from '@/components/ui/skeleton'
import { Label } from '@/components/ui/label'
import { ErrorState } from '@/components/ui/states'
import { useHealth, useRuntimeConfig, revalidate } from '@/lib/hooks'
import { useToast } from '@/components/ui/use-toast'
import { api } from '@/lib/api'
import { formatBytes, formatNumber } from '@/lib/utils'

export function DiagnosticsPage() {
  const toast = useToast()
  const { data: health, isLoading, error, mutate } = useHealth(10000)
  const { data: runtimeConfig } = useRuntimeConfig()
  const [togglingPprof, setTogglingPprof] = useState(false)

  async function handleTogglePprof(enabled: boolean) {
    setTogglingPprof(true)
    try {
      await api.updateRuntimeConfig({ enablePprof: enabled })
      await revalidate.runtimeConfig()
      toast.success(enabled ? 'pprof 已启用' : 'pprof 已关闭', '需重启后端生效')
    } catch (err) {
      toast.error('切换失败', (err as Error).message)
    } finally {
      setTogglingPprof(false)
    }
  }

  return (
    <div className="space-y-6">
      <PageHeader
        title="诊断"
        description="后端内存指标与运行健康度"
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
              </CardHeader>
              <CardContent className="space-y-4">
                <div className="flex items-center justify-between">
                  <Label>启用 pprof</Label>
                  <Button
                    variant={runtimeConfig?.enablePprof ? 'default' : 'outline'}
                    size="sm"
                    disabled={togglingPprof}
                    onClick={() => handleTogglePprof(!runtimeConfig?.enablePprof)}
                  >
                    {runtimeConfig?.enablePprof ? '已启用' : '已关闭'}
                  </Button>
                </div>
                <div className="flex flex-wrap gap-2">
                  {['/debug/pprof/heap', '/debug/pprof/goroutine', '/debug/pprof/profile'].map((path) => (
                    <a
                      key={path}
                      href={path}
                      target="_blank"
                      rel="noopener noreferrer"
                      className="inline-flex"
                    >
                      <Badge variant="outline" className="font-mono hover:bg-accent cursor-pointer">
                        {path}
                      </Badge>
                    </a>
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
