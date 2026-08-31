import { useCallback, useEffect, useState } from 'react'
import {
  AlertTriangle,
  Database,
  Eye,
  EyeOff,
  HardDrive,
  Layers,
  RefreshCw,
  RotateCcw,
  Save,
  Server,
  ShieldCheck,
  Trash2,
} from 'lucide-react'
import { PageHeader } from '@/components/page-header'
import { RoleWatermark } from '@/components/role-watermark'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Switch } from '@/components/ui/switch'
import { SettingSection, SettingRow } from '@/components/ui/setting-card'
import { ErrorState, LoadingState } from '@/components/ui/states'
import { useToast } from '@/components/ui/use-toast'
import { useRuntimeConfig, useModelCatalogStatus, revalidate } from '@/lib/hooks'
import { api } from '@/lib/api'
import { formatRelative } from '@/lib/utils'
import type { LogLevel, RuntimeConfig, UsageLogRuntimeConfig, UsageStorageStatus } from '@/lib/types'

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

// 日志管理表单缺省值（后端 GET 返回生效值，老版本无该块时兜底）。
const defaultUsageLog: UsageLogRuntimeConfig = {
  persistEnabled: true,
  retentionDays: 0,
  maxStorageMB: 0,
  maxRecords: 0,
  bodyMaxKB: 1024,
  bodyOnErrorOnly: false,
  externalizeMedia: true,
  cleanupIntervalMinutes: 60,
}

function formatBytes(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes <= 0) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let value = bytes
  let unit = 0
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024
    unit++
  }
  return `${value >= 100 ? value.toFixed(0) : value.toFixed(1)} ${units[unit]}`
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
  const [storage, setStorage] = useState<UsageStorageStatus | null>(null)
  const [cleaning, setCleaning] = useState(false)

  const refreshStorage = useCallback(async () => {
    try {
      setStorage(await api.usageStorage())
    } catch {
      // 占用状态展示尽力而为：失败保持旧值，不打断设置页。
    }
  }, [])

  useEffect(() => {
    refreshStorage()
  }, [refreshStorage])

  function updateUsageLog<K extends keyof UsageLogRuntimeConfig>(key: K, value: UsageLogRuntimeConfig[K]) {
    setForm((prev) =>
      prev ? { ...prev, usageLog: { ...(prev.usageLog ?? defaultUsageLog), [key]: value } } : prev,
    )
  }

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
        // 日志管理：数值字段整体回写（GET 返回生效值，保存即显式化当前口径）。
        usageLog: form.usageLog ?? defaultUsageLog,
        // 目录刷新周期：0 = 默认 24h；保存即生效（后台周期动态读取配置）。
        modelCatalog: { syncIntervalMinutes: form.modelCatalog?.syncIntervalMinutes ?? 0 },
      })
      await revalidate.runtimeConfig()
      refreshStorage()
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
        <PageHeader title="运行配置" />
        <LoadingState rows={4} columns={2} />
      </div>
    )
  }

  if (error) {
    return (
      <div className="space-y-6">
        <PageHeader title="运行配置" />
        <ErrorState message={(error as Error).message} onRetry={() => mutate()} />
      </div>
    )
  }

  if (!form) return null

  return (
    <>
      <RoleWatermark className="-right-8 top-0 opacity-[0.05] dark:opacity-[0.08]" />

      <div className="relative z-[1] space-y-6">
        <PageHeader
          title="运行配置"        actions={
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

      <div className="grid grid-cols-1 gap-8 lg:grid-cols-2 pt-2">
        {/* 章节 1: 服务与网络 */}
        <SettingSection
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
        </SettingSection>

        {/* 章节 2: 安全与访问鉴权 */}
        <SettingSection
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
        </SettingSection>

        {/* 章节 3: 数据存储 */}
        <SettingSection
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
        </SettingSection>

        {/* 章节 4: 模型能力目录 */}
        <SettingSection
          icon={Layers}
          title="模型能力目录 (models.dev)"          action={
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
              description="定期后台同步周期（分钟，0 表示不启用该功能）"
            >
              <div className="flex w-full items-center gap-2 sm:w-48">
                <Input
                  type="number"
                  min={0}
                  className="font-mono text-xs"
                  value={form.modelCatalog?.syncIntervalMinutes ?? 1440}
                  onChange={(e) =>
                    setForm((prev) =>
                      prev
                        ? {
                            ...prev,
                            modelCatalog: {
                              ...(prev.modelCatalog ?? { enabled: true, url: '', syncIntervalMinutes: 1440 }),
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

            <div className="border-t border-border/40 pt-3 text-xs space-y-2">
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
        </SettingSection>

        {/* 章节 5: 日志管理 */}
        <SettingSection
          icon={HardDrive}
          title="日志管理"
          description="请求日志的留存策略、请求体保存上限与媒体外置"
          action={
            <Button
              variant="outline"
              size="sm"
              disabled={cleaning}
              onClick={async () => {
                setCleaning(true)
                try {
                  const result = await api.usageCleanup()
                  if (result.accepted) {
                    toast.success('清理已触发', '后台正在执行一轮清理巡检，稍后刷新查看结果')
                    // 巡检是异步的，稍等后再拉取状态。
                    setTimeout(refreshStorage, 3000)
                  } else {
                    toast.success('清理已在进行中', '上一轮清理尚未结束，请稍后再试')
                  }
                } catch (err) {
                  toast.error('触发清理失败', (err as Error).message)
                } finally {
                  setCleaning(false)
                }
              }}
            >
              <Trash2 className="h-3.5 w-3.5" /> 立即清理
            </Button>
          }
        >
          <div className="space-y-4">
            <SettingRow
              label="启用日志持久化"
              description="关闭后新请求完全不落库（统计与日志面板不再更新）"
            >
              <Switch
                checked={form.usageLog?.persistEnabled ?? defaultUsageLog.persistEnabled}
                onCheckedChange={(v) => updateUsageLog('persistEnabled', v)}
              />
            </SettingRow>

            <SettingRow
              label="过期清理天数"
              description="自动删除早于该天数的日志记录（0 = 不启用过期清理）"
            >
              <div className="flex w-full items-center gap-2 sm:w-48">
                <Input
                  type="number"
                  min={0}
                  className="font-mono text-xs"
                  value={form.usageLog?.retentionDays ?? defaultUsageLog.retentionDays}
                  onChange={(e) => updateUsageLog('retentionDays', Math.max(0, Number(e.target.value) || 0))}
                />
                <span className="shrink-0 text-xs text-muted-foreground">天</span>
              </div>
            </SettingRow>

            <SettingRow
              label="最大占用"
              description="数据库体积超限时按最旧优先自动清理（0 = 不限）"
            >
              <div className="flex w-full items-center gap-2 sm:w-48">
                <Input
                  type="number"
                  min={0}
                  className="font-mono text-xs"
                  value={form.usageLog?.maxStorageMB ?? defaultUsageLog.maxStorageMB}
                  onChange={(e) => updateUsageLog('maxStorageMB', Math.max(0, Number(e.target.value) || 0))}
                />
                <span className="shrink-0 text-xs text-muted-foreground">MB</span>
              </div>
            </SettingRow>

            <SettingRow
              label="最大保留条数"
              description="超出该条数时自动删除最旧的日志（0 = 不限）"
            >
              <div className="flex w-full items-center gap-2 sm:w-48">
                <Input
                  type="number"
                  min={0}
                  className="font-mono text-xs"
                  value={form.usageLog?.maxRecords ?? defaultUsageLog.maxRecords}
                  onChange={(e) => updateUsageLog('maxRecords', Math.max(0, Number(e.target.value) || 0))}
                />
                <span className="shrink-0 text-xs text-muted-foreground">条</span>
              </div>
            </SettingRow>

            <SettingRow
              label="请求体保存上限"
              description="每段链路（请求/转发/回传）落库的最大体积（0 = 不保存任何请求体）"
            >
              <div className="flex w-full items-center gap-2 sm:w-48">
                <Input
                  type="number"
                  min={0}
                  className="font-mono text-xs"
                  value={form.usageLog?.bodyMaxKB ?? defaultUsageLog.bodyMaxKB}
                  onChange={(e) => updateUsageLog('bodyMaxKB', Math.max(0, Number(e.target.value) || 0))}
                />
                <span className="shrink-0 text-xs text-muted-foreground">KB</span>
              </div>
            </SettingRow>

            <SettingRow
              label="仅保存出错请求体"
              description="开启后成功请求不保留请求体（仅元数据），失败请求完整保留以便排查"
            >
              <Switch
                checked={form.usageLog?.bodyOnErrorOnly ?? defaultUsageLog.bodyOnErrorOnly}
                onCheckedChange={(v) => updateUsageLog('bodyOnErrorOnly', v)}
              />
            </SettingRow>

            <SettingRow
              label="媒体外置保存"
              description="请求体中的 base64 媒体（图片/音频/视频/文件）存为独立文件，正文以占位符替代"
            >
              <Switch
                checked={form.usageLog?.externalizeMedia ?? defaultUsageLog.externalizeMedia}
                onCheckedChange={(v) => updateUsageLog('externalizeMedia', v)}
              />
            </SettingRow>

            <SettingRow
              label="清理巡检周期"
              description="后台自动清理的执行周期（分钟，最小 5）"
            >
              <div className="flex w-full items-center gap-2 sm:w-48">
                <Input
                  type="number"
                  min={5}
                  className="font-mono text-xs"
                  value={form.usageLog?.cleanupIntervalMinutes ?? defaultUsageLog.cleanupIntervalMinutes}
                  onChange={(e) =>
                    updateUsageLog('cleanupIntervalMinutes', Math.max(0, Number(e.target.value) || 0))
                  }
                />
                <span className="shrink-0 text-xs text-muted-foreground">分钟</span>
              </div>
            </SettingRow>

            {/* 当前占用与最近清理结果 */}
            <div className="border-t border-border/40 pt-3 text-xs space-y-2">
              <div className="flex items-center justify-between text-muted-foreground">
                <span>数据库占用</span>
                <span className="font-semibold text-foreground">
                  {storage ? `${formatBytes(storage.db.totalBytes)}（逻辑 ${formatBytes(storage.db.logicalBytes)}）` : '—'}
                </span>
              </div>
              <div className="flex items-center justify-between text-muted-foreground">
                <span>日志记录</span>
                <span className="font-semibold text-foreground">
                  {storage ? `${storage.recordCount.toLocaleString()} 条` : '—'}
                </span>
              </div>
              <div className="flex items-center justify-between text-muted-foreground">
                <span>外置媒体</span>
                <span className="font-semibold text-foreground">
                  {storage ? `${storage.assets.files.toLocaleString()} 个 · ${formatBytes(storage.assets.bytes)}` : '—'}
                </span>
              </div>
              {storage?.lastCleanup?.lastRunAt && (
                <div className="flex items-center justify-between text-muted-foreground">
                  <span>最近清理</span>
                  <span className="font-mono text-2xs">
                    {formatRelative(storage.lastCleanup.lastRunAt)}
                    {storage.lastCleanup.deletedByTTL +
                      storage.lastCleanup.deletedByRecords +
                      storage.lastCleanup.deletedBySize >
                    0
                      ? ` · 删 ${storage.lastCleanup.deletedByTTL + storage.lastCleanup.deletedByRecords + storage.lastCleanup.deletedBySize} 条`
                      : ' · 无删除'}
                  </span>
                </div>
              )}
              {storage?.lastCleanup?.lastError && (
                <p className="mt-2 text-2xs text-ember flex items-center gap-1">
                  <AlertTriangle className="h-3 w-3 shrink-0" /> 清理异常：{storage.lastCleanup.lastError}
                </p>
              )}
              <p className="pt-1 text-2xs text-muted-foreground/70">
                统计聚合（小时/日汇总）在日志清理后仍完整保留，历史用量报表不受影响。
              </p>
            </div>
          </div>
        </SettingSection>
      </div>
    </div>
    </>
  )
}
