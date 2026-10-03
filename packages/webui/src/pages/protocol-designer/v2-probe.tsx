import { useState } from 'react'
import { Button } from '@/components/ui/button'
import { Input, Textarea } from '@/components/ui/input'
import { ProtocolDocument } from '@/lib/protocol-document'
import { protocolAPI, type VerificationReport } from '@/lib/protocol-v2'

/** Runs the same bounded target probe exposed to Agent, independently of activation. */
export function ProtocolProbe({ source, id, hash }: { source: string; id: string; hash?: string }) {
  const [baseUrl, setBaseUrl] = useState('')
  const [apiKey, setAPIKey] = useState('')
  const [operation, setOperation] = useState('')
  const [request, setRequest] = useState('{}')
  const [evidence, setEvidence] = useState<{ source: string; reports: VerificationReport[] }>()
  const [error, setError] = useState('')
  const [isBusy, setBusy] = useState(false)
  const reports = evidence?.source === source ? evidence.reports : []
  const run = async (work: () => Promise<VerificationReport[]>) => {
    setBusy(true); setError('')
    try { setEvidence({ source, reports: await work() }) }
    catch (failure) { setError(failure instanceof Error ? failure.message : String(failure)) }
    finally { setBusy(false) }
  }
  return <details className="space-y-3 rounded border p-3">
    <summary>真实上游验证：{reports.length ? (reports[0].passed ? '已通过一次目标契约检查' : '未通过') : '当前草稿未读取验证记录'}</summary>
    <p className="text-xs text-muted-foreground">执行所选生成或模型发现操作，目录分页受引擎限制。先通过离线验证；响应会经过当前映射和契约检查。结果只证明这个目标、操作及返回内容，不证明全部能力或缓存命中。</p>
    <div className="grid gap-3 md:grid-cols-2">
      <label className="text-sm">上游 Base URL<Input value={baseUrl} onChange={(event) => setBaseUrl(event.target.value)} /></label>
      <label className="text-sm">API key<Input type="password" autoComplete="off" value={apiKey} onChange={(event) => setAPIKey(event.target.value)} /></label>
      <label className="text-sm">操作 ID<Input value={operation} onChange={(event) => setOperation(event.target.value)} /></label>
    </div>
    <label className="block text-sm">语义请求 JSON（生成需包含 model；发现可填空对象）<Textarea rows={8} className="font-mono text-xs" value={request} onChange={(event) => setRequest(event.target.value)} /></label>
    <div className="flex gap-2">
      <Button disabled={isBusy || !operation || !baseUrl} onClick={() => void run(async () => { new ProtocolDocument(request); return [(await protocolAPI.probe(source, request, { operation, baseUrl, apiKey })).report] })}>发送目标验证</Button>
      <Button disabled={isBusy || !hash} onClick={() => void run(() => protocolAPI.upstreamReports(id, hash!))}>读取此修订的目标记录</Button>
    </div>
    {error && <p role="alert" className="text-sm text-destructive">{error}</p>}
    {!!reports.length && <pre className="max-h-80 overflow-auto text-xs">{JSON.stringify(reports, null, 2)}</pre>}
  </details>
}
