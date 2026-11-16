import type { UsageLogDetail } from './types'

/** Keep execution counters as exact source JSON while adapting a usage-log response for the UI. */
export function readUsageLogDetail(source: string): UsageLogDetail {
  const detail = (JSON.parse(source) as { data?: UsageLogDetail & { protocolUsage?: unknown } } | null)?.data
  if (detail === null || typeof detail !== 'object' || Array.isArray(detail)) {
    throw new TypeError('Usage log detail must be an object')
  }
  const record = readMemberJSON(source, 'data')!
  const protocolUsageJSON = readMemberJSON(record, 'protocolUsage')
  delete detail.protocolUsage
  if (protocolUsageJSON !== undefined) detail.protocolUsageJSON = protocolUsageJSON
  return detail
}

// The API reader has validated JSON already. Scan spans without the protocol
// editor's unrelated document cap or a numeric conversion of the usage ledger.
function readMemberJSON(source: string, name: string): string | undefined {
  let offset = skipSpace(source, 0) + 1
  let member: string | undefined
  while (source[skipSpace(source, offset)] !== '}') {
    const keyStart = skipSpace(source, offset)
    const keyEnd = readStringEnd(source, keyStart)
    const valueStart = skipSpace(source, skipSpace(source, keyEnd) + 1)
    const valueEnd = readValueEnd(source, valueStart)
    if (JSON.parse(source.slice(keyStart, keyEnd)) === name) member = source.slice(valueStart, valueEnd)
    offset = skipSpace(source, valueEnd)
    if (source[offset] === ',') offset++
  }
  return member
}

function skipSpace(source: string, offset: number): number {
  while (/[ \t\r\n]/.test(source[offset])) offset++
  return offset
}

function readStringEnd(source: string, start: number): number {
  for (let offset = start + 1; ; offset++) {
    if (source[offset] === '\\') offset++
    else if (source[offset] === '"') return offset + 1
  }
}

function readValueEnd(source: string, start: number): number {
  if (source[start] === '"') return readStringEnd(source, start)
  let depth = 0
  for (let offset = start; ; offset++) {
    const token = source[offset]
    if (token === '"') offset = readStringEnd(source, offset) - 1
    else if (token === '{' || token === '[') depth++
    else if (token === '}' || token === ']') {
      if (depth === 0) return offset
      if (--depth === 0) return offset + 1
    } else if (token === ',' && depth === 0) return offset
  }
}
