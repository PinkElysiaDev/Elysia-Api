import { readChartTickSizePx } from '@/lib/utils'

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

/** 相对 now 的本地日历日差：0=今天，-1=昨天。 */
export function localDayDiff(ms: number, nowMs: number): number {
  const a = new Date(ms)
  const b = new Date(nowMs)
  a.setHours(0, 0, 0, 0)
  b.setHours(0, 0, 0, 0)
  return Math.round((a.getTime() - b.getTime()) / DAY_MS)
}

/** 脉搏悬停/横轴：今日 "15:30"，昨日 "昨日 15:30"，更早 "8月29日 15:30"。 */
export function formatPulseCaption(ms: number, nowMs: number): string {
  const hm = formatHm(ms)
  const diff = localDayDiff(ms, nowMs)
  if (diff === 0) return hm
  if (diff === -1) return `昨日 ${hm}`
  const d = new Date(ms)
  const now = new Date(nowMs)
  const md = `${d.getMonth() + 1}月${d.getDate()}日`
  const day = d.getFullYear() !== now.getFullYear() ? `${d.getFullYear()}年${md}` : md
  return `${day} ${hm}`
}

/** 与 CHART_TICK 等宽字体对齐的刻度宽估算（CJK 1em，ASCII ~0.64em）。 */
function estimatePulseTickWidth(label: string, fontPx = readChartTickSizePx()): number {
  const cjk = fontPx
  const ascii = fontPx * 0.64
  let w = 0
  for (const ch of label) {
    w += ch.charCodeAt(0) > 0xff ? cjk : ascii
  }
  return w
}

/**
 * 横轴刻度是否互不重叠。首刻度按左对齐、末刻度按右对齐、中间居中——
 * 与 PulseXTick 的 textAnchor 一致；Recharts 一律按居中测宽，不能用来避让「昨日」。
 */
export function pulseXTicksFit(
  ticks: number[],
  nowMs: number,
  spanPx: number,
  fontPx = readChartTickSizePx(),
  gapPx = 8,
): boolean {
  const n = ticks.length
  if (n <= 1 || !(spanPx > 0)) return true
  let prevRight = -Infinity
  for (let i = 0; i < n; i++) {
    const w = estimatePulseTickWidth(formatPulseCaption(ticks[i], nowMs), fontPx)
    const x = (i * spanPx) / (n - 1)
    const left = i === 0 ? x : i === n - 1 ? x - w : x - w / 2
    const right = i === 0 ? x + w : i === n - 1 ? x : x + w / 2
    if (left < prevRight + gapPx) return false
    prevRight = right
  }
  return true
}
