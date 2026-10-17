import { useCallback, useEffect, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { AlertTriangle, ArrowLeft, Eye, RefreshCw, RotateCcw, Trash2 } from 'lucide-react'
import { PageHeader } from '@/components/page-header'
import { RoleWatermark } from '@/components/role-watermark'
import { ToolbarSummary } from '@/components/toolbar-summary'
import { CapChip } from '@/components/badges'
import { Button } from '@/components/ui/button'
import { Seg } from '@/components/ui/seg'
import { EmptyText, LoadingState } from '@/components/ui/states'
import { Input } from '@/components/ui/input'
import { TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import {
  protocolAPI,
  type ProtocolHistoryDetail,
  type ProtocolHistoryItem,
  type ProtocolReference,
} from '@/lib/protocol-v2'
import { cn, formatDateTime } from '@/lib/utils'

const REASON_LABELS: Record<string, string> = {
  preset_replaced: '预置旧版本',
  identity_recovered: '历史身份恢复',
  preset_repaired: '预置损坏原文',
  preset_draft_replaced: '预置原草稿',
  custom_deleted: '已删除自定义协议',
}

const HISTORY_FILTERS = [
  { value: 'all', label: '全部' },
  { value: 'preset_replaced', label: '预置旧版本' },
  { value: 'custom_deleted', label: '已删除' },
]

const REFERENCE_KIND_LABELS: Record<string, string> = {
  source: '模型源',
  model: '模型',
  group: '模型组',
  job: '持久任务',
  session: 'Agent 编辑会话',
  draft: '当前草稿',
  active: '当前启用',
  in_flight: '进行中的请求或会话',
}

export function ReferenceList({ items }: { items: ProtocolReference[] }) {
  return (
    <ul className="space-y-1 text-xs text-muted-foreground">
      {items.map((item, index) => (
        <li
          key={`${item.kind}/${item.sourceId}/${item.id}/${index}`}
          className="flex items-center gap-1.5"
        >
          <span className="h-1 w-1 rounded-full bg-muted-foreground/60" aria-hidden />
          <span className="font-medium text-foreground">
            {REFERENCE_KIND_LABELS[item.kind] ?? item.kind}
          </span>
          <span>·</span>
          <span className="font-mono">
            {item.name || [item.sourceId, item.id].filter(Boolean).join(' / ')}
          </span>
        </li>
      ))}
    </ul>
  )
}

export function ProtocolHistoryPage() {
  const navigate = useNavigate()
  const [items, setItems] = useState<ProtocolHistoryItem[]>([])
  const [loaded, setLoaded] = useState(false)
  const [filter, setFilter] = useState('all')
  const [query, setQuery] = useState('')
  const [detail, setDetail] = useState<ProtocolHistoryDetail>()
  const [action, setAction] = useState<'restore' | 'delete'>()
  const [newID, setNewID] = useState('')
  const [newName, setNewName] = useState('')
  const [confirmation, setConfirmation] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const [restoredID, setRestoredID] = useState('')

  const refresh = useCallback(async () => {
    setItems(await protocolAPI.history())
    setLoaded(true)
  }, [])

  useEffect(() => {
    void refresh().catch((err: unknown) => setError(String(err)))
  }, [refresh])

  const inspect = async (id: string) => {
    setBusy(true)
    setError('')
    try {
      setDetail(await protocolAPI.historyDetail(id))
    } catch (err) {
      setError(String(err))
    } finally {
      setBusy(false)
    }
  }

  const run = async () => {
    if (!detail || busy) return
    setBusy(true)
    setError('')
    setNotice('')
    setRestoredID('')
    try {
      if (action === 'restore') {
        const result = await protocolAPI.restore(detail.item.id, newID.trim(), newName.trim())
        setRestoredID(result.protocolId)
        setNotice(
          result.activated
            ? `已创建并启用 ${result.protocolId}，可在模型源中选择。`
            : `已保存新草稿 ${result.protocolId}，验证未通过：${(result.issues ?? []).map((issue) => `${issue.path} ${issue.reason}`).join('；')}`,
        )
      } else {
        await protocolAPI.deleteHistory(detail.item.id)
        setDetail(undefined)
        setNotice('该历史版本已彻底删除。')
      }
      setAction(undefined)
      await refresh()
    } catch (err) {
      setError(String(err))
    } finally {
      setBusy(false)
    }
  }

  const visible = items.filter(
    (item) =>
      (filter === 'all' || item.reason === filter) &&
      `${item.name} ${item.protocolId} ${item.hash}`.toLowerCase().includes(query.toLowerCase()),
  )

  return (
    <>
      <RoleWatermark className="-right-8 top-0 opacity-[0.05] dark:opacity-[0.08]" />

      <div className="relative z-[1] space-y-6">
        <PageHeader
          title="协议历史"
          actions={
            <>
              <Button
                disabled={busy}
                variant="ghost"
                onClick={() => void refresh().catch((err: unknown) => setError(String(err)))}
              >
                <RefreshCw className={cn('h-4 w-4', busy && 'animate-spin')} aria-hidden />
                刷新
              </Button>
              <Button onClick={() => navigate('/protocols')}>
                <ArrowLeft className="h-4 w-4" aria-hidden />
                返回协议设计器
              </Button>
            </>
          }
        />

        {error && !action && (
          <div
            role="alert"
            className="flex items-start gap-2.5 rounded-xl border border-destructive/30 bg-destructive/5 px-4 py-3 text-sm text-destructive"
          >
            <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0" aria-hidden />
            <span>{error}</span>
          </div>
        )}

        {notice && (
          <div
            role="status"
            className="flex flex-wrap items-center justify-between gap-3 rounded-xl border border-border/70 bg-card px-4 py-3 text-sm shadow-soft"
          >
            <span>{notice}</span>
            {restoredID && (
              <Button
                size="sm"
                variant="ghost"
                onClick={() => navigate('/protocols', { state: { protocolId: restoredID } })}
              >
                查看新协议
              </Button>
            )}
          </div>
        )}

        <div className="flex flex-wrap items-center justify-between gap-3 py-1">
          <div className="flex flex-wrap items-center gap-3">
            <span className="text-2xs font-semibold uppercase tracking-wider text-muted-foreground">
              类型
            </span>
            {/* 筛选与搜索成组不换行：窄屏整组下移，搜索框始终贴在筛选右侧 */}
            <div className="flex min-w-0 items-center gap-3">
              <Seg
                aria-label="历史类型"
                options={HISTORY_FILTERS}
                value={filter}
                onChange={setFilter}
              />
              <Input
                aria-label="搜索协议历史"
                placeholder="搜索名称、ID 或版本哈希"
                value={query}
                onChange={(event) => setQuery(event.target.value)}
                className="w-56 min-w-0 text-xs"
              />
            </div>
          </div>

          <ToolbarSummary
            items={[
              {
                label: '预置旧版',
                value: items.filter((item) => item.reason === 'preset_replaced').length,
              },
              {
                label: '已删除',
                value: items.filter((item) => item.reason === 'custom_deleted').length,
              },
            ]}
            total={items.length}
            unit="个归档版本"
          />
        </div>

        {!loaded ? (
          <div className="space-y-3">
            <p role="status" className="text-xs text-muted-foreground">
              正在读取历史…
            </p>
            <LoadingState rows={3} columns={5} />
          </div>
        ) : visible.length === 0 ? (
          <EmptyText className="py-12 text-center">暂无匹配的历史版本</EmptyText>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full text-sm">
              <TableHeader>
                <TableRow className="border-b border-border/50 hover:bg-transparent">
                  <TableHead className="py-3.5 pl-4">协议</TableHead>
                  <TableHead className="py-3.5 text-center">版本</TableHead>
                  <TableHead className="py-3.5 text-center">来源</TableHead>
                  <TableHead className="py-3.5 text-center">归档时间</TableHead>
                  <TableHead className="py-3.5 pr-4 text-right">操作</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody className="divide-y divide-border/30">
                {visible.map((item) => (
                  <TableRow key={item.id} className="border-b-0">
                    <TableCell className="py-3.5 pl-4">
                      <div className="font-medium text-foreground">
                        {item.name || item.protocolId}
                      </div>
                      <div className="font-mono text-2xs text-muted-foreground">
                        {item.protocolId}
                      </div>
                    </TableCell>
                    <TableCell className="py-3.5 text-center">
                      <div className="text-xs text-muted-foreground">
                        {item.version || '—'}
                        {item.isDraft ? ' · 草稿' : ''}
                      </div>
                      <code className="mt-0.5 inline-block rounded border border-border/70 bg-secondary/35 px-1.5 py-0.5 font-mono text-2xs text-secondary-foreground">
                        {item.hash.slice(0, 12)}
                      </code>
                    </TableCell>
                    <TableCell className="py-3.5 whitespace-nowrap text-center">
                      <CapChip className="whitespace-nowrap">
                        {REASON_LABELS[item.reason] ?? '已删除自定义协议'}
                      </CapChip>
                    </TableCell>
                    <TableCell className="py-3.5 whitespace-nowrap text-center font-mono text-xs text-muted-foreground">
                      {item.archivedAt ? formatDateTime(item.archivedAt) : '—'}
                    </TableCell>
                    <TableCell className="py-3.5 pr-4 whitespace-nowrap text-right">
                      <Button size="sm" disabled={busy} onClick={() => void inspect(item.id)}>
                        <Eye className="h-3.5 w-3.5" aria-hidden />
                        查看版本
                      </Button>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </table>
          </div>
        )}

        {detail && (
          <section
            aria-label="历史版本详情"
            className="space-y-4 border-t border-border/50 pt-6"
          >
            <div className="flex flex-wrap items-start justify-between gap-3 border-b border-border/50 pb-4">
              <div>
                <h2 className="font-display text-base font-semibold text-foreground">
                  {detail.item.name || detail.item.protocolId}
                </h2>
                <p className="mt-0.5 break-all font-mono text-xs text-muted-foreground">
                  {detail.item.hash}
                </p>
              </div>
              <div className="flex flex-wrap gap-2">
                <Button
                  disabled={busy}
                  onClick={() => {
                    setNewID(`${detail.item.protocolId}-restored-${detail.item.hash.slice(0, 8)}`)
                    setNewName(`${detail.item.name || detail.item.protocolId}（恢复）`)
                    setError('')
                    setAction('restore')
                  }}
                >
                  <RotateCcw className="h-3.5 w-3.5" aria-hidden />
                  恢复为新协议
                </Button>
                <Button
                  disabled={busy || detail.references.length > 0}
                  variant="destructive"
                  onClick={() => {
                    setConfirmation('')
                    setError('')
                    setAction('delete')
                  }}
                >
                  <Trash2 className="h-3.5 w-3.5" aria-hidden />
                  彻底删除
                </Button>
              </div>
            </div>

            <p className="text-xs text-muted-foreground">
              验证：
              {detail.report?.definitionHash
                ? detail.report.passed
                  ? `已通过 · 编译器 ${detail.report.compilerVersion}`
                  : '未通过'
                : '暂无验证记录'}
              。恢复时会重新验证。
            </p>

            {(detail.report?.issues?.length ?? 0) > 0 && (
              <ul className="space-y-1 text-xs text-destructive">
                {detail.report!.issues.map((issue, index) => (
                  <li key={index} className="font-mono">
                    {issue.path} · {issue.reason}
                  </li>
                ))}
              </ul>
            )}

            {detail.references.length > 0 && (
              <div className="space-y-2 border-l-2 border-primary/50 pl-3.5">
                <p className="text-xs font-semibold text-foreground">以下引用阻止彻底删除</p>
                <ReferenceList items={detail.references} />
              </div>
            )}

            {detail.changes && (
              <details className="border-t border-border/40 pt-3.5">
                <summary className="cursor-pointer text-xs font-medium text-foreground">
                  与当前启用版本的差异（{detail.changes.length} 项）
                </summary>
                <pre className="mt-2 max-h-64 overflow-auto rounded-md bg-[var(--code-bg)] p-3 font-mono text-xs">
                  {JSON.stringify(detail.changes, null, 2)}
                </pre>
              </details>
            )}

            {detail.item.readError && (
              <p role="alert" className="text-sm text-destructive">
                原始记录无法解析：{detail.item.readError}。原文保留如下。
              </p>
            )}

            <details open className="border-t border-border/40 pt-3.5">
              <summary className="cursor-pointer text-xs font-medium text-foreground">
                完整协议定义
              </summary>
              <pre className="mt-2 max-h-96 overflow-auto whitespace-pre-wrap break-all rounded-md bg-[var(--code-bg)] p-3 font-mono text-xs">
                {detail.item.rawDefinition ?? detail.item.definition}
              </pre>
            </details>
          </section>
        )}

        <Dialog
          open={!!action}
          onOpenChange={(open) => {
            if (!open && !busy) setAction(undefined)
          }}
        >
          <DialogContent hideClose={busy}>
            <DialogHeader>
              <DialogTitle>
                {action === 'restore' ? '恢复为新协议' : '彻底删除历史版本'}
              </DialogTitle>
              <DialogDescription>
                {action === 'restore'
                  ? '创建一个可编辑的独立协议，验证通过后启用。现有模型源的绑定保持不变。'
                  : '此操作不可恢复，将删除该历史定义及其专属验证记录。既有备份和调用日志会保留。'}
              </DialogDescription>
            </DialogHeader>
            {action === 'restore' ? (
              <div className="space-y-3">
                <label className="block space-y-1.5 text-xs font-medium text-foreground">
                  <span>新协议 ID</span>
                  <Input
                    className="font-mono text-xs"
                    value={newID}
                    onChange={(event) => setNewID(event.target.value)}
                    disabled={busy}
                  />
                </label>
                <label className="block space-y-1.5 text-xs font-medium text-foreground">
                  <span>协议名称</span>
                  <Input
                    value={newName}
                    onChange={(event) => setNewName(event.target.value)}
                    disabled={busy}
                  />
                </label>
              </div>
            ) : (
              <label className="block space-y-1.5 text-xs font-medium text-foreground">
                <span>输入原协议 ID「{detail?.item.protocolId}」确认</span>
                <Input
                  aria-label="确认删除协议 ID"
                  className="font-mono text-xs"
                  value={confirmation}
                  onChange={(event) => setConfirmation(event.target.value)}
                  disabled={busy}
                />
              </label>
            )}
            {error && (
              <p role="alert" className="text-sm text-destructive">
                {error}
              </p>
            )}
            <DialogFooter>
              <Button disabled={busy} variant="ghost" onClick={() => setAction(undefined)}>
                取消
              </Button>
              <Button
                disabled={
                  busy ||
                  (action === 'restore'
                    ? !newID.trim()
                    : confirmation !== detail?.item.protocolId)
                }
                variant={action === 'delete' ? 'destructive' : 'primary'}
                onClick={() => void run()}
              >
                {busy
                  ? '处理中…'
                  : action === 'restore'
                    ? '创建并验证启用'
                    : '确认彻底删除'}
              </Button>
            </DialogFooter>
          </DialogContent>
        </Dialog>
      </div>
    </>
  )
}
