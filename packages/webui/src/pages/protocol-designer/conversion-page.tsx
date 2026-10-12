import { useEffect, useRef, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { SignatureProbe } from './signature-probe'
import { PageHeader } from '@/components/page-header'
import { Button } from '@/components/ui/button'
import { protocolAPI, type EnabledProtocol } from '@/lib/protocol-v2'
import {
  conversionAPI,
  newConversionPolicy,
  type ConversionMatch,
  type ConversionPolicy,
  type ConversionRecord,
  type ConversionRegistry,
} from '@/lib/conversion-policy'

import { field, pretty } from './conversion-fields'
import { ConversionRuleEditor } from './conversion-rule-editor'
import { ConversionActivation } from './conversion-activation'
import { ConversionPreview } from './conversion-preview'
import { useConversionDocument } from './use-conversion-document'

export function ConversionPolicyPage() {
  const navigate = useNavigate()
  const selectionGeneration = useRef(0)
  useEffect(
    () => () => {
      selectionGeneration.current++
    },
    [],
  )
  const [items, setItems] = useState<ConversionRecord[]>([])
  const [registry, setRegistry] = useState<ConversionRegistry>()
  const [protocols, setProtocols] = useState<EnabledProtocol[]>([])
  const document = useConversionDocument()
  const { text, setText, policy, parseError, update, updateFields } = document
  const [saved, setSaved] = useState<ConversionRecord>()
  const [revisions, setRevisions] = useState<ConversionRecord[]>([])
  const [revision, setRevision] = useState('')
  const [selector, setSelector] = useState<ConversionMatch>({})
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const [stats, setStats] = useState<Record<string, number>>({})
  const [session, setSession] = useState('')
  const [clearSession, setClearSession] = useState(false)
  const refresh = async () => {
    setItems(await conversionAPI.list())
    setStats(await conversionAPI.stats())
  }
  const action = async (fn: () => Promise<void>) => {
    setBusy(true)
    setError('')
    setNotice('')
    try {
      await fn()
    } catch (e) {
      setError(String(e))
    } finally {
      setBusy(false)
    }
  }
  useEffect(() => {
    void action(async () => {
      const [s, p] = await Promise.all([protocolAPI.schema(), protocolAPI.enabled()])
      setRegistry(s.conversion)
      setProtocols(p)
      await refresh()
    })
  }, [])
  const open = async (item: ConversionRecord) => {
    const requestId = ++selectionGeneration.current
    setSaved(item)
    setText(item.policyJSON)
    setSelector(item.selector)
    setRevision(item.activeHash)
    setRevisions([])
    const items = await conversionAPI.revisions(item.id)
    if (selectionGeneration.current === requestId) setRevisions(items)
  }
  const matches = !!policy && !!saved && text === saved.policyJSON
  return (
    <div className="space-y-5">
      <PageHeader
        title="转换行为"
        actions={<Button onClick={() => navigate('/protocols')}>返回协议</Button>}
      />
      <p className="text-sm text-muted-foreground">
        配置顺序：引擎默认 → 协议对 → 模型源 → 模型。同 ID
        规则覆盖；严格模式拒绝无法保留的信息。预置与使用相同模块的自定义协议自动继承引擎规则。用量投影保留内部原始计量。预览不会保存会话副本。
      </p>
      {error && (
        <p role="alert" className="text-destructive">
          {error}
        </p>
      )}
      {notice && <p role="status">{notice}</p>}
      <div className="flex flex-wrap gap-2">
        <select
          aria-label="转换策略"
          className={field + ' max-w-sm'}
          value={saved?.id ?? ''}
          disabled={busy}
          onChange={(e) => {
            const item = items.find((x) => x.id === e.target.value)
            if (item) void action(() => open(item))
          }}
        >
          <option value="">新策略</option>
          {items.map((i) => (
            <option key={i.id} value={i.id}>
              {i.policy.name || i.id} · {i.activeHash ? '已启用' : '草稿'}
            </option>
          ))}
        </select>
        <Button
          disabled={busy}
          onClick={() => {
            selectionGeneration.current++
            setSaved(undefined)
            setRevisions([])
            setRevision('')
            setSelector({})
            setText(pretty(newConversionPolicy()))
          }}
        >
          新建策略
        </Button>
      </div>
      <fieldset disabled={busy} className="space-y-4 rounded-lg border p-4">
        <div className="grid gap-3 md:grid-cols-3">
          <label>
            策略 ID
            <input
              aria-label="策略 ID"
              className={field}
              disabled={!!saved}
              value={policy?.id ?? ''}
              onChange={(e) => update({ id: e.target.value })}
            />
          </label>
          <label>
            名称
            <input
              aria-label="策略名称"
              className={field}
              value={policy?.name ?? ''}
              onChange={(e) => update({ name: e.target.value })}
            />
          </label>
          <label>
            转换模式
            <select
              aria-label="转换模式"
              className={field}
              value={policy?.mode ?? 'compatible'}
              onChange={(e) => update({ mode: e.target.value as ConversionPolicy['mode'] })}
            >
              <option value="compatible">兼容：明确降级并记录</option>
              <option value="strict">严格：不可保留则拒绝</option>
            </select>
          </label>
        </div>
        <ConversionRuleEditor document={document} registry={registry} />
        {policy?.continuation && (
          <details open>
            <summary>签名恢复与保存</summary>
            <div className="space-y-2 pt-3">
              <label className="mr-5">
                <input
                  type="checkbox"
                  checked={policy.continuation.clientCarrier}
                  onChange={(e) => updateFields('/continuation', { clientCarrier: e.target.checked })}
                />{' '}
                客户端回传载体
              </label>
              <label>
                <input
                  type="checkbox"
                  checked={policy.continuation.persist}
                  onChange={(e) => updateFields('/continuation', { persist: e.target.checked })}
                />{' '}
                加密持久副本
              </label>
              <div className="grid gap-2 md:grid-cols-4">
                {(['retentionSeconds', 'turnsPerSession', 'maxBytes', 'recordBytes'] as const).map((key) => (
                  <label className="text-xs" key={key}>
                    {key}
                    <input
                      className={field}
                      type="number"
                      value={policy.continuation![key]}
                      onChange={(e) => updateFields('/continuation', { [key]: Number(e.target.value) })}
                    />
                  </label>
                ))}
              </div>
            </div>
          </details>
        )}
        {policy?.usage && (
          <div className="flex flex-wrap gap-5">
            <label>
              <input
                type="checkbox"
                checked={policy.usage.defaultIncludeUsage}
                onChange={(e) => updateFields('/usage', { defaultIncludeUsage: e.target.checked })}
              />{' '}
              未声明时返回 usage（旧行为兼容）
            </label>
            <label>
              <input
                type="checkbox"
                checked={policy.usage.collectUpstreamUsage}
                onChange={(e) => updateFields('/usage', { collectUpstreamUsage: e.target.checked })}
              />{' '}
              请求上游用量
            </label>
          </div>
        )}
        <details>
          <summary>完整策略 JSON</summary>
          <textarea
            aria-label="完整策略 JSON"
            className={field + ' min-h-80 font-mono'}
            value={text}
            onChange={(e) => setText(e.target.value)}
          />
        </details>
        {parseError && <p role="alert">{parseError}</p>}
        <div className="flex gap-2">
          <Button
            disabled={!policy || !!parseError}
            onClick={() =>
              void action(async () => {
                const r = await conversionAPI.save(text, saved?.hash ?? '')
                setSaved({ ...r, activeHash: saved?.activeHash ?? '', selector })
                setText(r.policyJSON)
                await refresh()
                setNotice('草稿已保存，激活前需验证')
              })
            }
          >
            保存策略草稿
          </Button>
          <Button
            disabled={!matches}
            onClick={() =>
              void action(async () => {
                const r = await conversionAPI.verify(saved!.id, saved!.hash)
                setRevisions(await conversionAPI.revisions(saved!.id))
                setRevision(r.hash)
                setNotice(
                  `验证完成：${r.reports?.filter((x) => x.passed).length ?? 0} 个能力组合通过，请检查拒绝项后选择适用协议对`,
                )
              })
            }
          >
            验证策略
          </Button>
        </div>
      </fieldset>
      <ConversionActivation
        protocols={protocols}
        selector={selector}
        setSelector={setSelector}
        revisions={revisions}
        saved={saved}
        revision={revision}
        setRevision={setRevision}
        busy={busy}
        onActivate={() =>
          void action(async () => {
            await conversionAPI.activate(saved!.id, revision, saved!.activeHash ?? '', selector)
            const r = (await conversionAPI.list()).find((x) => x.id === saved!.id)!
            setSaved(r)
            await refresh()
            setNotice('策略与绑定证据已原子更新')
          })
        }
      />
      {policy && (
        <SignatureProbe policy={policy} saved={saved} ingressId={selector.sourceId} clean={!!matches} />
      )}
      <ConversionPreview
        key={saved?.id ?? 'new'}
        policyJSON={text}
        isValid={!!policy}
        busy={busy}
        registry={registry}
        action={action}
      />
      <section className="space-y-2 rounded-lg border p-4">
        <h2>附加状态存储</h2>
        <p className="text-sm">
          记录 {stats.records ?? 0} · 字节 {stats.bytes ?? 0} · 命中 {stats.hits ?? 0} · 未命中{' '}
          {stats.misses ?? 0} · 已过期 {stats.expired ?? 0} · 淘汰 {stats.evicted ?? 0}
        </p>
        <input
          aria-label="清理会话 ID"
          className={field}
          value={session}
          onChange={(e) => {
            setSession(e.target.value)
            setClearSession(false)
          }}
        />
        <label className="block text-sm">
          <input type="checkbox" checked={clearSession} onChange={(e) => setClearSession(e.target.checked)} />{' '}
          确认删除该会话的服务端恢复副本，之后可能无法恢复签名
        </label>
        <Button
          disabled={busy || !session.trim() || !clearSession}
          onClick={() =>
            void action(async () => {
              await conversionAPI.clear(session)
              await refresh()
              setClearSession(false)
              setNotice('指定会话副本已清理')
            })
          }
        >
          清理会话副本
        </Button>
      </section>
    </div>
  )
}
