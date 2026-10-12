import type { UsageTokenUsage } from '@/lib/types'
import { formatNumber } from '@/lib/utils'

/** Render normalized input components without counting cache creation twice. */
export function UsageTokens({ usage }: { usage: UsageTokenUsage }) {
  const input = usage.inputTokens
  const output = usage.outputTokens
  const read = usage.cacheHitTokens
  const creation = usage.cacheCreationTokens
  const hasCompleteSplit = read != null && creation != null
  const remainder = input == null ? undefined : Math.max(input - (read ?? 0) - (creation ?? 0), 0)
  const total = input != null && output != null ? input + output : usage.totalTokens
  const segments = [
    { label: '缓存命中', count: read, color: 'rose-soft' },
    { label: hasCompleteSplit ? '未命中输入' : '其余输入', count: remainder, color: 'rose' },
    { label: '缓存创建', count: creation, color: 'amber' },
    { label: '输出', count: output, color: 'jade' },
  ]
  if (total == null && segments.every((segment) => segment.count == null)) {
    return <p className="text-xs text-muted-foreground">上游未返回 token 用量。</p>
  }
  return <div aria-label="Token 用量">
    {total != null && total > 0 && <div className="my-2 flex h-2 overflow-hidden rounded-full bg-border" aria-hidden>
      {segments.map(({ label, count, color }) => count != null && count > 0 && <i key={label} className="h-full" style={{ width: `${(count / total) * 100}%`, background: `var(--${color})` }} />)}
    </div>}
    <p className="tnum flex flex-wrap gap-x-4 gap-y-1 text-xs text-muted-foreground">
      {segments.map(({ label, count, color }) => <span key={label}>
        <i className="mr-[5px] inline-block h-2 w-2 rounded-[2px] align-[-1px]" style={{ background: `var(--${color})` }} />
        {label} {count == null ? '—' : formatNumber(count)}
      </span>)}
      <span>合计 <b className="font-semibold text-foreground">{total == null ? '—' : formatNumber(total)}</b></span>
      {usage.estimated && <span className="text-amber">（估算）</span>}
    </p>
  </div>
}
