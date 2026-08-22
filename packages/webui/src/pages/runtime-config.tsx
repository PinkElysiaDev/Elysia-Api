import { useEffect, useState } from 'react'
import { AlertTriangle, Eye, EyeOff, RefreshCw, RotateCcw, Save, Settings, Database, Shield, Network } from 'lucide-react'
import { PageHeader } from '@/components/page-header'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Switch } from '@/components/ui/switch'
import { ErrorState, LoadingState } from '@/components/ui/states'
import { useToast } from '@/components/ui/use-toast'
import { useRuntimeConfig, revalidate } from '@/lib/hooks'
import { api } from '@/lib/api'
import type { LogLevel, RuntimeConfig } from '@/lib/types'

export function RuntimeConfigPage() {
  const toast = useToast()
  const { data, isLoading, error, mutate } = useRuntimeConfig()
  const [form, setForm] = useState<RuntimeConfig | null>(null)
  const [saving, setSaving] = useState(false)
  const [restartNotice, setRestartNotice] = useState(false)
  const [showToken, setShowToken] = useState(false)

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
      <div className="space-y-6">
        <PageHeader title="运行配置" description="查看与修改后端运行参数" />
        <Card>
          <LoadingState rows={4} columns={2} />
        </Card>
      </div>
    )
  }

  if (error) {
    return (
      <div className="space-y-6">
        <PageHeader title="运行配置" description="查看与修改后端运行参数" />
        <Card>
          <ErrorState message={(error as Error).message} onRetry={() => mutate()} />
        </Card>
      </div>
    )
  }

  if (!form) return null

  return (
    <div className="space-y-6">
      <PageHeader
        title="运行配置"
        description="可热更新 logLevel 与 httpTimeout；host/port/databasePath 变更需重启后端"
        actions={
          <Button onClick={handleSave} disabled={saving}>
            <Save className="h-4 w-4" /> {saving ? '保存中…' : '保存'}
          </Button>
        }
      />

      {restartNotice && (
        <div className="flex items-center gap-2 rounded-xl border border-primary/30 bg-primary/8 px-4 py-3 text-sm">
          <AlertTriangle className="h-4 w-4 text-primary" />
          部分配置已变更，需要手动重启或通过服务管理器重启后端才能生效。
        </div>
      )}

      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2">
            <Settings className="h-4 w-4 text-primary" /> 监听与超时
          </CardTitle>
          <CardDescription>修改 host 或 port 后需要重启后端进程。</CardDescription>
        </CardHeader>
        <CardContent className="grid gap-4 sm:grid-cols-2">
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
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2">
            <Database className="h-4 w-4 text-primary" /> 数据库
          </CardTitle>
          <CardDescription>修改数据库路径需重启后端生效。</CardDescription>
        </CardHeader>
        <CardContent className="space-y-2">
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
              <RotateCcw className="h-4 w-4" />
            </Button>
          </div>
          {data?.defaultDatabasePath && form.databasePath !== data.defaultDatabasePath && (
            <p className="text-xs text-muted-foreground">
              默认路径：<code className="rounded bg-muted px-1 py-0.5 font-mono">{data.defaultDatabasePath}</code>
            </p>
          )}
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2">
            <Shield className="h-4 w-4 text-primary" /> Panel Access Token
          </CardTitle>
          <CardDescription>用于 WebUI 面板登录与管理 API 鉴权的令牌。</CardDescription>
        </CardHeader>
        <CardContent className="space-y-2">
          <Label>Token</Label>
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
              {showToken ? <EyeOff className="h-4 w-4" /> : <Eye className="h-4 w-4" />}
            </Button>
          </div>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2">
            <Network className="h-4 w-4 text-primary" /> SSRF / 虚拟网卡（TUN fake-ip）
          </CardTitle>
          <CardDescription>
            开启后放行 Clash/Mihomo TUN fake-ip 段（198.18.0.0/15、240.0.0.0/4），解决全局 TUN 代理下
            上游域名被解析为假 IP 而遭 SSRF 守卫误杀返回 403 的问题。真实内网、云元数据 169.254.169.254、
            环回等段仍被拦截。变更即时生效，无需重启。
          </CardDescription>
        </CardHeader>
        <CardContent>
          <label className="flex items-center gap-3">
            <Switch
              checked={form.allowFakeIPOutbound}
              onCheckedChange={(v) => update('allowFakeIPOutbound', v)}
            />
            <span className="text-sm font-medium">允许 fake-ip 段出站</span>
          </label>
          {form.allowFakeIPOutbound && (
            <p className="mt-2 flex items-center gap-2 text-xs text-amber-600 dark:text-amber-400">
              <AlertTriangle className="h-3 w-3" /> 已放宽 SSRF 出站校验，请确保上游 baseUrl 可信。
            </p>
          )}
        </CardContent>
      </Card>

      <div>
        <Button
          variant="outline"
          onClick={async () => {
            try {
              await api.reload()
              toast.success('已触发热重载')
            } catch (err) {
              toast.error('热重载失败', (err as Error).message)
            }
          }}
        >
          <RefreshCw className="h-4 w-4" /> 触发热重载
        </Button>
      </div>
    </div>
  )
}
