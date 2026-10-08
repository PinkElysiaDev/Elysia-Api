import { useState } from 'react'
import { Button } from '@/components/ui/button'
import { conversionAPI, type ConversionPolicy, type ConversionRecord } from '@/lib/conversion-policy'

export function SignatureProbe({ policy, saved, ingressId, clean }: { policy: ConversionPolicy; saved?: ConversionRecord; ingressId?: string; clean: boolean }) {
  const [ruleId, setRuleId] = useState('')
  const [sourceId, setSourceId] = useState('')
  const [modelId, setModelId] = useState('')
  const [keyNumber, setKeyNumber] = useState('')
  const [busy, setBusy] = useState(false)
  const [result, setResult] = useState('')
  const [error, setError] = useState('')
  const rules = policy.rules.filter((r) => r.action === 'provider_signature' && r.enabled)
  if (!rules.length) return null
  const run = async () => {
    if (!saved || !ingressId) return
    setBusy(true); setError(''); setResult('')
    try {
      const proof = await conversionAPI.probeSignature(saved.id, { hash: saved.hash, ruleId, sourceId, modelId, ingressId, keyIndex: keyNumber ? Number(keyNumber) - 1 : undefined })
      setResult(`兼容值已验证：${proof.modelId} · ${proof.targetRevision.slice(0, 12)}。离线验证完成后再启用策略。`)
    } catch (e) { setError(String(e)) } finally { setBusy(false) }
  }
  const input = 'w-full rounded border bg-card p-2 text-sm'
  return <section aria-label="兼容签名验证" className="space-y-3 rounded-lg border p-4">
    <h2>验证兼容值</h2>
    <p className="text-sm text-muted-foreground">向已有模型源发送最小工具历史验证请求，会产生少量上游用量。返回的工具不会执行。验证仅适用于此策略、协议修订、账号和实际模型，30 天后过期。</p>
    <div className="grid gap-2 md:grid-cols-3">
      <label>兼容值规则<select aria-label="兼容值规则" className={input} value={ruleId} onChange={(e) => setRuleId(e.target.value)}><option value="">选择规则</option>{rules.map((r) => <option key={r.id}>{r.id}</option>)}</select></label>
      <label>模型源 ID<input aria-label="验证模型源 ID" className={input} value={sourceId} onChange={(e) => setSourceId(e.target.value)} /></label>
      <label>实际模型 ID<input aria-label="验证模型 ID" className={input} value={modelId} onChange={(e) => setModelId(e.target.value)} /></label>
      <label>密钥序号（可选）<input type="number" min={1} step={1} aria-label="验证密钥序号" className={input} value={keyNumber} onChange={(e) => setKeyNumber(e.target.value)} placeholder="默认使用首个可用密钥" /></label>
    </div>
    <p className="text-xs text-muted-foreground">先保存草稿，并在适用范围中选择来源协议。</p>
    {error && <p role="alert" className="text-destructive">{error}</p>}{result && <p role="status">{result}</p>}
    <Button disabled={busy || !clean || !saved || !ingressId || !ruleId || !modelId} onClick={() => void run()}>发送兼容值验证请求</Button>
  </section>
}
