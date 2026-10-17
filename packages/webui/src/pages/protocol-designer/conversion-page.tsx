import { useEffect, useRef, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { AlertTriangle, ArrowLeft, Database, Plus, Save, ShieldCheck, Trash2 } from 'lucide-react'
import { SignatureProbe } from './signature-probe'
import { PageHeader } from '@/components/page-header'
import { RoleWatermark } from '@/components/role-watermark'
import { Button } from '@/components/ui/button'
import { protocolAPI, type EnabledProtocol } from '@/lib/protocol-v2'
import {
  conversionAPI,
  newConversionPolicy,
  type ConversionMatch,
  type ConversionRecord,
  type ConversionRegistry,
} from '@/lib/conversion-policy'
import { field } from './conversion-fields'
import { ConversionRuleEditor } from './conversion-rule-editor'
import { ConversionActivation } from './conversion-activation'
import { ConversionPreview } from './conversion-preview'
import { useConversionDocument } from './use-conversion-document'

const CONTINUATION_BUDGET_LABELS: Record<
  'retentionSeconds' | 'turnsPerSession' | 'maxBytes' | 'recordBytes',
  string
> = {
  retentionSeconds: '保留时长 (秒)',
  turnsPerSession: '每会话轮数上限',
  maxBytes: '总容量上限 (Bytes)',
  recordBytes: '单条记录上限 (Bytes)',
}

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
    const loadedRevisions = await conversionAPI.revisions(item.id)
    if (selectionGeneration.current === requestId) setRevisions(loadedRevisions)
  }

  const matches = !!policy && !!saved && text === saved.policyJSON

  return (
    <>
      <RoleWatermark className="-right-8 top-0 opacity-[0.05] dark:opacity-[0.08]" />

      <div className="relative z-[1] space-y-6">
        <PageHeader
          title="转换行为"
          description="配置顺序：引擎默认 → 协议对 → 模型源 → 模型。同 ID 规则覆盖；严格模式拒绝无法保留的信息。"
          actions={
            <Button onClick={() => navigate('/protocols')}>
              <ArrowLeft className="h-4 w-4" aria-hidden />
              返回协议
            </Button>
          }
        />

        {error && (
          <div
            role="alert"
            className="flex items-start gap-2.5 rounded-xl border border-destructive/30 bg-destructive/5 px-4 py-3 text-sm text-destructive"
          >
            <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0" aria-hidden />
            <span>{error}</span>
          </div>
        )}

        {notice && (
          <p
            role="status"
            className="rounded-xl border border-border/70 bg-card px-4 py-3 text-sm text-foreground shadow-soft"
          >
            {notice}
          </p>
        )}

        {/* 策略选择工具条 */}
        <div className="flex flex-wrap items-center gap-3">
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
              setText(JSON.stringify(newConversionPolicy(), null, 2))
            }}
          >
            <Plus className="h-3.5 w-3.5" aria-hidden />
            新建策略
          </Button>
        </div>

        <fieldset
          disabled={busy}
          className="space-y-5 border-t border-border/50 pt-5"
        >
          <div className="grid gap-4 md:grid-cols-3">
            <label className="space-y-1.5 text-xs font-medium text-foreground">
              <span>策略 ID</span>
              <input
                aria-label="策略 ID"
                className={field + ' font-mono text-xs'}
                disabled={!!saved}
                value={policy?.id ?? ''}
                onChange={(e) => update({ id: e.target.value })}
              />
            </label>
            <label className="space-y-1.5 text-xs font-medium text-foreground">
              <span>名称</span>
              <input
                aria-label="策略名称"
                className={field}
                value={policy?.name ?? ''}
                onChange={(e) => update({ name: e.target.value })}
              />
            </label>
            <label className="space-y-1.5 text-xs font-medium text-foreground">
              <span>转换模式</span>
              <select
                aria-label="转换模式"
                className={field}
                value={policy?.mode ?? 'compatible'}
                onChange={(e) => {
                  const next = e.target.value
                  if (next === 'compatible' || next === 'strict') {
                    update({ mode: next })
                  }
                }}
              >
                <option value="compatible">兼容：明确降级并记录</option>
                <option value="strict">严格：不可保留则拒绝</option>
              </select>
            </label>
          </div>

          <ConversionRuleEditor document={document} registry={registry} />

          {policy?.continuation && (
            <details open className="border-t border-border/40 pt-4">
              <summary className="cursor-pointer text-xs font-semibold text-foreground">
                签名恢复与保存
              </summary>
              <div className="space-y-3 pt-3">
                <div className="flex flex-wrap gap-6 text-xs font-medium text-foreground">
                  <label className="inline-flex cursor-pointer items-center gap-2">
                    <input
                      type="checkbox"
                      className="accent-primary"
                      checked={policy.continuation.clientCarrier}
                      onChange={(e) =>
                        updateFields('/continuation', { clientCarrier: e.target.checked })
                      }
                    />
                    客户端回传载体
                  </label>
                  <label className="inline-flex cursor-pointer items-center gap-2">
                    <input
                      type="checkbox"
                      className="accent-primary"
                      checked={policy.continuation.persist}
                      onChange={(e) =>
                        updateFields('/continuation', { persist: e.target.checked })
                      }
                    />
                    加密持久副本
                  </label>
                </div>
                <div className="grid gap-3 md:grid-cols-4">
                  {(
                    ['retentionSeconds', 'turnsPerSession', 'maxBytes', 'recordBytes'] as const
                  ).map((key) => (
                    <label className="space-y-1 text-xs font-medium text-foreground" key={key}>
                      <span className="block text-muted-foreground">
                        {CONTINUATION_BUDGET_LABELS[key]}
                      </span>
                      <input
                        className={field + ' font-mono text-xs'}
                        type="number"
                        value={policy.continuation![key]}
                        onChange={(e) =>
                          updateFields('/continuation', { [key]: Number(e.target.value) })
                        }
                      />
                    </label>
                  ))}
                </div>
              </div>
            </details>
          )}

          {policy?.usage && (
            <div className="flex flex-wrap gap-6 border-t border-border/40 pt-4 text-xs font-medium text-foreground">
              <label className="inline-flex cursor-pointer items-center gap-2">
                <input
                  type="checkbox"
                  className="accent-primary"
                  checked={policy.usage.defaultIncludeUsage}
                  onChange={(e) =>
                    updateFields('/usage', { defaultIncludeUsage: e.target.checked })
                  }
                />
                未声明时返回 usage（旧行为兼容）
              </label>
              <label className="inline-flex cursor-pointer items-center gap-2">
                <input
                  type="checkbox"
                  className="accent-primary"
                  checked={policy.usage.collectUpstreamUsage}
                  onChange={(e) =>
                    updateFields('/usage', { collectUpstreamUsage: e.target.checked })
                  }
                />
                请求上游用量
              </label>
            </div>
          )}

          <details className="border-t border-border/40 pt-4">
            <summary className="cursor-pointer text-xs font-semibold text-foreground">
              完整策略 JSON
            </summary>
            <textarea
              aria-label="完整策略 JSON"
              className="mt-3 min-h-80 w-full rounded-md border border-input bg-card p-3 font-mono text-xs leading-relaxed text-foreground focus-visible:outline-none focus-visible:border-rose focus-visible:ring-[3px] focus-visible:ring-wash"
              value={text}
              onChange={(e) => setText(e.target.value)}
            />
          </details>

          {parseError && (
            <p role="alert" className="text-xs text-destructive">
              {parseError}
            </p>
          )}

          <div className="flex flex-wrap gap-2 pt-1">
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
              <Save className="h-3.5 w-3.5" aria-hidden />
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
              <ShieldCheck className="h-3.5 w-3.5" aria-hidden />
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
          <SignatureProbe
            policy={policy}
            saved={saved}
            ingressId={selector.sourceId}
            clean={!!matches}
          />
        )}

        <ConversionPreview
          key={saved?.id ?? 'new'}
          policyJSON={text}
          isValid={!!policy}
          busy={busy}
          registry={registry}
          action={action}
        />

        <section className="space-y-4 border-t border-border/50 pt-5">
          <div className="flex flex-wrap items-center justify-between gap-3 pb-1">
            <div className="flex items-center gap-2">
              <Database className="h-4 w-4 text-primary" aria-hidden />
              <h2 className="text-sm font-semibold text-foreground">附加状态存储</h2>
            </div>
            <p className="font-mono text-xs text-muted-foreground">
              记录 {stats.records ?? 0} · 字节 {stats.bytes ?? 0} · 命中 {stats.hits ?? 0} · 未命中{' '}
              {stats.misses ?? 0} · 已过期 {stats.expired ?? 0} · 淘汰 {stats.evicted ?? 0}
            </p>
          </div>

          <div className="space-y-3">
            <input
              aria-label="清理会话 ID"
              placeholder="输入需要清理的会话 ID"
              className={field + ' max-w-md font-mono text-xs'}
              value={session}
              onChange={(e) => {
                setSession(e.target.value)
                setClearSession(false)
              }}
            />
            <label className="flex cursor-pointer items-center gap-2 text-xs text-muted-foreground">
              <input
                type="checkbox"
                className="accent-primary"
                checked={clearSession}
                onChange={(e) => setClearSession(e.target.checked)}
              />
              确认删除该会话的服务端恢复副本，之后可能无法恢复签名
            </label>
            <Button
              variant="danger"
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
              <Trash2 className="h-3.5 w-3.5" aria-hidden />
              清理会话副本
            </Button>
          </div>
        </section>
      </div>
    </>
  )
}
