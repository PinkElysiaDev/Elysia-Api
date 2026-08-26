import { useMemo } from 'react'
import { compactNumber, formatNumber } from '@/lib/utils'

interface ModelBreakdownTooltipProps {
  active?: boolean
  payload?: Array<{
    name?: string
    value?: number | string
    color?: string
    /** recharts 系列的 tooltipType；'none' 表示装饰系列（如渐变面积），不展示。 */
    type?: string
    payload?: {
      date?: string
      label?: string
      tok?: number
      req?: number
      tokens?: number
      requests?: number
      successRequests?: number
      failedRequests?: number
      inputTokens?: number
      outputTokens?: number
      cacheHitTokens?: number
      modelTokens?: Record<string, number>
    }
  }>
  label?: string
  mode?: 'token' | 'request' | 'both'
}

const MODEL_PALETTE = [
  '#F43F85', // 瑰梅粉
  '#0DA574', // 翡翠绿
  '#818CF8', // 靛蓝
  '#F59E0B', // 暖琥珀
  '#A78BFA', // 兰花紫
  '#06B6D4', // 青碧
  '#94A3B8', // 灰蓝
]

/**
 * 趋势图交互悬浮浮层：悬停在 Token 柱或折线点上时展示总览与模型级细分。
 */
export function ModelBreakdownTooltip({ active, payload, label, mode = 'both' }: ModelBreakdownTooltipProps) {
  const rawData = payload?.[0]?.payload
  const modelTokens = rawData?.modelTokens

  // 排序并格式化模型细分明细。
  // 注意：所有 Hook 必须在任何 early return 之前调用——此前 return null 写在
  // useMemo 之前，违反 Hooks 规则，active 切换时会触发 "Rendered fewer hooks
  // than expected" 崩溃。
  const breakdown = useMemo(() => {
    if (!modelTokens) return []
    const entries = Object.entries(modelTokens).filter(([, tok]) => tok > 0)
    entries.sort((a, b) => b[1] - a[1])

    const total = entries.reduce((acc, [, val]) => acc + val, 0)
    if (total === 0) return []

    const top = entries.slice(0, 5)
    const rest = entries.slice(5)
    const restTotal = rest.reduce((acc, [, val]) => acc + val, 0)

    const list = top.map(([mName, mTok], idx) => ({
      name: mName || '未标记模型',
      tokens: mTok,
      ratio: (mTok / total) * 100,
      color: MODEL_PALETTE[idx % MODEL_PALETTE.length],
    }))

    if (restTotal > 0) {
      list.push({
        name: `其他 ${rest.length} 个模型`,
        tokens: restTotal,
        ratio: (restTotal / total) * 100,
        color: '#64748B',
      })
    }

    return list
  }, [modelTokens])

  if (!active || !payload?.length) return null

  return (
    <div className="min-w-[220px] max-w-[320px] rounded-xl border border-border/80 bg-card/95 p-3 text-xs shadow-xl backdrop-blur-md tnum">
      {/* 头部：日期（请求次数不在此重复展示——指标行的「请求数」已覆盖） */}
      <div className="flex items-center border-b border-border/60 pb-2">
        <span className="font-mono text-2xs font-semibold text-muted-foreground">{rawData?.date || label}</span>
      </div>

      {/* 指标总览项（过滤装饰系列：recharts 只在默认 tooltip 内容里过滤
          type === 'none'，自定义内容需自行遵守该约定） */}
      <div className="space-y-1.5 py-2">
        {payload
          .filter((entry) => entry.type !== 'none')
          .map((entry) => (
            <div key={entry.name} className="flex items-center justify-between gap-2">
              <span className="flex items-center gap-1.5 text-muted-foreground">
                <i className="h-2 w-2 rounded-full" style={{ background: entry.color }} aria-hidden />
                {entry.name}
              </span>
              <b className="font-mono font-semibold text-foreground">
                {typeof entry.value === 'number' ? formatNumber(entry.value) : entry.value}
              </b>
            </div>
          ))}
      </div>

      {/* 模型级细分明细（若当前桶包含模型细分数据） */}
      {breakdown.length > 0 && mode !== 'request' && (
        <div className="mt-1 border-t border-border/60 pt-2.5">
          <div className="mb-2 flex items-center justify-between">
            <span className="text-2xs font-semibold uppercase tracking-wider text-muted-foreground">
              模型消耗明细
            </span>
            <span className="text-2xs text-muted-foreground/80">占比</span>
          </div>
          <div className="space-y-2">
            {breakdown.map((item) => (
              <div key={item.name} className="space-y-1">
                <div className="flex items-center justify-between gap-2 text-2xs">
                  <span className="flex items-center gap-1.5 truncate text-foreground font-mono" title={item.name}>
                    <span className="h-1.5 w-1.5 shrink-0 rounded-full" style={{ backgroundColor: item.color }} />
                    <span className="truncate">{item.name}</span>
                  </span>
                  <div className="flex items-center gap-1.5 shrink-0 font-mono text-muted-foreground">
                    <span className="font-semibold text-foreground">{compactNumber(item.tokens)}</span>
                    <span className="text-2xs opacity-75">({item.ratio.toFixed(1)}%)</span>
                  </div>
                </div>
                {/* 占比迷你进度条 */}
                <div className="h-1 w-full overflow-hidden rounded-full bg-secondary/80">
                  <div
                    className="h-full rounded-full transition-all duration-300"
                    style={{
                      width: `${Math.max(item.ratio, 2)}%`,
                      backgroundColor: item.color,
                    }}
                  />
                </div>
              </div>
            ))}
          </div>
        </div>
      )}
    </div>
  )
}
