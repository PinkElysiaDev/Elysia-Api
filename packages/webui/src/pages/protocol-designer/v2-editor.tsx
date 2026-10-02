import { useEffect, useMemo, useRef, useState } from 'react'
import { ProtocolProbe } from './v2-probe'
import { Button } from '@/components/ui/button'
import { Input, Textarea } from '@/components/ui/input'
import { ApiError } from '@/lib/api'
import { ProtocolDocument } from '@/lib/protocol-document'
import { protocolAPI, type ConversionIssue, type ProtocolDraft, type ProtocolRevision, type ProtocolSchema, type VerificationReport } from '@/lib/protocol-v2'
import { ProtocolDirectionEditor, ProtocolOperationEditor } from './v2-operation-editor'

const sectionNames = [
  ['directions', '方向与映射'], ['operations', '传输与操作'], ['samples', '请求、响应与事件样例'],
  ['sessionSamples', '实时会话样例'], ['taskSamples', '异步任务样例'], ['native', '原生字段规则'], ['extensions', '扩展元数据'],
] as const
const emptyDefinition = () => JSON.stringify({ schemaVersion: 2, id: '', name: '', version: '1', family: '', wireVersion: '1', capabilities: {}, directions: {}, operations: {}, native: { preserve: false }, samples: [] }, null, 2)

/** Complete v2 editor. Raw JSON is the only editable source of truth. */
export function ProtocolV2Editor({ schema, draft, activeHash, initialSource, onSaved, onClose }: {
  schema: ProtocolSchema; draft?: ProtocolDraft; activeHash: string; initialSource?: string; onSaved: (id: string) => Promise<void>; onClose: () => void
}) {
  const [source, setSource] = useState(initialSource ?? draft?.definition ?? emptyDefinition)
  const [savedSource, setSavedSource] = useState(draft?.definition ?? '')
  const [draftHash, setDraftHash] = useState(draft?.hash ?? '')
  const [tab, setTab] = useState('basic')
  const [isBusy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [issues, setIssues] = useState<ConversionIssue[]>([])
  const [report, setReport] = useState<VerificationReport>()
  const [verifiedSource, setVerifiedSource] = useState('')
  const [revisions, setRevisions] = useState<ProtocolRevision[]>([])
  const [selectedRevision, setSelectedRevision] = useState('')
  const [direction, setDirection] = useState('decode_request')
  const [input, setInput] = useState('{}')
  const [isSequence, setSequence] = useState(false)
  const [previewMode, setPreviewMode] = useState('mapping')
  const [workflowSample, setWorkflowSample] = useState('')
  const [taskOperation, setTaskOperation] = useState('')
  const [taskKind, setTaskKind] = useState('decode')
  const [taskPurpose, setTaskPurpose] = useState('status')
  const [output, setOutput] = useState<unknown>()
  const [target, setTarget] = useState('{}')
  const [focusPath, setFocusPath] = useState('')
  const [pendingSections, setPendingSections] = useState<Record<string, { source: string; error: string }>>({})
  const jsonRef = useRef<HTMLTextAreaElement>(null)
  const parsed = useMemo(() => {
    try { return { document: new ProtocolDocument(source), error: '' } }
    catch (failure) { return { document: undefined, error: String(failure) } }
  }, [source])
  const document = parsed.document
  const scalar = (path: string): string => { const raw = document?.read(path); const value = raw && JSON.parse(raw); return typeof value === 'string' ? value : '' }
  const id = scalar('/id')
  const hasPendingSections = Object.keys(pendingSections).length > 0
  const isDirty = source !== savedSource || hasPendingSections
  const canActivate = !isDirty && source === verifiedSource && report?.passed && report.compilerVersion === schema.compilerVersion
  const change = (path: string, value: string) => { if (document) { setSource(document.set(path, value)); setError('') } }
  const editSection = (key: string, value: string) => {
    try {
      new ProtocolDocument(value)
      if (!document) throw new Error('请先修正完整 JSON')
      change('/' + key, value)
      setPendingSections((current) => { const next = { ...current }; delete next[key]; return next })
    } catch (failure) { setPendingSections((current) => ({ ...current, [key]: { source: value, error: String(failure) } })) }
  }

  const run = async (work: () => Promise<void>) => {
    setBusy(true); setError('')
    try { await work() }
    catch (failure) {
      setError(failure instanceof Error ? failure.message : String(failure))
      if (failure instanceof ApiError) {
        const details = failure.details as { issues?: ConversionIssue[] } | undefined
        if (details?.issues) setIssues(details.issues)
      }
    } finally { setBusy(false) }
  }
  const save = async () => {
    if (!document || !id || hasPendingSections) throw new Error('请填写协议 ID，并修正所有 JSON 片段')
    const result = await protocolAPI.save(id, source, draftHash)
    setDraftHash(result.draft.hash); setSavedSource(source); setIssues(result.issues ?? [])
    await onSaved(id)
    setRevisions(await protocolAPI.revisions(id))
  }
  const verify = async () => {
    if (isDirty || !draftHash) throw new Error('请先保存当前草稿，再验证这个版本')
    const result = await protocolAPI.verify(id, draftHash)
    setReport(result.report); setVerifiedSource(source); setIssues(result.report.issues ?? [])
    setRevisions(await protocolAPI.revisions(id))
  }
  const locate = (issue: ConversionIssue) => {
    let path = issue.path
    if (issue.evidence) {
      for (const section of ['samples', 'sessionSamples', 'taskSamples']) {
        const entries = document?.locate(`/${section}`)?.children
        for (const [index] of entries ?? []) if (scalar(`/${section}/${index}/id`) === issue.evidence) path = `/${section}/${index}`
      }
    }
    setFocusPath(path); setTab('json')
  }
  useEffect(() => {
    if (tab !== 'json' || !focusPath) return
    const document = parsed.document
    const location = document?.locate(focusPath)
    if (location && jsonRef.current) { jsonRef.current.focus(); jsonRef.current.setSelectionRange(location.start, location.end) }
  }, [tab, focusPath, parsed.document])
  useEffect(() => {
    if (!draft) return
    let isCurrent = true
    void protocolAPI.revisions(draft.protocolId).then((items) => { if (isCurrent) setRevisions(items) }).catch((failure: unknown) => { if (isCurrent) setError(String(failure)) })
    return () => { isCurrent = false }
  }, [draft])

  const capabilities = document?.locate('/capabilities')
  const mappings = document?.locate('/directions')
  return <section className="space-y-4 rounded-lg border border-border bg-card p-5" aria-label="协议版本编辑器">
    <div className="flex flex-wrap items-center justify-between gap-3">
      <div><h2 className="text-lg font-semibold">{id || '新协议'}</h2><p className="text-xs text-muted-foreground">{isDirty ? '草稿尚未保存' : '草稿已保存'} · {activeHash ? `启用版本 ${activeHash.slice(0, 12)}` : '尚未启用'}</p></div>
      <div className="flex flex-wrap gap-2">
        <Button disabled={isBusy || hasPendingSections} onClick={() => void run(save)}>保存草稿</Button>
        <Button disabled={isBusy || !document || hasPendingSections} onClick={() => void run(async () => { const result = await protocolAPI.validate(source); setIssues(result.issues ?? []); setOutput(result) })}>检查定义</Button>
        <Button disabled={isBusy || isDirty || !draftHash} onClick={() => void run(verify)}>离线验证</Button>
        <Button variant="primary" disabled={isBusy || !canActivate} onClick={() => void run(async () => { if (report) { await protocolAPI.activate(id, report.definitionHash, activeHash); await onSaved(id) } })}>启用版本</Button>
        <Button variant="ghost" disabled={isBusy || isDirty} onClick={onClose}>关闭编辑器</Button>
      </div>
    </div>
    {(error || parsed.error) && <p role="alert" className="text-sm text-destructive">{error || parsed.error}</p>}
    {hasPendingSections && <p role="alert" className="text-sm text-destructive">以下片段尚有语法错误，请修正后保存：{Object.keys(pendingSections).join('、')}</p>}
    <div role="tablist" aria-label="协议编辑选项" className="flex flex-wrap gap-2">
      {[['basic', '基本信息'], ...sectionNames, ['preview', '转换预览'], ['verification', '能力与验证'], ['revisions', '版本与回滚'], ['json', '完整 JSON']].map(([value, label]) => <Button key={value} role="tab" aria-selected={tab === value} variant={tab === value ? 'default' : 'ghost'} onClick={() => setTab(value)}>{label}</Button>)}
    </div>
    <fieldset disabled={isBusy} className="min-w-0 space-y-4">
      {tab === 'directions' && document && !hasPendingSections && <ProtocolDirectionEditor document={document} schema={schema} onChange={setSource} />}
      {tab === 'operations' && document && !hasPendingSections && <ProtocolOperationEditor document={document} schema={schema} onChange={setSource} />}
      {tab === 'basic' && <>
        <div className="grid gap-4 md:grid-cols-2">{[['id', '协议 ID'], ['name', '名称'], ['version', '定义版本'], ['family', '协议族'], ['wireVersion', '线格式版本']].map(([key, label]) => <label key={key} className="space-y-1 text-sm">{label}<Input value={scalar('/' + key)} disabled={!document || (key === 'id' && !!draftHash)} onChange={(event) => change('/' + key, JSON.stringify(event.target.value))} /></label>)}</div>
        <fieldset className="space-y-2"><legend className="text-sm font-medium">声明能力</legend><div className="flex flex-wrap gap-3">{schema.capabilities.map((capability) => <label key={capability} className="flex items-center gap-2 text-xs"><input type="checkbox" disabled={!capabilities} checked={document?.read(`/capabilities/${capability}`) === 'true'} onChange={(event) => change(`/capabilities/${capability}`, String(event.target.checked))} />{capability}</label>)}</div></fieldset>
        <p className="text-xs text-muted-foreground">每个方向的映射与样例必须证明所声明的能力。新增能力后重新验证才能启用。</p>
      </>}
      {sectionNames.map(([key, label]) => tab === key && <div key={key} className="space-y-2"><label className="block text-sm">{label} JSON<Textarea aria-label={`${label} JSON`} rows={16} className="font-mono text-xs" value={pendingSections[key]?.source ?? document?.read('/' + key) ?? (key.includes('Samples') || key === 'samples' ? '[]' : '{}')} onChange={(event) => editSection(key, event.target.value)} /></label>{pendingSections[key] && <p className="text-sm text-destructive">{pendingSections[key].error}</p>}</div>)}
      {tab === 'directions' && <details><summary>引擎支持的方向与映射操作</summary><p className="text-xs">{schema.directions.join(' · ')}</p><ul className="space-y-1 text-xs">{schema.mappingOperations.map((operation) => <li key={operation.name}><code>{operation.name}</code>：{operation.description}</li>)}</ul><p className="text-xs">可复用模块：{schema.modules.map((module) => `${module.name} (${module.directions.join(', ')})`).join('；')}</p></details>}
      {tab === 'operations' && <p className="text-xs text-muted-foreground">操作定义包含 method、path、transport、auth，以及可选的 session 或 task。支持 HTTP JSON、SSE、NDJSON、WebSocket；任务流声明 submit/status/result/cancel。未安装的引擎能力会在检查定义时报告。</p>}
      {tab === 'json' && <label className="block space-y-2 text-sm">完整协议 JSON<Textarea ref={jsonRef} aria-label="完整协议 JSON" rows={22} className="font-mono text-xs" value={source} onChange={(event) => setSource(event.target.value)} />{focusPath && <span className="text-xs text-muted-foreground">定位：{focusPath}</span>}</label>}
      {tab === 'preview' && <>
        <label className="block text-sm">预览类型 <select aria-label="预览类型" className="rounded border bg-card p-2" value={previewMode} onChange={(event) => setPreviewMode(event.target.value)}><option value="mapping">请求 / 响应 / 事件</option><option value="session">完整双向会话</option><option value="task">任务操作</option></select></label>
        {previewMode === 'session' && <label className="block text-sm">会话样例 ID<Input value={workflowSample} onChange={(event) => setWorkflowSample(event.target.value)} /></label>}
        {previewMode === 'task' && <div className="grid gap-3 md:grid-cols-3"><label>提交操作名<Input value={taskOperation} onChange={(event) => setTaskOperation(event.target.value)} /></label><label>任务映射<select className="block rounded border bg-card p-2" value={taskKind} onChange={(event) => setTaskKind(event.target.value)}>{['decode', 'encode', 'control'].map((kind) => <option key={kind}>{kind}</option>)}</select></label><label>任务阶段<select className="block rounded border bg-card p-2" value={taskPurpose} onChange={(event) => setTaskPurpose(event.target.value)}>{['submit', 'status', 'result', 'cancel'].map((purpose) => <option key={purpose}>{purpose}</option>)}</select></label></div>}
        <div className="flex flex-wrap items-center gap-3"><label>转换方向 <select aria-label="转换方向" className="rounded border bg-card p-2" value={direction} onChange={(event) => setDirection(event.target.value)}>{schema.directions.map((value) => <option key={value}>{value}</option>)}</select></label><label className="text-sm"><input type="checkbox" checked={isSequence} onChange={(event) => setSequence(event.target.checked)} /> 事件序列</label></div>
        <label className="block text-sm">输入 JSON<Textarea aria-label="预览输入 JSON" rows={7} value={input} onChange={(event) => setInput(event.target.value)} /></label>
        <Button disabled={hasPendingSections} onClick={() => void run(async () => { new ProtocolDocument(input); const result = previewMode === 'mapping' ? await protocolAPI.preview(source, direction, input, isSequence) : await protocolAPI.workflow(source, { mode: previewMode, sample: workflowSample, operation: taskOperation, kind: taskKind, purpose: taskPurpose }, input); setOutput(result); setIssues(result.issues) })}>运行转换预览</Button>
        <label className="block text-sm">目标协议完整 JSON<Textarea aria-label="目标协议 JSON" rows={5} value={target} onChange={(event) => setTarget(event.target.value)} /></label>
        <Button disabled={hasPendingSections} onClick={() => void run(async () => { new ProtocolDocument(target); const result = await protocolAPI.combine(source, target); setOutput(result); setIssues(result.issues) })}>验证协议组合</Button>
        <pre aria-label="转换链路结果" className="max-h-96 overflow-auto rounded bg-secondary/30 p-3 text-xs">{output === undefined ? '预览将显示语义模型、输出与诊断。' : typeof output === 'string' ? output : JSON.stringify(output, null, 2)}</pre>
      </>}
      {tab === 'verification' && <>
        <p role="status">离线验证：{report && verifiedSource === source ? (report.passed ? '通过' : '未通过') : '当前草稿尚未验证'}</p>
        <ProtocolProbe source={source} id={id} hash={verifiedSource === source ? report?.definitionHash : undefined} />
        <p className="text-xs text-muted-foreground">离线报告绑定定义、样例和编译器版本；修改内容后需要重新验证。真实上游验证是独立证据。</p>
        <div className="overflow-x-auto"><table className="w-full text-xs"><thead><tr><th className="text-left">能力</th>{[...(mappings?.children.keys() ?? [])].map((name) => <th key={name}>{name}</th>)}<th>样例证据</th></tr></thead><tbody>{schema.capabilities.filter((capability) => document?.read(`/capabilities/${capability}`) === 'true').map((capability) => <tr key={capability}><td>{capability}</td>{[...(mappings?.children.keys() ?? [])].map((name) => <td key={name} className="text-center">{document?.locate(`/directions/${name}/capabilities`) ? document.read(`/directions/${name}/capabilities/${capability}`) === 'true' ? '支持' : '—' : '继承声明'}</td>)}<td>{verifiedSource === source && report?.covered?.includes(capability) ? '已验证' : '待验证'}</td></tr>)}</tbody></table></div>
        <ul className="space-y-1 text-xs">{report?.checks?.map((check, index) => <li key={index}>{check.passed ? '✓' : '×'} {check.sampleId} {check.direction}</li>)}</ul>
        <details><summary>当前引擎契约</summary><pre className="max-h-96 overflow-auto text-xs">{JSON.stringify({ engineFeatures: schema.engineFeatures, events: schema.events, definition: schema.definitionSchema, semantic: schema.semanticSchema }, null, 2)}</pre></details>
      </>}
      {tab === 'revisions' && <>
        <label className="block text-sm">选择已保存修订<select aria-label="选择修订" className="ml-3 max-w-full rounded border bg-card p-2" value={selectedRevision} onChange={(event) => setSelectedRevision(event.target.value)}><option value="">请选择</option>{revisions.map((revision) => <option key={revision.hash} value={revision.hash}>{revision.hash.slice(0, 16)} · {revision.createdAt}</option>)}</select></label>
        <div className="flex flex-wrap gap-2"><Button disabled={!selectedRevision || !activeHash} onClick={() => void run(async () => setOutput(await protocolAPI.diff(id, activeHash, selectedRevision)))}>比较启用版本</Button><Button disabled={!selectedRevision || isDirty} onClick={() => { const revision = revisions.find((item) => item.hash === selectedRevision); if (revision) { setSource(revision.definition); setTab('json') } }}>载入为草稿</Button><Button disabled={!selectedRevision} onClick={() => void run(async () => { const result = await protocolAPI.verifyRevision(id, selectedRevision); setIssues(result.issues); setOutput(result); if (!result.passed) return; await protocolAPI.activate(id, selectedRevision, activeHash, true); await onSaved(id) })}>验证并回滚至所选修订</Button></div>
        <pre className="max-h-96 overflow-auto text-xs">{output === undefined ? '' : typeof output === 'string' ? output : JSON.stringify(output, null, 2)}</pre>
      </>}
    </fieldset>
    {!!issues.length && <div aria-label="协议诊断" className="space-y-2 border-t pt-3">{issues.map((issue, index) => <div key={index} className="text-sm"><button className="text-left font-mono text-xs underline" onClick={() => locate(issue)}>{issue.code} · {issue.path || '/'}</button><p>{issue.reason}</p><p className="text-xs text-muted-foreground">{issue.suggestion}</p></div>)}</div>}
  </section>
}
