/** 固定 UTC offset 的日桶算术——与后端 UsageDaily 的分桶公式完全一致。 */
export const DAY_MS = 86_400_000

/** 第 dayOffset 天（0=今天）的桶起点时间戳（负值=往过去偏移）。 */
export function offsetDayStart(ts: number, offsetMinutes: number, dayOffset = 0): number {
  const shifted = ts + offsetMinutes * 60_000
  return (Math.floor(shifted / DAY_MS) + dayOffset) * DAY_MS - offsetMinutes * 60_000
}

/** 时间戳在固定 offset 分桶下的 YYYY-MM-DD（与后端 date() 输出同构）。 */
export function offsetDayKey(ts: number, offsetMinutes: number): string {
  const shifted = new Date(ts + offsetMinutes * 60_000)
  return `${shifted.getUTCFullYear()}-${String(shifted.getUTCMonth() + 1).padStart(2, '0')}-${String(shifted.getUTCDate()).padStart(2, '0')}`
}

export type PulseRange = '60m' | '6h' | '24h'

export const PULSE_OPTIONS: { value: PulseRange; label: string }[] = [
  { value: '60m', label: '近 60 分钟' },
  { value: '6h', label: '近 6 小时' },
  { value: '24h', label: '近 24 小时' },
]

export function pulseWindow(range: PulseRange): { spanMs: number; bucketMinutes: 1 | 5 | 15 } {
  if (range === '60m') return { spanMs: 60 * 60_000, bucketMinutes: 1 }
  if (range === '6h') return { spanMs: 6 * 60 * 60_000, bucketMinutes: 5 }
  return { spanMs: 24 * 60 * 60_000, bucketMinutes: 15 }
}

function pad2(n: number) {
  return String(n).padStart(2, '0')
}

export function formatHm(ms: number) {
  const d = new Date(ms)
  return `${pad2(d.getHours())}:${pad2(d.getMinutes())}`
}
