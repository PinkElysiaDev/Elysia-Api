import { useState } from 'react'
import { RefreshCw, Recycle, TerminalSquare } from 'lucide-react'
import { PageHeader } from '@/components/page-header'
import { SectionHeader } from '@/components/section-header'
import { KpiCard, KpiGrid } from '@/components/kpi-card'
import { Button } from '@/components/ui/button'
import { Switch } from '@/components/ui/switch'
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
    <>
      <PageHeader
        title="诊断"
        description="后端内存指标与运行健康度"
        actions={
          <Button onClick={() => mutate()}>
            <RefreshCw /> 刷新
          </Button>
        }
      />

      {error ? (
        <ErrorState message={(error as Error).message} onRetry={() => mutate()} />
      ) : (
        <>
          <section aria-label="健康">
            <KpiGrid cols={4}>
              <KpiCard
                label="健康状态"
                value={health?.status === 'ok' ? '正常' : isLoading ? '…' : '异常'}
                deltaTone={health?.status === 'ok' ? 'up' : 'down'}
                delta={health?.status === 'ok' ? '后端运行中' : '检查后端日志'}
              />
              <KpiCard
                label="数据库"
                value={health?.database ? '已连接' : isLoading ? '…' : '不可用'}
                deltaTone={health?.database ? 'up' : 'down'}
                delta={health?.database ? 'SQLite 连接正常' : 'SQLite 连接失败'}
              />
              <KpiCard
                label="已分配内存"
                value={isLoading ? '—' : formatBytes(health?.memory.alloc)}
                delta="当前堆上活跃对象"
              />
              <KpiCard
                label="系统保留内存"
                value={isLoading ? '—' : formatBytes(health?.memory.sys)}
                delta="向 OS 申请的总量"
              />
            </KpiGrid>
          </section>

          <section className="border-t border-border pt-6" aria-label="GC">
            <SectionHeader title="GC 指标" />
            <p className="tnum font-display text-2xl font-medium">
              {isLoading ? '—' : formatNumber(health?.memory.numGC)}
              <span className="ml-2 font-sans text-xs font-normal text-muted-foreground">次 GC · 垃圾回收次数反映分配压力</span>
            </p>
          </section>

          <section className="border-t border-border pt-6" aria-label="pprof">
            <SectionHeader title="pprof 性能分析" />
            <div className="max-w-xl space-y-4">
              <label className="flex items-center gap-3">
                <Switch
                  checked={!!runtimeConfig?.enablePprof}
                  disabled={togglingPprof}
                  onCheckedChange={handleTogglePprof}
                />
                <span className="text-sm font-medium">启用 pprof</span>
                <span className="text-xs text-muted-foreground">（需重启后端生效）</span>
              </label>
              <div className="flex flex-wrap gap-2">
                {['/debug/pprof/heap', '/debug/pprof/goroutine', '/debug/pprof/profile'].map((path) => (
                  <a
                    key={path}
                    href={path}
                    target="_blank"
                    rel="noopener noreferrer"
                    className="inline-flex items-center rounded border border-border px-1.5 py-px font-mono text-2xs text-muted-foreground transition-colors hover:border-rose hover:text-rose"
                  >
                    {path}
                  </a>
                ))}
              </div>
              <p className="flex items-center gap-1.5 text-xs text-muted-foreground">
                <TerminalSquare className="h-3.5 w-3.5" />
                链接在新窗口打开，由后端 /debug 端点提供。
              </p>
            </div>
          </section>

          <section className="border-t border-border pt-6" aria-label="运行说明">
            <SectionHeader title="说明" />
            <p className="max-w-[60ch] text-xs leading-relaxed text-muted-foreground">
              <Recycle className="mr-1 inline h-3.5 w-3.5" />
              内存与 GC 数字每 10 秒自动刷新；如需更细粒度的堆分析，启用 pprof 后使用 heap 剖像。
            </p>
          </section>
        </>
      )}
    </>
  )
}
