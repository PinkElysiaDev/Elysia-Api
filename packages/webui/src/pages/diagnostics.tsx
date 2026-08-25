import { useState } from 'react'
import {
  Activity,
  Cpu,
  Database,
  ExternalLink,
  RefreshCw,
  TerminalSquare,
  Zap,
} from 'lucide-react'
import { PageHeader } from '@/components/page-header'
import { KpiCard, KpiGrid } from '@/components/kpi-card'
import { Button } from '@/components/ui/button'
import { Switch } from '@/components/ui/switch'
import { SettingCard, SettingRow } from '@/components/ui/setting-card'
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
      toast.success(
        enabled ? 'pprof 性能剖析已开启' : 'pprof 性能剖析已关闭',
        '该项属于底层运行时配置，需重启服务进程生效',
      )
    } catch (err) {
      toast.error('切换失败', (err as Error).message)
    } finally {
      setTogglingPprof(false)
    }
  }

  const pprofEndpoints = [
    { name: '堆内存分配 (Heap)', path: '/debug/pprof/heap', desc: '查看活跃堆对象及内存泄漏' },
    { name: 'Goroutine 协程', path: '/debug/pprof/goroutine', desc: '检查并发泄漏与阻塞堆栈' },
    { name: 'CPU 实时采样', path: '/debug/pprof/profile', desc: '30s CPU 采样 profile 下载' },
    { name: '互斥锁争用 (Mutex)', path: '/debug/pprof/mutex', desc: '锁竞争与持有时长统计' },
  ]

  return (
    <div className="space-y-6">
      <PageHeader
        title="系统诊断"
        description="实时监控 Go 运行时内存拓扑、GC 回收压力及底层性能分析工具。"
        actions={
          <Button onClick={() => mutate()} disabled={isLoading}>
            <RefreshCw className={isLoading ? 'h-4 w-4 animate-spin' : 'h-4 w-4'} /> 立即刷新
          </Button>
        }
      />

      {error ? (
        <ErrorState message={(error as Error).message} onRetry={() => mutate()} />
      ) : (
        <>
          {/* 指标矩阵 */}
          <section aria-label="核心指标">
            <KpiGrid cols={4}>
              <KpiCard
                variant="hero"
                icon={<Activity className="h-4 w-4" />}
                label="堆分配内存 (Alloc)"
                value={isLoading ? '—' : formatBytes(health?.memory.alloc)}
                deltaTone={health?.status === 'ok' ? 'up' : 'down'}
                delta={
                  health?.status === 'ok'
                    ? '网关运转正常 · 活跃堆对象占用'
                    : '服务异常，检测到故障（详见日志）'
                }
              />
              <KpiCard
                icon={<Cpu className="h-4 w-4" />}
                label="向 OS 申请 (Sys)"
                value={isLoading ? '—' : formatBytes(health?.memory.sys)}
                delta="虚拟内存系统保留量"
              />
              <KpiCard
                icon={<Database className="h-4 w-4" />}
                label="数据库状态"
                value={health?.database ? '正常' : isLoading ? '…' : '不可达'}
                deltaTone={health?.database ? 'up' : 'down'}
                delta={health?.database ? 'SQLite 连接就绪' : '数据库文件不可达'}
              />
              <KpiCard
                icon={<Zap className="h-4 w-4" />}
                label="累计 GC 次数"
                value={isLoading ? '—' : formatNumber(health?.memory.numGC)}
                delta="启动至今完成回收次数"
              />
            </KpiGrid>
          </section>

          <div className="grid grid-cols-1 gap-6 lg:grid-cols-2">
            {/* GC 压力与内存回收（数据已上移 KPI 卡，此处保留解读说明） */}
            <SettingCard
              icon={Zap}
              title="垃圾回收 (GC) 运行指标"
              description="Go Runtime 内存分配器压力与自动回收频次统计"
            >
              <div className="text-xs leading-relaxed text-muted-foreground space-y-1.5">
                <p>• 指标数据由后端服务每 10 秒自动聚合更新一次，无感轮询。</p>
                <p>• 当 Alloc / Sys 持续攀升且 GC 次数过高时，可能存在上游大型请求或并发突增。</p>
              </div>
            </SettingCard>

            {/* pprof 性能剖析 */}
            <SettingCard
              icon={TerminalSquare}
              title="pprof 运行时性能剖析"
              description="提供 Go 官方性能剖析端点，用于排查高 CPU、内存泄漏与协程挂死"
            >
              <div className="space-y-4">
                <SettingRow
                  label="启用 pprof 分析端点"
                  description="在 /debug/pprof/* 暴露性能分析接口（变更需重启后端）"
                >
                  <Switch
                    checked={!!runtimeConfig?.enablePprof}
                    disabled={togglingPprof}
                    onCheckedChange={handleTogglePprof}
                  />
                </SettingRow>

                <div className="space-y-2 pt-2">
                  <div className="text-xs font-medium text-foreground">常用分析端点快速入口：</div>
                  <div className="grid grid-cols-1 gap-2 sm:grid-cols-2">
                    {pprofEndpoints.map((item) => (
                      <a
                        key={item.path}
                        href={item.path}
                        target="_blank"
                        rel="noopener noreferrer"
                        className="group flex flex-col justify-between rounded-lg border border-border/80 bg-card p-3 transition-colors hover:border-primary/50 hover:bg-secondary/40"
                      >
                        <div className="flex items-center justify-between">
                          <span className="font-medium text-xs text-foreground group-hover:text-primary">
                            {item.name}
                          </span>
                          <ExternalLink className="h-3.5 w-3.5 text-muted-foreground/60 transition-transform group-hover:translate-x-0.5 group-hover:-translate-y-0.5 group-hover:text-primary" />
                        </div>
                        <span className="mt-1 font-mono text-2xs text-muted-foreground truncate">
                          {item.path}
                        </span>
                      </a>
                    ))}
                  </div>
                </div>
              </div>
            </SettingCard>
          </div>
        </>
      )}
    </div>
  )
}
