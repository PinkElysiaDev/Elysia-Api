import { useEffect, useState } from 'react'
import { AlertTriangle, RefreshCw, Save, Settings } from 'lucide-react'
import { PageHeader } from '@/components/page-header'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Badge } from '@/components/ui/badge'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
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
      })
      await revalidate.runtimeConfig()
      setRestartNotice(result.restartRequired)
      toast.success(
        '运行配置已更新',
        result.restartRequired ? 'host/port 变更需重启后端生效' : '热更新字段已生效',
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
        description="可热更新 logLevel 与 httpTimeout；host/port 变更需重启后端"
        actions={
          <Button onClick={handleSave} disabled={saving}>
            <Save className="h-4 w-4" /> {saving ? '保存中…' : '保存'}
          </Button>
        }
      />

      {restartNotice && (
        <div className="flex items-center gap-2 rounded-xl border border-primary/30 bg-primary/8 px-4 py-3 text-sm">
          <AlertTriangle className="h-4 w-4 text-primary" />
          host 或 port 已变更，需要通过入口插件或手动重启后端才能生效。
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
          <CardTitle>引导配置（只读）</CardTitle>
          <CardDescription>以下字段属于 bootstrap config.json，通常通过重启后端或入口插件修改。</CardDescription>
        </CardHeader>
        <CardContent className="grid gap-4 sm:grid-cols-2">
          <ReadOnlyField label="数据库路径" value={form.databasePath} mono />
          <div className="space-y-2">
            <Label>Panel Access Token</Label>
            <Badge variant={form.panelAccessTokenConfigured ? 'success' : 'destructive'}>
              {form.panelAccessTokenConfigured ? '已配置' : '未配置'}
            </Badge>
          </div>
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

function ReadOnlyField({ label, value, mono }: { label: string; value: string; mono?: boolean }) {
  return (
    <div className="space-y-2">
      <Label>{label}</Label>
      <div
        className={`flex h-10 items-center truncate rounded-lg border border-border bg-muted/50 px-3 text-sm text-muted-foreground ${
          mono ? 'font-mono text-xs' : ''
        }`}
        title={value}
      >
        {value || '—'}
      </div>
    </div>
  )
}
