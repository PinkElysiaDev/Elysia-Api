import { useEffect, useState } from 'react'
import { AlertTriangle, Eye, EyeOff, RefreshCw, RotateCcw, Save } from 'lucide-react'
import { PageHeader } from '@/components/page-header'
import { SectionHeader } from '@/components/section-header'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Switch } from '@/components/ui/switch'
import { ErrorState, LoadingState } from '@/components/ui/states'
import { useToast } from '@/components/ui/use-toast'
import { useRuntimeConfig, useModelCatalogStatus, revalidate } from '@/lib/hooks'
import { api } from '@/lib/api'
import { formatRelative } from '@/lib/utils'
import type { LogLevel, RuntimeConfig } from '@/lib/types'

// 目录数据来源的展示名。
function catalogSourceLabel(source: string): string {
  switch (source) {
    case 'snapshot':
      return '内置快照'
    case 'cache':
      return '本地缓存'
    case 'network':
      return '在线更新'
    default:
      return source
  }
}

export function RuntimeConfigPage() {
  const toast = useToast()
  const { data, isLoading, error, mutate } = useRuntimeConfig()
  const { data: catalogStatus } = useModelCatalogStatus()
  const [form, setForm] = useState<RuntimeConfig | null>(null)
  const [saving, setSaving] = useState(false)
  const [restartNotice, setRestartNotice] = useState(false)
  const [showToken, setShowToken] = useState(false)
  const [catalogRefreshing, setCatalogRefreshing] = useState(false)

  useEffect(() => {
    if (data) setForm(data)
  }, [data])

  function update<K extends keyof RuntimeConfig>(key: K, value: RuntimeConfig[K]) {
    setForm((prev) => (prev ? { ...prev, [key]: value } : prev))
  }

  async function handleSave() {
    if (!form) return
    if (form.port < 1 || form.port > 65535) {
      toast.error('端口非法', 'port 必须在 1-65535 之间')
      return
    }
    if (form.httpTimeout < 0) {
      toast.error('超时非法', 'httpTimeout 必须为非负整数')
      return
    }
    setSaving(true)
    try {
      const result = await api.updateRuntimeConfig({
        host: form.host,
        port: form.port,
        logLevel: form.logLevel,
        httpTimeout: form.httpTimeout,
        // 留空 = 不修改现有令牌；提交空串会被后端拒绝（空令牌会锁死面板）。
        panelAccessToken: form.panelAccessToken.trim() ? form.panelAccessToken : undefined,
        databasePath: form.databasePath,
        enablePprof: form.enablePprof,
        allowFakeIPOutbound: form.allowFakeIPOutbound,
        // 目录刷新周期：0 = 默认 24h；保存即生效（后台周期动态读取配置）。
        modelCatalog: { syncIntervalMinutes: form.modelCatalog?.syncIntervalMinutes ?? 0 },
      })
      await revalidate.runtimeConfig()
      setRestartNotice(result.restartRequired)
      toast.success(
        '运行配置已更新',
        result.restartRequired ? '部分变更需重启后端生效' : '热更新字段已生效',
      )
    } catch (err) {
      toast.error('保存失败', (err as Error).message)
    } finally {
      setSaving(false)
    }
  }

  if (isLoading && !form) {
    return (
      <>
        <PageHeader title="运行配置" description="查看与修改后端运行参数" />
        <LoadingState rows={4} columns={2} />
      </>
    )
  }

  if (error) {
    return (
      <>
        <PageHeader title="运行配置" description="查看与修改后端运行参数" />
        <ErrorState message={(error as Error).message} onRetry={() => mutate()} />
      </>
    )
  }

  if (!form) return null

  return (
    <>
      <PageHeader
        title="运行配置"
        description="可热更新 logLevel 与 httpTimeout；host/port/databasePath 变更需重启后端"
        actions={
          <>
            <Button
              onClick={async () => {
                try {
                  await api.reload()
                  toast.success('已触发热重载')
                } catch (err) {
                  toast.error('热重载失败', (err as Error).message)
                }
              }}
            >
              <RefreshCw /> 重载配置
            </Button>
            <Button variant="primary" onClick={handleSave} disabled={saving}>
              <Save /> {saving ? '保存中…' : '保存'}
            </Button>
          </>
        }
      />

      {restartNotice && (
        <div className="flex items-center gap-2 rounded-[7px] border border-[color-mix(in_srgb,var(--amber)_35%,transparent)] bg-[color-mix(in_srgb,var(--amber)_7%,transparent)] px-4 py-3 text-sm text-amber">
          <AlertTriangle className="h-4 w-4 shrink-0" />
          部分配置已变更，需要手动重启或通过服务管理器重启后端才能生效。
        </div>
      )}

      {/* 服务 */}
      <section className="pt-8 first:pt-0">
        <SectionHeader title="服务" />
        <div className="grid gap-4 sm:grid-cols-2">
          <div className="space-y-2">
            <Label>Host</Label>
            <Input value={form.host} placeholder="127.0.0.1" onChange={(e) => update('host', e.target.value)} />
          </div>
          <div className="space-y-2">
            <Label>Port</Label>
            <Input
              type="number"
              min={1}
              max={65535}
              value={form.port}
              onChange={(e) => update('port', Number(e.target.value) || 0)}
            />
          </div>
          <div className="space-y-2">
            <Label>日志级别</Label>
            <Select value={form.logLevel} onValueChange={(v) => update('logLevel', v as LogLevel)}>
              <SelectTrigger>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="debug">Debug</SelectItem>
                <SelectItem value="info">Info</SelectItem>
                <SelectItem value="warn">Warn</SelectItem>
                <SelectItem value="error">Error</SelectItem>
              </SelectContent>
            </Select>
          </div>
          <div className="space-y-2">
            <Label>
              HTTP 超时 (秒) <span className="text-xs font-normal text-muted-foreground">0 = 不限制</span>
            </Label>
            <Input
              type="number"
              min={0}
              value={form.httpTimeout}
              onChange={(e) => update('httpTimeout', Math.max(0, Number(e.target.value) || 0))}
            />
          </div>
        </div>
      </section>

      {/* 安全 */}
      <section className="pt-8 first:pt-0">
        <SectionHeader title="安全" />
        <div className="max-w-xl space-y-4">
          <div className="space-y-2">
            <Label>Panel Access Token</Label>
            <p className="text-xs text-muted-foreground">用于 WebUI 面板登录与管理 API 鉴权的令牌；留空提交则保持不变。</p>
            <div className="flex gap-2">
              <Input
                type={showToken ? 'text' : 'password'}
                value={form.panelAccessToken}
                placeholder="输入新的 Panel Access Token"
                onChange={(e) => update('panelAccessToken', e.target.value)}
              />
              <Button
                variant="outline"
                size="icon"
                type="button"
                title={showToken ? '隐藏' : '显示'}
                onClick={() => setShowToken(!showToken)}
              >
                {showToken ? <EyeOff /> : <Eye />}
              </Button>
            </div>
          </div>
          <div className="space-y-2 rounded-[7px] border border-border p-3.5">
            <label className="flex items-center gap-3">
              <Switch
                checked={form.allowFakeIPOutbound}
                onCheckedChange={(v) => update('allowFakeIPOutbound', v)}
              />
              <span className="text-sm font-medium">允许 fake-ip 段出站（TUN 虚拟网卡）</span>
            </label>
            <p className="text-xs leading-relaxed text-muted-foreground">
              开启后放行 Clash/Mihomo TUN fake-ip 段（198.18.0.0/15、240.0.0.0/4），解决全局 TUN 代理下
              上游域名被解析为假 IP 而遭 SSRF 守卫误杀返回 403 的问题。真实内网、云元数据 169.254.169.254、
              环回等段仍被拦截。变更即时生效，无需重启。
            </p>
            {form.allowFakeIPOutbound && (
              <p className="flex items-center gap-2 text-xs text-amber">
                <AlertTriangle className="h-3 w-3" /> 已放宽 SSRF 出站校验，请确保上游 baseUrl 可信。
              </p>
            )}
          </div>
        </div>
      </section>

      {/* 数据库 */}
      <section className="pt-8 first:pt-0">
        <SectionHeader title="数据库" />
        <div className="max-w-xl space-y-2">
          <Label>数据库路径</Label>
          <div className="flex gap-2">
            <Input
              className="font-mono text-xs"
              value={form.databasePath}
              placeholder={data?.defaultDatabasePath}
              onChange={(e) => update('databasePath', e.target.value)}
            />
            <Button
              variant="outline"
              size="icon"
              title="重置为默认路径"
              onClick={() => update('databasePath', data?.defaultDatabasePath ?? '')}
            >
              <RotateCcw />
            </Button>
          </div>
          {data?.defaultDatabasePath && form.databasePath !== data.defaultDatabasePath && (
            <p className="text-xs text-muted-foreground">
              默认路径：<code className="rounded bg-code px-1 py-0.5 font-mono">{data.defaultDatabasePath}</code>
              （修改路径需重启后端生效）
            </p>
          )}
        </div>
      </section>

      {/* 高级：模型能力目录 */}
      <section className="pt-8 first:pt-0">
        <SectionHeader title="模型能力目录" />
        <div className="max-w-2xl space-y-4">
          <p className="text-xs leading-relaxed text-muted-foreground">
            数据源为 models.dev，用于模型刷新时自动回填视觉/工具等能力字段。
            目录定期后台更新并缓存到数据库同目录（model-catalog.json），重启后立即可用。
          </p>
          <div className="grid gap-4 sm:grid-cols-2">
            <div className="space-y-2">
              <Label>
                更新周期 (分钟){' '}
                <span className="text-xs font-normal text-muted-foreground">0 = 默认 1440（24 小时）</span>
              </Label>
              <Input
                type="number"
                min={0}
                value={form.modelCatalog?.syncIntervalMinutes ?? 0}
                onChange={(e) =>
                  setForm((prev) =>
                    prev
                      ? {
                          ...prev,
                          modelCatalog: {
                            ...(prev.modelCatalog ?? { enabled: true, url: '', syncIntervalMinutes: 0 }),
                            syncIntervalMinutes: Math.max(0, Number(e.target.value) || 0),
                          },
                        }
                      : prev,
                  )
                }
              />
              <p className="text-xs text-muted-foreground">随上方「保存」提交，保存后立即生效，无需重启。</p>
            </div>
            <div className="space-y-2">
              <Label>目录状态</Label>
              <div className="flex h-[34px] items-center">
                <Button
                  variant="outline"
                  disabled={catalogRefreshing || form.modelCatalog?.enabled === false}
                  onClick={async () => {
                    setCatalogRefreshing(true)
                    try {
                      const result = await api.modelCatalogRefresh()
                      await revalidate.modelCatalogStatus()
                      const status = result.status
                      if (status.lastError) {
                        toast.error('目录更新失败', status.lastError)
                      } else {
                        toast.success(
                          '能力目录已更新',
                          `已加载 ${status.entries} 个模型${status.lastSync ? ` · ${formatRelative(status.lastSync)}` : ''}`,
                        )
                      }
                    } catch (err) {
                      toast.error('目录更新失败', (err as Error).message)
                    } finally {
                      setCatalogRefreshing(false)
                    }
                  }}
                >
                  <RefreshCw className={catalogRefreshing ? 'animate-spin' : undefined} /> 立即更新
                </Button>
              </div>
            </div>
          </div>
          <div className="space-y-1 text-xs text-muted-foreground">
            <p>
              {catalogStatus
                ? catalogStatus.entries > 0
                  ? `已加载 ${catalogStatus.entries} 个模型`
                  : '尚未加载成功'
                : '状态加载中…'}
              {catalogStatus?.source && ` · 来源：${catalogSourceLabel(catalogStatus.source)}`}
              {catalogStatus?.lastSync && ` · 上次更新 ${formatRelative(catalogStatus.lastSync)}`}
              {catalogStatus?.sourceURL && (
                <>
                  {' '}
                  · <span className="font-mono">{catalogStatus.sourceURL}</span>
                </>
              )}
            </p>
            {catalogStatus?.lastError && (
              <p className="flex items-center gap-1.5 text-amber">
                <AlertTriangle className="h-3 w-3" /> 在线更新失败（内置快照/缓存仍可用）：{catalogStatus.lastError}
              </p>
            )}
          </div>
        </div>
      </section>
    </>
  )
}
