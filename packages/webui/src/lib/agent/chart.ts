/** ```chart 围栏规格解析（聊天内嵌图表）。 */

export interface AgentChartSpec {
  type: 'bar' | 'line' | 'pie'
  title?: string
  x?: unknown[]
  series?: { name?: string; data?: unknown[] }[]
}

export function parseChartSpec(raw: string): AgentChartSpec | null {
  try {
    const parsed = JSON.parse(raw) as AgentChartSpec
    if (!parsed || (parsed.type !== 'bar' && parsed.type !== 'line' && parsed.type !== 'pie')) return null
    if (!Array.isArray(parsed.x) || !Array.isArray(parsed.series) || parsed.series.length === 0) return null
    return parsed
  } catch {
    return null
  }
}
