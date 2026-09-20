/** 工具参数的界面脱敏（apiKey 形态只保留首尾片段）。 */

function maskSensitive(value: unknown): string {
  const text = typeof value === 'string' ? value : JSON.stringify(value)
  if (!text) return ''
  if (/(sk-|key|token|secret)/i.test(text) && text.length > 8) {
    return text.slice(0, 4) + '***' + text.slice(-4)
  }
  return text
}

/** 工具参数的安全展示：深拷贝后对敏感键脱敏，再格式化为 JSON。 */
export function toolArgsPreview(args: unknown): string {
  if (args == null) return ''
  if (typeof args === 'string') return maskSensitive(args)
  try {
    const clone = JSON.parse(JSON.stringify(args)) as Record<string, unknown>
    for (const key of Object.keys(clone)) {
      if (/apikey|secret|token/i.test(key) && typeof clone[key] === 'string') {
        clone[key] = maskSensitive(clone[key])
      }
    }
    return JSON.stringify(clone, null, 2)
  } catch {
    return maskSensitive(args)
  }
}
