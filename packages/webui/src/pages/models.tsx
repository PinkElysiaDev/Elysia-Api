import { useMemo, useState } from 'react'
import { Boxes, Eye, RefreshCw, Wrench } from 'lucide-react'
import { PageHeader } from '@/components/page-header'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Input } from '@/components/ui/input'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { AsyncState } from '@/components/ui/states'
import { ModelTypeBadge, PlatformBadge } from '@/components/badges'
import { useToast } from '@/components/ui/use-toast'
import { useModels, revalidate } from '@/lib/hooks'
import { api } from '@/lib/api'
import { cn, formatNumber, formatRelative } from '@/lib/utils'
import type { Model } from '@/lib/types'

export function ModelsPage() {
  const toast = useToast()
  const { data, isLoading, error, mutate } = useModels()
  const [refreshing, setRefreshing] = useState(false)
  const [search, setSearch] = useState('')
  const [platform, setPlatform] = useState('all')
  const [type, setType] = useState('all')

  async function refreshAll() {
    setRefreshing(true)
    try {
      const result = await api.refreshModels()
      await revalidate.models()
      toast.success('已刷新模型', `共聚合 ${result.count} 个模型`)
    } catch (err) {
      toast.error('刷新失败', (err as Error).message)
    } finally {
      setRefreshing(false)
    }
  }

  const filtered = useMemo(() => {
    if (!data) return []
    const kw = search.trim().toLowerCase()
    return data.filter((m) => {
      if (platform !== 'all' && m.platform !== platform) return false
      if (type !== 'all' && m.type !== type) return false
      if (kw && !`${m.id} ${m.name}`.toLowerCase().includes(kw)) return false
      return true
    })
  }, [data, search, platform, type])

  const grouped = useMemo(() => {
    const map = new Map<string, Model[]>()
    for (const model of filtered) {
      const key = model.sourceName || model.sourceId || '未知来源'
      const list = map.get(key) ?? []
      list.push(model)
      map.set(key, list)
    }
    return Array.from(map.entries()).sort((a, b) => a[0].localeCompare(b[0]))
  }, [filtered])

  return (
    <div className="space-y-6">
      <PageHeader
        title="模型缓存"
        description="聚合自各模型源的可用模型，按来源分组展示"
        actions={
          <Button onClick={refreshAll} disabled={refreshing}>
            <RefreshCw className={refreshing ? 'animate-spin' : ''} /> 全量刷新
          </Button>
        }
      />

      <Card className="p-4">
        <div className="flex flex-wrap items-center gap-3">
          <Input
            className="w-full sm:w-64"
            placeholder="搜索模型 ID 或名称"
            value={search}
            onChange={(e) => setSearch(e.target.value)}
          />
          <Select value={platform} onValueChange={setPlatform}>
            <SelectTrigger className="w-[150px]">
              <SelectValue placeholder="平台" />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="all">全部平台</SelectItem>
              <SelectItem value="openai">OpenAI</SelectItem>
              <SelectItem value="claude">Claude</SelectItem>
              <SelectItem value="gemini">Gemini</SelectItem>
            </SelectContent>
          </Select>
          <Select value={type} onValueChange={setType}>
            <SelectTrigger className="w-[150px]">
              <SelectValue placeholder="类型" />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="all">全部类型</SelectItem>
              <SelectItem value="llm">LLM</SelectItem>
              <SelectItem value="embedding">Embedding</SelectItem>
              <SelectItem value="reranker">Reranker</SelectItem>
            </SelectContent>
          </Select>
          <span className="ml-auto text-sm text-muted-foreground">
            共 {formatNumber(filtered.length)} 个模型
          </span>
        </div>
      </Card>

      <AsyncState
        isLoading={isLoading}
        error={error}
        data={grouped}
        onRetry={() => mutate()}
        emptyIcon={<Boxes className="h-7 w-7" />}
        emptyTitle="暂无模型"
        emptyDescription="先在模型源页配置供应商并刷新模型。"
      >
        {(groups) => (
          <div className="space-y-4">
            {groups.map(([sourceName, models]) => (
              <Card key={sourceName}>
                <CardHeader className="flex-row items-center justify-between py-4">
                  <CardTitle className="flex items-center gap-2 text-sm">
                    {sourceName}
                    <Badge variant="muted">{models.length}</Badge>
                  </CardTitle>
                </CardHeader>
                <CardContent>
                  <div className="grid gap-2 sm:grid-cols-2 lg:grid-cols-3">
                    {models.map((model) => (
                      <ModelCard key={`${model.sourceId}-${model.id}`} model={model} />
                    ))}
                  </div>
                </CardContent>
              </Card>
            ))}
          </div>
        )}
      </AsyncState>
    </div>
  )
}

function ModelCard({ model }: { model: Model }) {
  const dimmed = !model.available
  return (
    <div
      className={cn(
        'rounded-xl border border-border/70 bg-background/40 p-3 transition-colors',
        dimmed && 'opacity-55',
      )}
    >
      <div className="flex items-start justify-between gap-2">
        <div className="min-w-0">
          <p className="truncate text-sm font-medium" title={model.id}>
            {model.name || model.id}
          </p>
          <p className="truncate font-mono text-xs text-muted-foreground" title={model.id}>
            {model.id}
          </p>
        </div>
        <PlatformBadge platform={model.platform} />
      </div>
      <div className="mt-2.5 flex flex-wrap items-center gap-1.5">
        <ModelTypeBadge type={model.type} />
        {model.maxTokens > 0 && <Badge variant="outline">{formatNumber(model.maxTokens)} tok</Badge>}
        {model.visionCapable && (
          <Badge variant="secondary">
            <Eye className="h-3 w-3" /> 视觉
          </Badge>
        )}
        {model.toolsCapable && (
          <Badge variant="secondary">
            <Wrench className="h-3 w-3" /> 工具
          </Badge>
        )}
        {!model.available && <Badge variant="muted">不可用</Badge>}
      </div>
      <p className="mt-2 text-[11px] text-muted-foreground">检测于 {formatRelative(model.lastCheckedAt)}</p>
    </div>
  )
}
