import { useCallback, useEffect, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { PageHeader } from '@/components/page-header'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogTitle } from '@/components/ui/dialog'
import { protocolAPI, type ProtocolHistoryDetail, type ProtocolHistoryItem, type ProtocolReference } from '@/lib/protocol-v2'

const reasonLabel = (reason: string) => reason === 'preset_replaced' ? '预置旧版本' : '已删除自定义协议'
export function ReferenceList({ items }: { items: ProtocolReference[] }) {
  const labels: Record<string, string> = { source: '模型源', model: '模型', group: '模型组', job: '持久任务', session: 'Agent 编辑会话', draft: '当前草稿', active: '当前启用', in_flight: '进行中的请求或会话' }
  return <ul className="space-y-1 text-sm">{items.map((item, index) => <li key={`${item.kind}/${item.sourceId}/${item.id}/${index}`}>{labels[item.kind] ?? item.kind} · {item.name || [item.sourceId, item.id].filter(Boolean).join(' / ')}</li>)}</ul>
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
  const refresh = useCallback(async () => { setItems(await protocolAPI.history()); setLoaded(true) }, [])
  useEffect(() => { void refresh().catch((err: unknown) => setError(String(err))) }, [refresh])
  const inspect = async (id: string) => {
    setBusy(true); setError('')
    try { setDetail(await protocolAPI.historyDetail(id)) } catch (err) { setError(String(err)) } finally { setBusy(false) }
  }
  const run = async () => {
    if (!detail || busy) return
    setBusy(true); setError(''); setNotice(''); setRestoredID('')
    try {
      if (action === 'restore') {
        const result = await protocolAPI.restore(detail.item.id, newID.trim(), newName.trim())
        setRestoredID(result.protocolId)
        setNotice(result.activated ? `已创建并启用 ${result.protocolId}，可在模型源中选择。` : `已保存新草稿 ${result.protocolId}，验证未通过：${(result.issues ?? []).map((issue) => `${issue.path} ${issue.reason}`).join('；')}`)
      } else {
        await protocolAPI.deleteHistory(detail.item.id)
        setDetail(undefined); setNotice('该历史版本已彻底删除。')
      }
      setAction(undefined); await refresh()
    } catch (err) { setError(String(err)) } finally { setBusy(false) }
  }
  const visible = items.filter((item) => (filter === 'all' || item.reason === filter) && `${item.name} ${item.protocolId} ${item.hash}`.toLowerCase().includes(query.toLowerCase()))
  return <div className="space-y-6">
    <PageHeader title="协议历史" actions={<Button onClick={() => navigate('/protocols')}>返回协议设计器</Button>} />
    <p className="text-sm text-muted-foreground">查看被更新替换的预置版本和已删除的自定义协议。恢复将创建独立的新协议。</p>
    {error && !action && <p role="alert" className="text-destructive">{error}</p>}
    {notice && <div role="status" className="rounded-lg border p-3 text-sm">{notice}{restoredID && <Button variant="ghost" onClick={() => navigate('/protocols', { state: { protocolId: restoredID } })}>查看新协议</Button>}</div>}
    <div className="flex flex-wrap gap-3">
      <label className="text-sm">类型 <select aria-label="历史类型" value={filter} onChange={(event) => setFilter(event.target.value)} className="rounded-md border bg-card p-2"><option value="all">全部</option><option value="preset_replaced">预置旧版本</option><option value="custom_deleted">已删除自定义协议</option></select></label>
      <Input aria-label="搜索协议历史" placeholder="搜索名称、ID 或版本哈希" value={query} onChange={(event) => setQuery(event.target.value)} className="max-w-sm" />
      <Button disabled={busy} variant="ghost" onClick={() => void refresh().catch((err: unknown) => setError(String(err)))}>刷新</Button>
    </div>
    {!loaded ? <p role="status">正在读取历史…</p> : visible.length === 0 ? <p className="text-muted-foreground">暂无匹配的历史版本。</p> : <div className="overflow-x-auto rounded-lg border"><table className="w-full text-left text-sm"><thead className="bg-muted/40"><tr><th className="p-3">协议</th><th>版本</th><th>来源</th><th>归档时间</th><th className="p-3">操作</th></tr></thead><tbody>{visible.map((item) => <tr key={item.id} className="border-t"><td className="p-3"><div>{item.name || item.protocolId}</div><div className="font-mono text-xs text-muted-foreground">{item.protocolId}</div></td><td><div>{item.version || '—'}{item.isDraft ? ' · 草稿' : ''}</div><code className="text-xs">{item.hash.slice(0, 12)}</code></td><td>{reasonLabel(item.reason)}</td><td>{new Date(item.archivedAt).toLocaleString()}</td><td className="p-3"><Button disabled={busy} onClick={() => void inspect(item.id)}>查看版本</Button></td></tr>)}</tbody></table></div>}
    {detail && <section aria-label="历史版本详情" className="space-y-4 rounded-lg border bg-card p-4">
      <div className="flex flex-wrap items-center justify-between gap-3"><div><h2 className="font-medium">{detail.item.name || detail.item.protocolId}</h2><p className="break-all font-mono text-xs text-muted-foreground">{detail.item.hash}</p></div><div className="flex gap-2">
        <Button disabled={busy} onClick={() => { setNewID(`${detail.item.protocolId}-restored-${detail.item.hash.slice(0, 8)}`); setNewName(`${detail.item.name || detail.item.protocolId}（恢复）`); setError(''); setAction('restore') }}>恢复为新协议</Button>
        <Button disabled={busy || detail.references.length > 0 || detail.item.reason === 'preset_replaced'} variant="destructive" onClick={() => { setConfirmation(''); setError(''); setAction('delete') }}>彻底删除</Button>
      </div></div>
      <p className="text-sm">验证：{detail.report?.definitionHash ? detail.report.passed ? `已通过 · 编译器 ${detail.report.compilerVersion}` : '未通过' : '暂无验证记录'}。恢复时会重新验证。</p>
      {(detail.report?.issues?.length ?? 0) > 0 && <ul className="text-sm text-destructive">{detail.report!.issues.map((issue, index) => <li key={index}>{issue.path} · {issue.reason}</li>)}</ul>}
      {detail.references.length > 0 && <div className="space-y-2 rounded-md bg-muted p-3"><p className="text-sm font-medium">以下引用阻止彻底删除</p><ReferenceList items={detail.references} /></div>}
      {detail.changes && <details><summary className="cursor-pointer text-sm">与当前启用版本的差异（{detail.changes.length} 项）</summary><pre className="max-h-64 overflow-auto p-3 text-xs">{JSON.stringify(detail.changes, null, 2)}</pre></details>}
      <details open><summary className="cursor-pointer text-sm">完整协议定义</summary><pre className="mt-2 max-h-96 overflow-auto whitespace-pre-wrap break-all rounded-md bg-muted p-3 text-xs">{detail.item.definition}</pre></details>
    </section>}
    <Dialog open={!!action} onOpenChange={(open) => { if (!open && !busy) setAction(undefined) }}><DialogContent hideClose={busy}>
      <DialogTitle>{action === 'restore' ? '恢复为新协议' : '彻底删除历史版本'}</DialogTitle>
      <DialogDescription>{action === 'restore' ? '创建一个可编辑的独立协议，验证通过后启用。现有模型源的绑定保持不变。' : '此操作不可恢复，将删除该历史定义及其专属验证记录。既有备份和调用日志会保留。'}</DialogDescription>
      {action === 'restore' ? <><label className="space-y-1 text-sm">新协议 ID<Input value={newID} onChange={(event) => setNewID(event.target.value)} disabled={busy} /></label><label className="space-y-1 text-sm">协议名称<Input value={newName} onChange={(event) => setNewName(event.target.value)} disabled={busy} /></label></> : <label className="space-y-1 text-sm">输入原协议 ID「{detail?.item.protocolId}」确认<Input aria-label="确认删除协议 ID" value={confirmation} onChange={(event) => setConfirmation(event.target.value)} disabled={busy} /></label>}
      {error && <p role="alert" className="text-sm text-destructive">{error}</p>}
      <DialogFooter><Button disabled={busy} variant="ghost" onClick={() => setAction(undefined)}>取消</Button><Button disabled={busy || (action === 'restore' ? !newID.trim() : confirmation !== detail?.item.protocolId)} variant={action === 'delete' ? 'destructive' : 'primary'} onClick={() => void run()}>{busy ? '处理中…' : action === 'restore' ? '创建并验证启用' : '确认彻底删除'}</Button></DialogFooter>
    </DialogContent></Dialog>
  </div>
}
