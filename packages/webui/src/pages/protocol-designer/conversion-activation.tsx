import type { EnabledProtocol } from '@/lib/protocol-v2'
import type { ConversionMatch, ConversionRecord } from '@/lib/conversion-policy'
import { Button } from '@/components/ui/button'
import { field } from './conversion-fields'

type Props = {
  protocols: EnabledProtocol[]
  selector: ConversionMatch
  setSelector: (value: ConversionMatch) => void
  revisions: ConversionRecord[]
  saved?: ConversionRecord
  revision: string
  setRevision: (value: string) => void
  busy: boolean
  onActivate: () => void
}

/** Select a verified revision and its protocol scope before activation or rollback. */
export function ConversionActivation({
  protocols,
  selector,
  setSelector,
  revisions,
  saved,
  revision,
  setRevision,
  busy,
  onActivate,
}: Props) {
  const protocolSelect = (label: string, key: 'sourceId' | 'targetId') => (
    <label className="space-y-1.5 text-xs font-medium text-foreground">
      <span>{label}</span>
      <select
        aria-label={label}
        className={field}
        value={selector[key] ?? ''}
        onChange={(e) => setSelector({ ...selector, [key]: e.target.value })}
      >
        <option value="">全部协议</option>
        {protocols.map((p) => (
          <option key={p.id} value={p.id}>
            {p.name || p.id}
          </option>
        ))}
      </select>
    </label>
  )
  return (
    <section className="space-y-4 border-t border-border/50 pt-5" aria-label="策略激活">
      <h2 className="text-sm font-semibold text-foreground">修订、适用范围与回滚</h2>
      <div className="grid gap-4 md:grid-cols-3">
        {protocolSelect('来源协议', 'sourceId')}
        {protocolSelect('上游协议', 'targetId')}
        <label className="space-y-1.5 text-xs font-medium text-foreground">
          <span>已验证修订</span>
          <select
            aria-label="策略修订"
            className={field + ' font-mono text-xs'}
            value={revision}
            onChange={(e) => setRevision(e.target.value)}
          >
            <option value="">请选择修订</option>
            {revisions.map((r) => (
              <option key={r.hash} value={r.hash}>
                {r.hash.slice(0, 12)}
                {saved?.activeHash === r.hash ? '（当前）' : ''}
              </option>
            ))}
          </select>
        </label>
      </div>
      <Button variant="primary" disabled={busy || !saved || !revision} onClick={onActivate}>
        启用或回滚至此修订
      </Button>
      <details className="border-t border-border/40 pt-3.5">
        <summary className="cursor-pointer text-xs font-medium text-foreground">验证详情</summary>
        <pre className="mt-2 max-h-64 overflow-auto whitespace-pre-wrap rounded-md bg-[var(--code-bg)] p-3 font-mono text-xs">
          {JSON.stringify(revisions.find((r) => r.hash === revision)?.reports ?? [], null, 2)}
        </pre>
      </details>
    </section>
  )
}
