import { useLayoutEffect, useEffect, useState } from 'react'
import { readChartTickSizePx } from '@/lib/utils'

/** 总览折线/面积入场时长：与 recharts 默认一致，脉搏 / 趋势 / 日调用共用。 */
export const CHART_ENTER_MS = 1500

/** 趋势图与模型日调用图共用的绘图盒，保证横轴与刻度左右对齐。 */
export const OVERVIEW_CHART = {
  height: 340,
  // 左右留白给 Y 轴刻度；刻度画在 yLeft/yRight 宽里，外侧再留 8px 不贴边。
  margin: { top: 16, right: 8, bottom: 8, left: 8 },
  yLeftWidth: 48,
  yRightWidth: 48,
  xMinTickGap: 24,
  // point：折线端点落在绘图区左右边。ComposedChart 里只要挂了 Bar，
  // 默认会改成 band，点落在每个类目带的中心，两端各空出半个类目宽。
  xScale: 'point' as const,
  xPadding: { left: 10, right: 10 },
} as const

export const MODEL_COLORS = [
  'var(--rose)',
  'var(--orchid)',
  'var(--jade)',
  'var(--amber)',
  'var(--ember)',
  'var(--rose-soft)',
  '#5B7CFA',
  '#2A9B8F',
  '#8B6BCF',
]

export function ticksFor(values: number[]): number[] {
  const max = Math.max(0, ...values)
  if (max <= 0) return [0]
  // 步进取 1/2/5×10ⁿ 的「好看倍数」（如 50/100/200），恒定 3 段（0/⅓/⅔/1）——
  // 趋势图与日分布图共用，网格线几何位置对齐不因取整方式漂移。
  const raw = max / 3
  const pow = 10 ** Math.floor(Math.log10(raw))
  const n = raw / pow
  const step = (n <= 1 ? 1 : n <= 2 ? 2 : n <= 5 ? 5 : 10) * pow
  return [0, step, step * 2, step * 3]
}

/** 均匀、好看的 Y 轴刻度（含小数），顶格对齐数据最大值之上的 neat step。 */
export function niceTicks(max: number, splits = 3): number[] {
  if (!(max > 0)) return [0, 1]
  const raw = max / splits
  const pow = 10 ** Math.floor(Math.log10(raw))
  const n = raw / pow
  const step = (n <= 1 ? 1 : n <= 2 ? 2 : n <= 5 ? 5 : 10) * pow
  const top = Math.ceil(max / step) * step
  const out: number[] = []
  for (let v = 0; v <= top + step * 0.25; v += step) {
    out.push(Number(v.toPrecision(12)))
  }
  return out
}

/** 从等间隔桶里均匀抽出 count 个横轴刻度，避免 preserveStartEnd 造成末档间距忽大忽小。 */
export function evenIndexTicks<T>(items: T[], count: number, pick: (item: T) => number): number[] {
  if (items.length === 0) return []
  const n = Math.max(2, Math.min(count, items.length))
  const out: number[] = []
  for (let i = 0; i < n; i++) {
    const idx = Math.round((i * (items.length - 1)) / (n - 1))
    out.push(pick(items[idx]))
  }
  return [...new Set(out)]
}

/**
 * 新窗口的数据回来之前仍画旧窗口，避免切换范围时先用空点挂图、入场动画被吃掉。
 * ready 为 true（该次请求结束）后才提交 requested。
 */
export function useCommittedRange<T>(requested: T, ready: boolean): T {
  const [committed, setCommitted] = useState(requested)
  if (ready && committed !== requested) {
    setCommitted(requested)
  }
  return committed
}

/** 实测 --chart-tick-size 的解析像素值，供脉搏横轴避让按实际字号测宽。
 * 刻度字号随根字号随视口连续缩放，没有断点可监听，resize 时用 rAF 节流重测。 */
export function useChartTickSize(): number {
  const [size, setSize] = useState(readChartTickSizePx)
  useLayoutEffect(() => {
    let raf = 0
    const update = () => {
      raf = 0
      const next = readChartTickSizePx()
      setSize((prev) => (prev === next ? prev : next))
    }
    update()
    const onResize = () => {
      if (raf === 0) raf = window.requestAnimationFrame(update)
    }
    window.addEventListener('resize', onResize)
    return () => {
      window.removeEventListener('resize', onResize)
      if (raf !== 0) window.cancelAnimationFrame(raf)
    }
  }, [])
  return size
}

/** 时间范围切换时播一次入场动画。 */
export function useEnterAnimation(resetKey: string | number, durationMs = CHART_ENTER_MS) {
  const [seen, setSeen] = useState(resetKey)
  const [active, setActive] = useState(true)
  if (seen !== resetKey) {
    setSeen(resetKey)
    setActive(true)
  }
  useEffect(() => {
    if (!active) return
    const id = window.setTimeout(() => setActive(false), durationMs)
    return () => window.clearTimeout(id)
  }, [seen, active, durationMs])
  return active
}
