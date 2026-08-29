import { clsx, type ClassValue } from 'clsx'
import { twMerge } from 'tailwind-merge'
import type { Model } from '@/lib/types'

/** Tailwind class 合并（后者覆盖前者同前缀）。 */
export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs))
}

/**
 * 模型源筛选是纯前端实现：选中源 → 该源下全部模型名，与「模型」筛选取交集后
 * 走后端已有的 modelName 多值 IN 过滤（零后端改动）。
 * 交集为空且确有筛选输入时返回 [NO_MATCH_MODEL_FILTER]：这个哨兵值在后端
 * 匹配不到任何行，页面展示显式空结果——而不是把空数组当"未筛选"静默丢掉
 * 筛选条件、回退成全量数据。两侧都未选时返回空数组（= 不过滤）。
 */
export const NO_MATCH_MODEL_FILTER = 'ￗno-match'

export function effectiveModelFilter(
  modelNames: string[],
  sourceNames: string[],
  models: Model[],
  modelsLoaded = true,
): string[] {
  if (sourceNames.length === 0) return modelNames
  // 模型目录尚未加载（或加载失败）时不做源→模型交集：空目录会把任何源
  // 选择误判为"无命中"，页面显示永久空态。
  if (!modelsLoaded) return modelNames
  const set = new Set(sourceNames)
  const fromSources = models.filter((m) => m.sourceName && set.has(m.sourceName)).map((m) => m.name)
  if (modelNames.length === 0) {
    return fromSources.length > 0 ? fromSources : [NO_MATCH_MODEL_FILTER]
  }
  const sourceSet = new Set(fromSources)
  const matched = modelNames.filter((name) => sourceSet.has(name))
  return matched.length > 0 ? matched : [NO_MATCH_MODEL_FILTER]
}

/** 千分位格式化数字；空值显示 0。 */
export function formatNumber(value: number | undefined | null): string {
  if (value == null || Number.isNaN(value)) return '0'
  return new Intl.NumberFormat('zh-CN').format(value)
}

/** 字节数人性化显示（KB/MB/GB），保留两位小数。 */
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

/** 毫秒时长人性化显示（ms/s/min/h）。 */
export function formatDuration(ms: number | undefined | null): string {
  if (ms == null || Number.isNaN(ms)) return '-'
  if (ms < 1000) return `${Math.round(ms)} ms`
  return `${(ms / 1000).toFixed(2)} s`
}

/** ISO 时间转本地 YYYY-MM-DD HH:mm:ss 显示。 */
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

/** 相对时间（刚刚/N 分钟前/…），超过阈值回退绝对时间。 */
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

/** 分子/分母 百分比字符串（保留一位小数）；分母为 0 显示 —。 */
export function percent(part: number, total: number): string {
  if (!total) return '0%'
  return `${((part / total) * 100).toFixed(1)}%`
}

/** cacheHitRate（0–1）→ 百分比文案。 */
export function formatHitRate(rate: number): string {
  return `${(rate * 100).toFixed(1)}%`
}

/** Recharts 轴刻度共用样式。 */
export const CHART_TICK = {
  fontFamily: 'ui-monospace, SF Mono, Menlo, monospace',
  fontSize: 11,
  fill: 'hsl(var(--muted-foreground))',
} as const

/** 紧凑数字：1234 → 1.2k，1200000 → 1.2m。用于 TPM 等大数值的大字展示。 */
export function compactNumber(value: number | undefined | null): string {
  const n = Number(value || 0)
  const abs = Math.abs(n)
  if (abs < 1000) return String(Math.round(n))
  if (abs < 1_000_000) return `${(n / 1000).toFixed(abs < 10_000 ? 1 : 0).replace(/\.0$/, '')}k`
  if (abs < 1_000_000_000) return `${(n / 1_000_000).toFixed(abs < 10_000_000 ? 1 : 0).replace(/\.0$/, '')}m`
  return `${(n / 1_000_000_000).toFixed(1).replace(/\.0$/, '')}b`
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


/** 时间窗起点 ISO（24h/7d/30d；all 返回 undefined 表示无下界）。 */
export function startOfRange(range: '24h' | '7d' | '30d' | 'all', nowIso?: string): string | undefined {
  if (range === 'all') return undefined
  const reference = nowIso ? new Date(nowIso).getTime() : Date.now()
  const map: Record<string, number> = {
    '24h': 24 * 60 * 60 * 1000,
    '7d': 7 * 24 * 60 * 60 * 1000,
    '30d': 30 * 24 * 60 * 60 * 1000,
  }
  return new Date(reference - map[range]).toISOString()
}

/** 把参考时刻向下取整到 bucketMs 边界再序列化：作为 usage 查询的 to 参数时，
 *  缓存键在桶内保持稳定，空闲自动刷新从每分钟一次降为每桶一次。 */
export function bucketedTimeISO(atMs: number, bucketMs: number): string {
  return new Date(Math.floor(atMs / bucketMs) * bucketMs).toISOString()
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
  if (content == null || content === '') return ''
  try {
    return JSON.parse(content)
  } catch {
    return content
  }
}

/** 判断 HTTP 状态码是否属于成功区间（2xx/3xx）。 */
export function isSuccessStatus(code: number): boolean {
  return code >= 200 && code < 400
}

/** 模型检索谓词：按 id / 名称 / 所属源名的子串匹配（忽略大小写）。 */
export function matchesModelKeyword(keyword: string, model: { id: string; name: string; sourceName?: string }): boolean {
  const kw = keyword.trim().toLowerCase()
  if (!kw) return true
  return `${model.id} ${model.name} ${model.sourceName ?? ''}`.toLowerCase().includes(kw)
}
