import { useEffect, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { SignatureProbe } from './signature-probe'
import { PageHeader } from '@/components/page-header'
import { Button } from '@/components/ui/button'
import { protocolAPI, type EnabledProtocol } from '@/lib/protocol-v2'
import { conversionAPI, newConversionPolicy, type ConversionMatch, type ConversionPhase, type ConversionPolicy, type ConversionRecord, type ConversionRegistry, type ConversionRule } from '@/lib/conversion-policy'

const field = 'rounded-md border bg-card p-2 text-sm w-full'
const pretty = (v: unknown) => JSON.stringify(v, null, 2)
const phases: Record<ConversionPhase, string> = { ingress: '入口规范化', request: '候选请求', response: '响应', event: '流式事件', wire: '编码后' }

export function ConversionPolicyPage() {
  const navigate = useNavigate()
  const [items, setItems] = useState<ConversionRecord[]>([])
  const [registry, setRegistry] = useState<ConversionRegistry>()
  const [protocols, setProtocols] = useState<EnabledProtocol[]>([])
  const [text, setText] = useState(pretty(newConversionPolicy()))
  const [saved, setSaved] = useState<ConversionRecord>()
  const [revisions, setRevisions] = useState<ConversionRecord[]>([])
  const [revision, setRevision] = useState('')
  const [selector, setSelector] = useState<ConversionMatch>({})
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const [phase, setPhase] = useState<ConversionPhase>('request')
  const [input, setInput] = useState('{"schemaVersion":1,"source":{},"model":"example","content":[],"parameters":{"stream":false}}')
  const [context, setContext] = useState('{"source":{"definitionId":"openai-chat-completions","family":"openai_chat","wireVersion":"v2"},"target":{"definitionId":"google-generate-content","family":"gemini","wireVersion":"v2"},"model":"example","operation":"generate","transport":"http_json"}')
  const [preview, setPreview] = useState<Awaited<ReturnType<typeof conversionAPI.preview>>>()
  const [stats, setStats] = useState<Record<string, number>>({})
  const [session, setSession] = useState('')
  const [clearSession, setClearSession] = useState(false)
  let policy: ConversionPolicy | undefined
  let parseError = ''
  try { policy = JSON.parse(text) as ConversionPolicy; if (!Array.isArray(policy.rules)) throw new Error('rules 必须是数组') } catch (e) { parseError = String(e) }
  const update = (change: Partial<ConversionPolicy>) => { if (policy) setText(pretty({ ...policy, ...change })) }
  const updateRule = (index: number, change: Partial<ConversionRule>) => { if (policy) update({ rules: policy.rules.map((r, i) => i === index ? { ...r, ...change } : r) }) }
  const refresh = async () => { setItems(await conversionAPI.list()); setStats(await conversionAPI.stats()) }
  const action = async (fn: () => Promise<void>) => { setBusy(true); setError(''); setNotice(''); try { await fn() } catch (e) { setError(String(e)) } finally { setBusy(false) } }
  useEffect(() => { void action(async () => { const [s, p] = await Promise.all([protocolAPI.schema(), protocolAPI.enabled()]); setRegistry(s.conversion); setProtocols(p); await refresh() }) }, [])
  const open = async (item: ConversionRecord) => { setSaved(item); setText(pretty(item.policy)); setSelector(item.selector); setRevision(item.activeHash); setRevisions(await conversionAPI.revisions(item.id)); setPreview(undefined) }
  const matches = policy && saved && pretty(policy) === pretty(saved.policy)
  const move = (index: number, offset: number) => {
    if (!policy) return
    const rules = [...policy.rules]; const other = index + offset
    if (other < 0 || other >= rules.length) return
    ;[rules[index], rules[other]] = [rules[other], rules[index]]
    update({ rules: rules.map((r, i) => ({ ...r, order: (i + 1) * 1000 })) })
  }
  const protocolSelect = (label: string, key: 'sourceId' | 'targetId') => <label className="space-y-1 text-sm">{label}<select aria-label={label} className={field} value={selector[key] ?? ''} onChange={(e) => setSelector({ ...selector, [key]: e.target.value })}><option value="">全部协议</option>{protocols.map((p) => <option key={p.id} value={p.id}>{p.name || p.id}</option>)}</select></label>
  return <div className="space-y-5">
    <PageHeader title="转换行为" actions={<Button onClick={() => navigate('/protocols')}>返回协议</Button>} />
    <p className="text-sm text-muted-foreground">配置顺序：引擎默认 → 协议对 → 模型源 → 模型。同 ID 规则覆盖；严格模式拒绝无法保留的信息。预置与使用相同模块的自定义协议自动继承引擎规则。用量投影保留内部原始计量。预览不会保存会话副本。</p>
    {error && <p role="alert" className="text-destructive">{error}</p>}{notice && <p role="status">{notice}</p>}
    <div className="flex flex-wrap gap-2"><select aria-label="转换策略" className={field + ' max-w-sm'} value={saved?.id ?? ''} disabled={busy} onChange={(e) => { const item = items.find((x) => x.id === e.target.value); if (item) void action(() => open(item)) }}><option value="">新策略</option>{items.map((i) => <option key={i.id} value={i.id}>{i.policy.name || i.id} · {i.activeHash ? '已启用' : '草稿'}</option>)}</select><Button disabled={busy} onClick={() => { setSaved(undefined); setRevisions([]); setRevision(''); setSelector({}); setText(pretty(newConversionPolicy())) }}>新建策略</Button></div>
    <fieldset disabled={busy} className="space-y-4 rounded-lg border p-4">
      <div className="grid gap-3 md:grid-cols-3"><label>策略 ID<input aria-label="策略 ID" className={field} disabled={!!saved} value={policy?.id ?? ''} onChange={(e) => update({ id: e.target.value })} /></label><label>名称<input aria-label="策略名称" className={field} value={policy?.name ?? ''} onChange={(e) => update({ name: e.target.value })} /></label><label>转换模式<select aria-label="转换模式" className={field} value={policy?.mode ?? 'compatible'} onChange={(e) => update({ mode: e.target.value as ConversionPolicy['mode'] })}><option value="compatible">兼容：明确降级并记录</option><option value="strict">严格：不可保留则拒绝</option></select></label></div>
      <details open><summary className="cursor-pointer">有序规则</summary><div className="space-y-3 pt-3">{policy?.rules.map((r, i) => <section key={i} className="space-y-2 rounded-md border p-3" aria-label={`规则 ${r.id}`}>
        <div className="flex flex-wrap gap-2"><label><input type="checkbox" checked={r.enabled} onChange={(e) => updateRule(i, { enabled: e.target.checked })} />启用</label><input aria-label={`规则 ${i + 1} ID`} className={field + ' max-w-xs'} value={r.id} onChange={(e) => updateRule(i, { id: e.target.value })} /><Button size="sm" onClick={() => move(i, -1)}>上移</Button><Button size="sm" onClick={() => move(i, 1)}>下移</Button><Button size="sm" variant="danger" onClick={() => update({ rules: policy.rules.filter((_, j) => i !== j) })}>移除规则</Button></div>
        <div className="grid gap-2 md:grid-cols-3"><label>阶段<select className={field} value={r.phase} onChange={(e) => updateRule(i, { phase: e.target.value as ConversionPhase })}>{registry?.phases.map((p) => <option key={p} value={p}>{phases[p]}</option>)}</select></label><label>动作<select className={field} value={r.action} onChange={(e) => { const action = e.target.value; const spec = registry?.actionSchemas?.[action]; updateRule(i, { action, ...(spec ? { phase: spec.phases.includes(r.phase) ? r.phase : spec.phases[0], value: typeof r.value === 'string' && spec.value.enum.includes(r.value) ? r.value : 'gemini' } : {}) }) }}>{registry?.actions.map((a) => <option key={a}>{a}</option>)}</select></label><label>顺序<input type="number" className={field} value={r.order} onChange={(e) => updateRule(i, { order: Number(e.target.value) })} /></label></div>
        <div className="grid gap-2 md:grid-cols-3">{(['sourceId', 'targetId', 'model', 'operation', 'transport', 'nodeKind', 'path'] as const).map((key) => <label key={key} className="text-xs">匹配 {key}<input className={field} value={r.match?.[key] ?? ''} onChange={(e) => updateRule(i, { match: { ...r.match, [key]: e.target.value } })} /></label>)}</div>
        {registry?.actionSchemas?.[r.action] && <div className="space-y-2"><p className="text-xs text-muted-foreground">{registry.actionSchemas[r.action].description}</p><label className="block text-xs">目标编码器<select aria-label={`规则 ${i + 1} 目标编码器`} className={field} value={typeof r.value === 'string' ? r.value : ''} onChange={(e) => updateRule(i, { value: e.target.value })}><option value="">请选择</option>{registry.actionSchemas[r.action].value.enum.map((codec) => <option key={codec}>{codec}</option>)}</select></label></div>}
        <label className="block text-xs">动作字段路径<input className={field} value={r.path ?? ''} onChange={(e) => updateRule(i, { path: e.target.value })} /></label><label className="block text-xs">诊断原因<input className={field} value={r.reason ?? ''} onChange={(e) => updateRule(i, { reason: e.target.value })} /></label>
        <p className="text-xs text-muted-foreground">value、expression、字段存在条件和资源限制可在完整 JSON 中编辑。</p>
      </section>)}<Button onClick={() => update({ rules: [...(policy?.rules ?? []), { id: `rule-${(policy?.rules.length ?? 0) + 1}`, order: ((policy?.rules.length ?? 0) + 1) * 1000, enabled: true, phase: 'request', match: {}, action: 'warn', reason: '自定义转换规则' }] })}>添加规则</Button></div></details>
      {policy?.continuation && <details open><summary>签名恢复与保存</summary><div className="space-y-2 pt-3"><label className="mr-5"><input type="checkbox" checked={policy.continuation.clientCarrier} onChange={(e) => update({ continuation: { ...policy.continuation!, clientCarrier: e.target.checked } })} /> 客户端回传载体</label><label><input type="checkbox" checked={policy.continuation.persist} onChange={(e) => update({ continuation: { ...policy.continuation!, persist: e.target.checked } })} /> 加密持久副本</label><div className="grid gap-2 md:grid-cols-4">{(['retentionSeconds', 'turnsPerSession', 'maxBytes', 'recordBytes'] as const).map((key) => <label className="text-xs" key={key}>{key}<input className={field} type="number" value={policy.continuation![key]} onChange={(e) => update({ continuation: { ...policy.continuation!, [key]: Number(e.target.value) } })} /></label>)}</div></div></details>}
      {policy?.usage && <div className="flex flex-wrap gap-5"><label><input type="checkbox" checked={policy.usage.defaultIncludeUsage} onChange={(e) => update({ usage: { ...policy.usage!, defaultIncludeUsage: e.target.checked } })} /> 未声明时返回 usage（旧行为兼容）</label><label><input type="checkbox" checked={policy.usage.collectUpstreamUsage} onChange={(e) => update({ usage: { ...policy.usage!, collectUpstreamUsage: e.target.checked } })} /> 请求上游用量</label></div>}
      <details><summary>完整策略 JSON</summary><textarea aria-label="完整策略 JSON" className={field + ' min-h-80 font-mono'} value={text} onChange={(e) => setText(e.target.value)} /></details>
      {parseError && <p role="alert">{parseError}</p>}
      <div className="flex gap-2"><Button disabled={!policy || !!parseError} onClick={() => void action(async () => { const r = await conversionAPI.save(policy!, saved?.hash ?? ''); setSaved({ ...r, activeHash: saved?.activeHash ?? '', selector }); setText(pretty(r.policy)); await refresh(); setNotice('草稿已保存，激活前需验证') })}>保存策略草稿</Button><Button disabled={!matches} onClick={() => void action(async () => { const r = await conversionAPI.verify(saved!.id, saved!.hash); setRevisions(await conversionAPI.revisions(saved!.id)); setRevision(r.hash); setNotice(`验证完成：${r.reports?.filter((x) => x.passed).length ?? 0} 个能力组合通过，请检查拒绝项后选择适用协议对`) })}>验证策略</Button></div>
    </fieldset>
    <section className="space-y-3 rounded-lg border p-4" aria-label="策略激活"><h2>修订、适用范围与回滚</h2><div className="grid gap-3 md:grid-cols-3">{protocolSelect('来源协议', 'sourceId')}{protocolSelect('上游协议', 'targetId')}<label>已验证修订<select aria-label="策略修订" className={field} value={revision} onChange={(e) => setRevision(e.target.value)}><option value="">请选择修订</option>{revisions.map((r) => <option key={r.hash} value={r.hash}>{r.hash.slice(0, 12)}{saved?.activeHash === r.hash ? '（当前）' : ''}</option>)}</select></label></div><Button disabled={busy || !saved || !revision} onClick={() => void action(async () => { await conversionAPI.activate(saved!.id, revision, saved!.activeHash ?? '', selector); const r = (await conversionAPI.list()).find((x) => x.id === saved!.id)!; setSaved(r); await refresh(); setNotice('策略与绑定证据已原子更新') })}>启用或回滚至此修订</Button><details><summary>验证详情</summary><pre className="overflow-auto whitespace-pre-wrap text-xs">{pretty(revisions.find((r) => r.hash === revision)?.reports ?? [])}</pre></details></section>
    {policy && <SignatureProbe policy={policy} saved={saved} ingressId={selector.sourceId} clean={!!matches} />}
    <section className="space-y-3 rounded-lg border p-4" aria-label="转换行为预览"><h2>逐阶段预览</h2><select aria-label="预览阶段" className={field} value={phase} onChange={(e) => setPhase(e.target.value as ConversionPhase)}>{registry?.phases.map((p) => <option key={p} value={p}>{phases[p]}</option>)}</select><label className="block">协议与模型上下文<textarea aria-label="行为预览上下文" className={field + ' h-28 font-mono'} value={context} onChange={(e) => setContext(e.target.value)} /></label><label className="block">输入 JSON<textarea aria-label="行为预览输入" className={field + ' h-40 font-mono'} value={input} onChange={(e) => setInput(e.target.value)} /></label><Button disabled={busy || !policy} onClick={() => void action(async () => setPreview(await conversionAPI.preview(policy!, phase, JSON.parse(context), JSON.parse(input))))}>预览行为</Button>{preview && <><ul className="text-xs">{Object.entries(preview.effective.origins).map(([rule, origin]) => <li key={rule}>{rule} ← {origin}</li>)}</ul><pre aria-label="行为预览结果" className="overflow-auto whitespace-pre-wrap text-xs">{pretty(preview)}</pre></>}</section>
    <section className="space-y-2 rounded-lg border p-4"><h2>附加状态存储</h2><p className="text-sm">记录 {stats.records ?? 0} · 字节 {stats.bytes ?? 0} · 命中 {stats.hits ?? 0} · 未命中 {stats.misses ?? 0} · 已过期 {stats.expired ?? 0} · 淘汰 {stats.evicted ?? 0}</p><input aria-label="清理会话 ID" className={field} value={session} onChange={(e) => { setSession(e.target.value); setClearSession(false) }} /><label className="block text-sm"><input type="checkbox" checked={clearSession} onChange={(e) => setClearSession(e.target.checked)} /> 确认删除该会话的服务端恢复副本，之后可能无法恢复签名</label><Button disabled={busy || !session.trim() || !clearSession} onClick={() => void action(async () => { await conversionAPI.clear(session); await refresh(); setClearSession(false); setNotice('指定会话副本已清理') })}>清理会话副本</Button></section>
  </div>
}
