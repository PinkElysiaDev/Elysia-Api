import { useCallback, useEffect, useMemo, useState } from 'react'
import { AlertTriangle, CheckCircle2, Copy, FileJson, Pencil, Plus, Sparkles, Trash2 } from 'lucide-react'
import { PageHeader } from '@/components/page-header'
import { AsyncState } from '@/components/ui/states'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { ToolbarSummary } from '@/components/toolbar-summary'
import { SearchInput } from '@/components/ui/search-input'
import { useConfirm } from '@/components/ui/confirm-dialog'
import { useApiAction } from '@/lib/use-api-action'
import { useToast } from '@/components/ui/use-toast'
import { api, ApiError } from '@/lib/api'
import { useModels, useSources } from '@/lib/hooks'
import { formatRelative } from '@/lib/utils'
import type { CustomProtocolConfig, CustomProtocolSummary } from '@/lib/types'
import { AssistantDialog } from './assistant-panel'
import { ProtocolFormDialog } from './protocol-form-dialog'
import { protocolTypeLabel } from './schema'
import { useCustomProtocolSchema } from './schema'

function newProtocolDraft(): CustomProtocolConfig {
  return {
    id: `my-protocol-${Math.random().toString(36).slice(2, 6)}`,
    type: 'llm',
    request: {
      method: 'POST',
      path: '',
      body: {
        model: { field: 'model', mode: 'string' },
        messages: { field: 'messages', mode: 'json' },
        stream: { field: 'stream', mode: 'json', omitIfEmpty: true },
      },
      auth: { mode: '' },
    },
    response: {
      body: {
        output: { text: { field: 'text', value: '示例文本' } },
        finish: { field: 'stop_reason', value: 'stop' },
      },
    },
  }
}

export function ProtocolDesignerPage() {
  const { success: toastSuccess } = useToast()
  const { confirm, dialog } = useConfirm()
  const { run } = useApiAction()
  const schema = useCustomProtocolSchema()
  const { data: sources = [] } = useSources()
  const { data: models = [] } = useModels()

  const [items, setItems] = useState<CustomProtocolSummary[] | null>(null)
  const [loadError, setLoadError] = useState<string | null>(null)
  const [keyword, setKeyword] = useState('')
  const [formOpen, setFormOpen] = useState(false)
  const [editing, setEditing] = useState<CustomProtocolConfig | null>(null)
  const [isNew, setIsNew] = useState(false)
  const [assistantOpen, setAssistantOpen] = useState(false)

  const refresh = useCallback(async () => {
    try {
      setItems(await api.listCustomProtocols())
      setLoadError(null)
    } catch (error) {
      setLoadError(error instanceof ApiError ? error.message : String(error))
    }
  }, [])

  useEffect(() => {
    void refresh()
  }, [refresh])

  // 预置协议以 metadata.preset 标记（首次启动播种的四线制定义）。
  const isPreset = (summary: CustomProtocolSummary) => !!summary.config?.metadata?.preset

  // 复制为新协议：剥离预置标记，ID 取首个未占用的副本名（-copy、-copy-2…），
  // 避免静默覆盖已存在的副本定制。
  const copyAsNew = (summary: CustomProtocolSummary) => {
    const taken = new Set((items ?? []).map((item) => item.id))
    let candidate = `${summary.config.id}-copy`
    for (let suffix = 2; taken.has(candidate); suffix += 1) {
      candidate = `${summary.config.id}-copy-${suffix}`
    }
    const config: CustomProtocolConfig = {
      ...summary.config,
      id: candidate,
      metadata: { ...summary.config.metadata, preset: undefined },
    }
    setEditing(config)
    setIsNew(true)
    setFormOpen(true)
  }

  const filtered = useMemo(() => {
    const kw = keyword.trim().toLowerCase()
    if (!kw) return items ?? []
    return (items ?? []).filter((item) => `${item.id} ${item.name ?? ''}`.toLowerCase().includes(kw))
  }, [items, keyword])

  const validCount = useMemo(() => (items ?? []).filter((item) => item.valid).length, [items])

  const openCreate = () => {
    setEditing(newProtocolDraft())
    setIsNew(true)
    setFormOpen(true)
  }

  const openEdit = (summary: CustomProtocolSummary) => {
    setEditing(summary.config)
    setIsNew(false)
    setFormOpen(true)
  }

  const remove = async (summary: CustomProtocolSummary) => {
    const ok = await confirm({
      title: `删除协议 ${summary.id}？`,
      description: `使用 custom:${summary.id} 平台的模型源会失效。`,
      confirmText: '删除',
    })
    if (!ok) return
    await run(
      `delete:${summary.id}`,
      async () => {
        await api.deleteCustomProtocol(summary.id)
        await refresh()
        toastSuccess('协议已删除', summary.id)
      },
      { errorTitle: '删除失败' },
    )
  }

  return (
    <div className="relative z-[1] space-y-6">
      <PageHeader
        title="协议设计器"
        actions={
          <>
            <Button variant="ghost" onClick={() => setAssistantOpen(true)}>
              <Sparkles className="h-4 w-4" /> AI 助手
            </Button>
            <Button variant="primary" onClick={openCreate}>
              <Plus className="h-4 w-4" /> 新建协议
            </Button>
          </>
        }
      />

      <div className="flex flex-wrap items-center justify-between gap-3 py-1">
        <SearchInput
          className="w-full text-xs sm:w-72"
          ariaLabel="搜索协议"
          placeholder="搜索协议 ID / 名称…"
          value={keyword}
          onChange={setKeyword}
        />
        <ToolbarSummary
          items={[
            { label: '有效', value: validCount, tone: 'jade' },
            { label: '校验失败', value: (items ?? []).length - validCount, tone: 'ember' },
          ]}
          total={(items ?? []).length}
          unit="个协议"
        />
      </div>

      <AsyncState
        isLoading={items === null && !loadError}
        error={loadError}
        data={items ?? undefined}
        onRetry={() => void refresh()}
        loadingColumns={6}
        emptyIcon={<FileJson className="h-7 w-7" />}
        emptyTitle="暂无自定义协议"
        emptyDescription="新建协议从零构造请求体与字段映射，或用 AI 助手从 API 文档直接生成。"
        emptyAction={
          <Button variant="primary" onClick={openCreate}>
            <Plus className="h-4 w-4" /> 新建协议
          </Button>
        }
      >
        {() => (
          <div className="overflow-x-auto">
            <table className="w-full text-sm">
              <TableHeader className="bg-secondary/20">
                <TableRow className="border-b border-border/60 hover:bg-transparent">
                  <TableHead className="py-3.5">协议 ID</TableHead>
                  <TableHead className="py-3.5">名称</TableHead>
                  <TableHead className="py-3.5 text-center">类型</TableHead>
                  <TableHead className="py-3.5 text-center">校验</TableHead>
                  <TableHead className="py-3.5 text-center">版本</TableHead>
                  <TableHead className="py-3.5 text-center">更新时间</TableHead>
                  <TableHead className="py-3.5 pr-5 text-right font-semibold text-2xs uppercase tracking-wider text-muted-foreground">操作</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody className="divide-y divide-border/30">
                {filtered.map((summary) => (
                    <TableRow key={summary.id} className="cursor-pointer" onClick={() => openEdit(summary)}>
                      <TableCell className="py-3 font-mono text-xs">
                        {summary.id}
                        {isPreset(summary) && (
                          <Badge variant="secondary" className="ml-1.5 px-1.5 py-0 text-2xs">预置</Badge>
                        )}
                      </TableCell>
                      <TableCell className="max-w-48 truncate py-3 text-xs text-muted-foreground">
                        {summary.name || '—'}
                      </TableCell>
                      <TableCell className="py-3 text-center">
                        <Badge variant="outline" className="px-2 py-0 text-2xs">
                          {protocolTypeLabel(summary.type)}
                        </Badge>
                      </TableCell>
                      <TableCell className="py-3 text-center">
                        {summary.valid ? (
                          <span className="inline-flex items-center gap-1 text-2xs text-success">
                            <CheckCircle2 className="h-3.5 w-3.5" /> 有效
                          </span>
                        ) : (
                          <span
                            className="inline-flex items-center gap-1 text-2xs text-destructive"
                            title={summary.error}
                          >
                            <AlertTriangle className="h-3.5 w-3.5" /> 校验失败
                          </span>
                        )}
                      </TableCell>
                      <TableCell className="py-3 text-center font-mono text-xs text-muted-foreground">
                        {summary.version || '—'}
                      </TableCell>
                      <TableCell className="py-3 text-center text-xs text-muted-foreground">
                        {summary.updatedAt ? formatRelative(summary.updatedAt) : '—'}
                      </TableCell>
                      <TableCell className="py-3 pr-5 text-right">
                        <span className="inline-flex items-center gap-1" onClick={(event) => event.stopPropagation()}>
                          <Button variant="ghost" size="iconSm" aria-label="编辑" onClick={() => openEdit(summary)}>
                            <Pencil className="h-3.5 w-3.5" />
                          </Button>
                          <Button variant="ghost" size="iconSm" aria-label="复制为新协议" title="复制为新协议" onClick={() => copyAsNew(summary)}>
                            <Copy className="h-3.5 w-3.5" />
                          </Button>
                          <Button variant="ghost" size="iconSm" aria-label="删除" onClick={() => void remove(summary)}>
                            <Trash2 className="h-3.5 w-3.5" />
                          </Button>
                        </span>
                      </TableCell>
                    </TableRow>
                  ))}
              </TableBody>
            </table>
          </div>
        )}
      </AsyncState>

      <ProtocolFormDialog
        open={formOpen}
        onOpenChange={setFormOpen}
        protocol={editing}
        schema={schema}
        isNew={isNew}
        onSaved={refresh}
      />

      <AssistantDialog
        open={assistantOpen}
        onOpenChange={setAssistantOpen}
        sources={sources}
        models={models}
        protocolType={editing?.type || 'llm'}
        currentConfig={formOpen ? editing : null}
        onApplyDraft={(config) => {
          setAssistantOpen(false)
          setEditing(config)
          setIsNew(true)
          setFormOpen(true)
        }}
      />
      {dialog}
    </div>
  )
}
