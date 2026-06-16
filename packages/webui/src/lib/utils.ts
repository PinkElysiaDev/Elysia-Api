import { clsx, type ClassValue } from 'clsx'
import { twMerge } from 'tailwind-merge'

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs))
}

export function formatNumber(value: number | undefined | null): string {
  if (value == null || Number.isNaN(value)) return '0'
  return new Intl.NumberFormat('zh-CN').format(value)
}

export function formatBytes(bytes: number | undefined | null): string {
  if (!bytes || bytes < 0) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let value = bytes
  let unit = 0
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024
    unit += 1
  }
  return `${value.toFixed(value >= 100 || unit === 0 ? 0 : 1)} ${units[unit]}`
}

export function formatDuration(ms: number | undefined | null): string {
  if (ms == null || Number.isNaN(ms)) return '-'
  if (ms < 1000) return `${Math.round(ms)} ms`
  return `${(ms / 1000).toFixed(2)} s`
}

export function formatDateTime(value: string | number | Date | undefined | null): string {
  if (!value) return '-'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return '-'
  return new Intl.DateTimeFormat('zh-CN', {
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
    hour12: false,
  }).format(date)
}

export function formatRelative(value: string | number | Date | undefined | null): string {
  if (!value) return '-'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return '-'
  const diff = Date.now() - date.getTime()
  const abs = Math.abs(diff)
  const minute = 60_000
  const hour = 60 * minute
  const day = 24 * hour
  if (abs < minute) return '刚刚'
  if (abs < hour) return `${Math.round(abs / minute)} 分钟前`
  if (abs < day) return `${Math.round(abs / hour)} 小时前`
  if (abs < 30 * day) return `${Math.round(abs / day)} 天前`
  return formatDateTime(date)
}

export function percent(part: number, total: number): string {
  if (!total) return '0%'
  return `${((part / total) * 100).toFixed(1)}%`
}

/** 紧凑数字：1234 → 1.2k，1200000 → 1.2m。用于 TPM 等大数值的大字展示。 */
export function compactNumber(value: number | undefined | null): string {
  const n = Number(value || 0)
  const abs = Math.abs(n)
  if (abs < 1000) return String(Math.round(n))
  if (abs < 1_000_000) return `${(n / 1000).toFixed(abs < 10_000 ? 1 : 0).replace(/\.0$/, '')}k`
  if (abs < 1_000_000_000) return `${(n / 1_000_000).toFixed(abs < 10_000_000 ? 1 : 0).replace(/\.0$/, '')}m`
  return `${(n / 1_000_000_000).toFixed(1).replace(/\.0$/, '')}b`
}

/**
 * 速率（每分钟）。count 为请求数或 token 数，from/to 为 ISO 时间串。
 * 跨度无效（缺失或非正）时返回 null，由调用方决定展示占位符。
 */
export function ratePerMinute(
  count: number | undefined | null,
  from: string | undefined | null,
  to: string | undefined | null,
): number | null {
  if (!from || !to) return null
  const start = new Date(from).getTime()
  const end = new Date(to).getTime()
  if (Number.isNaN(start) || Number.isNaN(end)) return null
  const minutes = (end - start) / 60_000
  if (minutes <= 0) return null
  return Number(count || 0) / minutes
}

/** 去重 + 去空 + 按中文/字母序排序，返回 {value,label} 选项数组，供多选下拉使用。 */
export function uniqueSorted(values: (string | undefined | null)[]): { value: string; label: string }[] {
  const seen = new Set<string>()
  const result: string[] = []
  for (const raw of values) {
    const v = (raw ?? '').trim()
    if (!v || seen.has(v)) continue
    seen.add(v)
    result.push(v)
  }
  result.sort((a, b) => a.localeCompare(b, 'zh-Hans-CN'))
  return result.map((v) => ({ value: v, label: v }))
}

export function maskMiddle(value: string | undefined): string {
  if (!value) return ''
  if (value.length <= 8) return '***'
  return `${value.slice(0, 4)}…${value.slice(-4)}`
}

/** RFC3339 时间戳，供 usage 查询参数使用。 */
export function toRFC3339(date: Date): string {
  return date.toISOString()
}

export function startOfRange(range: '24h' | '7d' | '30d' | 'all'): string | undefined {
  if (range === 'all') return undefined
  const now = Date.now()
  const map: Record<string, number> = {
    '24h': 24 * 60 * 60 * 1000,
    '7d': 7 * 24 * 60 * 60 * 1000,
    '30d': 30 * 24 * 60 * 60 * 1000,
  }
  return new Date(now - map[range]).toISOString()
}

/** 把任意数据序列化为 JSON 文件并触发浏览器下载。 */
export function downloadJSON(filename: string, data: unknown): void {
  const blob = new Blob([JSON.stringify(data, null, 2)], { type: 'application/json' })
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = filename
  document.body.appendChild(a)
  a.click()
  a.remove()
  URL.revokeObjectURL(url)
}

/** 尽量把字符串解析成 JSON 对象；失败则原样返回字符串。用于展示/导出捕获的请求体。 */
export function tryParseJSON(content: string | undefined | null): unknown {
  if (content == null || content === '') return content ?? ''
  try {
    return JSON.parse(content)
  } catch {
    return content
  }
}
