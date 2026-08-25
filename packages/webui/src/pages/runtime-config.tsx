import { useEffect, useState } from 'react'
import {
  AlertTriangle,
  Database,
  Eye,
  EyeOff,
  Layers,
  RefreshCw,
  RotateCcw,
  Save,
  Server,
  ShieldCheck,
} from 'lucide-react'
import { PageHeader } from '@/components/page-header'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Switch } from '@/components/ui/switch'
import { SettingCard, SettingRow } from '@/components/ui/setting-card'
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
        result.restartRequired ? '部分变更需重启后端生效' : '热更新字段已即时生效',
      )
    } catch (err) {
      toast.error('保存失败', (err as Error).message)
    } finally {
      setSaving(false)
    }
  }

  if (isLoading && !form) {
    return (
      <div className="space-y-6">
        <PageHeader title="运行配置" description="查看与修改后端运行参数" />
        <LoadingState rows={4} columns={2} />
      </div>
    )
  }

  if (error) {
    return (
      <div className="space-y-6">
        <PageHeader title="运行配置" description="查看与修改后端运行参数" />
        <ErrorState message={(error as Error).message} onRetry={() => mutate()} />
      </div>
    )
  }

  if (!form) return null

  return (
    <div className="space-y-6">
      <PageHeader
        title="运行配置"
        description="管理网关端口、安全鉴权、持久化存储与模型能力目录参数。"
        actions={
          <>
            <Button
              onClick={async () => {
                try {
                  await api.reload()
                  toast.success('已触发配置热重载')
                } catch (err) {
                  toast.error('热重载失败', (err as Error).message)
                }
              }}
            >
              <RefreshCw className="h-4 w-4" /> 重载配置
            </Button>
            <Button variant="primary" onClick={handleSave} disabled={saving}>
              <Save className="h-4 w-4" /> {saving ? '保存中…' : '保存配置'}
            </Button>
          </>
        }
      />

      {restartNotice && (
        <div className="flex items-center gap-3 rounded-xl border border-amber/40 bg-amber/10 px-4 py-3 text-sm text-amber shadow-sm">
          <AlertTriangle className="h-5 w-5 shrink-0" />
          <span>部分基础配置已变更，需要手动重启或通过服务管理器重启后端进程方可生效。</span>
        </div>
      )}

      <div className="grid grid-cols-1 gap-6 lg:grid-cols-2">
        {/* 卡片 1: 服务与网络 */}
        <SettingCard
          icon={Server}
          title="服务与网络"
          description="网关监听地址、网络端口与核心超时设置"
        >
          <div className="space-y-4">
            <SettingRow
              label="监听 Host"
              description="网关绑定的网络接口（如 127.0.0.1 或 0.0.0.0）"
            >
              <Input
                className="w-full sm:w-56 font-mono text-xs"
                value={form.host}
                placeholder="127.0.0.1"
                onChange={(e) => update('host', e.target.value)}
              />
            </SettingRow>

            <SettingRow
              label="监听 Port"
              description="服务监听端口（1 ~ 65535，需重启生效）"
            >
              <Input
                type="number"
                min={1}
                max={65535}
                className="w-full sm:w-56 font-mono text-xs"
                value={form.port}
                onChange={(e) => update('port', Number(e.target.value) || 0)}
              />
            </SettingRow>

            <SettingRow
              label="日志记录级别"
              description="控制控制台与文件日志输出的详细程度（支持热更新）"
            >
              <div className="w-full sm:w-56">
                <Select value={form.logLevel} onValueChange={(v) => update('logLevel', v as LogLevel)}>
                  <SelectTrigger className="w-full">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="debug">Debug（调试）</SelectItem>
                    <SelectItem value="info">Info（信息）</SelectItem>
                    <SelectItem value="warn">Warn（警告）</SelectItem>
                    <SelectItem value="error">Error（错误）</SelectItem>
                  </SelectContent>
                </Select>
              </div>
            </SettingRow>

            <SettingRow
              label="HTTP 超时时间"
              description="上游请求超时时间（秒，0 表示不设硬性超时）"
            >
              <div className="flex w-full items-center gap-2 sm:w-56">
                <Input
                  type="number"
                  min={0}
                  className="font-mono text-xs"
                  value={form.httpTimeout}
                  onChange={(e) => update('httpTimeout', Math.max(0, Number(e.target.value) || 0))}
                />
                <span className="shrink-0 text-xs text-muted-foreground">秒</span>
              </div>
            </SettingRow>
          </div>
        </SettingCard>

        {/* 卡片 2: 安全与访问鉴权 */}
        <SettingCard
          icon={ShieldCheck}
          title="安全与访问鉴权"
          description="WebUI 面板口令与网络出站安全守卫"
        >
          <div className="space-y-4">
            <SettingRow
              label="Panel Access Token"
              description="用于登录控制台与管理 API 的鉴权令牌；留空提交则保持原值"
            >
              <div className="flex w-full sm:w-72 items-center gap-1.5">
                <Input
                  type={showToken ? 'text' : 'password'}
                  value={form.panelAccessToken}
                  placeholder="输入新令牌（留空不变）"
                  className="font-mono text-xs"
                  onChange={(e) => update('panelAccessToken', e.target.value)}
                />
                <Button
                  variant="outline"
                  size="iconSm"
                  type="button"
                  title={showToken ? '隐藏明文' : '显示明文'}
                  onClick={() => setShowToken(!showToken)}
                >
                  {showToken ? <EyeOff className="h-3.5 w-3.5" /> : <Eye className="h-3.5 w-3.5" />}
                </Button>
              </div>
            </SettingRow>

            <SettingRow
              label="允许 Fake-IP 段出站"
              description="放行 TUN 虚拟网卡 fake-ip 段（198.18.0.0/15、240.0.0.0/4），解决全局代理下域名解析被 SSRF 拦截的问题"
            >
              <Switch
                checked={form.allowFakeIPOutbound}
                onCheckedChange={(v) => update('allowFakeIPOutbound', v)}
              />
            </SettingRow>
            {form.allowFakeIPOutbound && (
              <div className="rounded-lg bg-amber/10 p-2.5 text-2xs text-amber flex items-center gap-2">
                <AlertTriangle className="h-4 w-4 shrink-0" />
                <span>已放宽 fake-ip SSRF 出站校验，真实内网与 169.254 元数据仍处于拦截保护中。</span>
              </div>
            )}
          </div>
        </SettingCard>

        {/* 卡片 3: 数据存储 */}
        <SettingCard
          icon={Database}
          title="持久化存储"
          description="SQLite 数据库文件路径与用量记录存储"
        >
          <div className="space-y-4">
            <SettingRow
              label="数据库存储路径"
              description="SQLite 数据库文件存放路径（变更需重启生效）"
              inline={false}
            >
              <div className="space-y-2">
                <div className="flex items-center gap-2">
                  <Input
                    className="font-mono text-xs"
                    value={form.databasePath}
                    placeholder={data?.defaultDatabasePath}
                    onChange={(e) => update('databasePath', e.target.value)}
                  />
                  <Button
                    variant="outline"
                    size="iconSm"
                    title="恢复为系统默认路径"
                    onClick={() => update('databasePath', data?.defaultDatabasePath ?? '')}
                  >
                    <RotateCcw className="h-3.5 w-3.5" />
                  </Button>
                </div>
                {data?.defaultDatabasePath && form.databasePath !== data.defaultDatabasePath && (
                  <p className="text-2xs text-muted-foreground">
                    系统默认路径：<code className="rounded bg-muted px-1.5 py-0.5 font-mono text-foreground">{data.defaultDatabasePath}</code>
                  </p>
                )}
              </div>
            </SettingRow>
          </div>
        </SettingCard>

        {/* 卡片 4: 模型能力目录 */}
        <SettingCard
          icon={Layers}
          title="模型能力目录 (models.dev)"
          description="自动识别与回填上游模型的视觉、工具调用等高级能力"
          action={
            <Button
              variant="outline"
              size="sm"
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
              <RefreshCw className={catalogRefreshing ? 'h-3.5 w-3.5 animate-spin' : 'h-3.5 w-3.5'} /> 立即更新
            </Button>
          }
        >
          <div className="space-y-4">
            <SettingRow
              label="后台自动同步周期"
              description="定期后台同步周期（分钟，0 表示默认 1440 即 24 小时）"
            >
              <div className="flex w-full items-center gap-2 sm:w-48">
                <Input
                  type="number"
                  min={0}
                  className="font-mono text-xs"
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
                <span className="shrink-0 text-xs text-muted-foreground">分钟</span>
              </div>
            </SettingRow>

            <div className="rounded-xl border border-border/70 bg-secondary/30 p-3 text-xs space-y-1.5">
              <div className="flex items-center justify-between text-muted-foreground">
                <span>当前加载规模</span>
                <span className="font-semibold text-foreground">
                  {catalogStatus && catalogStatus.entries > 0 ? `${catalogStatus.entries} 个模型规范` : '未加载'}
                </span>
              </div>
              <div className="flex items-center justify-between text-muted-foreground">
                <span>数据来源</span>
                <span className="font-medium text-foreground">{catalogSourceLabel(catalogStatus?.source ?? 'snapshot')}</span>
              </div>
              {catalogStatus?.lastSync && (
                <div className="flex items-center justify-between text-muted-foreground">
                  <span>最近更新时间</span>
                  <span className="font-mono text-2xs">{formatRelative(catalogStatus.lastSync)}</span>
                </div>
              )}
              {catalogStatus?.lastError && (
                <p className="mt-2 text-2xs text-ember flex items-center gap-1">
                  <AlertTriangle className="h-3 w-3 shrink-0" /> 在线拉取异常：{catalogStatus.lastError}
                </p>
              )}
            </div>
          </div>
        </SettingCard>
      </div>
    </div>
  )
}
