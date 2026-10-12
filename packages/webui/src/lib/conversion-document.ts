import type { ConversionPolicy, ConversionRule } from './conversion-policy'
import { ProtocolDocument } from './protocol-document'

export const RULE_ORDER_STEP = 1000
type ConversionParseResult = { ok: true; document: ProtocolDocument; policy: ConversionPolicy } | { ok: false; error: string }
const isObject = (value: unknown): value is Record<string, unknown> => value !== null && typeof value === 'object' && !Array.isArray(value)

/** Validate the form's shape; arbitrary rule payloads remain in the source document. */
export function parseConversionDocument(source: string): ConversionParseResult {
  try {
    const document = new ProtocolDocument(source)
    const policy: unknown = JSON.parse(source)
    if (!isObject(policy)) throw new Error('/：策略必须是对象')
    if (typeof policy.id !== 'string') throw new Error('/id：必须是字符串')
    if (policy.name !== undefined && typeof policy.name !== 'string') throw new Error('/name：必须是字符串')
    if (!Array.isArray(policy.rules)) throw new Error('/rules：必须是数组')
    for (const [index, rule] of policy.rules.entries()) {
      const path = `/rules/${index}`
      if (!isObject(rule)) throw new Error(`${path}：规则必须是对象`)
      for (const key of ['id', 'phase', 'action', 'path', 'reason']) {
        if (rule[key] !== undefined && typeof rule[key] !== 'string') throw new Error(`${path}/${key}：必须是字符串`)
      }
      if (rule.order !== undefined && (typeof rule.order !== 'number' || !Number.isSafeInteger(rule.order))) throw new Error(`${path}/order：必须是安全整数`)
      if (rule.enabled !== undefined && typeof rule.enabled !== 'boolean') throw new Error(`${path}/enabled：必须是布尔值`)
      if (rule.match !== undefined) {
        if (!isObject(rule.match)) throw new Error(`${path}/match：必须是对象`)
        for (const [key, value] of Object.entries(rule.match)) {
          if (key !== 'present' && typeof value !== 'string') throw new Error(`${path}/match/${key}：必须是字符串`)
        }
      }
    }
    for (const key of ['continuation', 'usage']) {
      if (policy[key] !== undefined && !isObject(policy[key])) throw new Error(`/${key}：必须是对象`)
    }
    return { ok: true, document, policy: policy as unknown as ConversionPolicy }
  } catch (error) {
    return { ok: false, error: String(error) }
  }
}

/** Patch explicit form fields only; untouched JSON numbers never round-trip through JS. */
export function patchConversionFields(source: string, pointer: string, fields: Record<string, unknown>): string {
  let document = new ProtocolDocument(source)
  if (document.locate(pointer)?.kind !== 'object') document = new ProtocolDocument(document.set(pointer, '{}'))
  for (const [key, value] of Object.entries(fields)) {
    const path = `${pointer}/${key.replace(/~/g, '~0').replace(/\//g, '~1')}`
    document = new ProtocolDocument(value === undefined ? document.remove(path) : document.set(path, JSON.stringify(value)))
  }
  return document.source
}

/** Reorder exact rule fragments, changing only their explicit execution order. */
export function moveConversionRule(source: string, index: number, offset: number): string {
  const document = new ProtocolDocument(source)
  const rules = [...document.locate('/rules')!.children.keys()].map((key) => document.read(`/rules/${key}`)!)
  const other = index + offset
  if (other < 0 || other >= rules.length) return source
  ;[rules[index], rules[other]] = [rules[other], rules[index]]
  return document.set('/rules', `[${rules.map((rule, i) => new ProtocolDocument(rule).set('/order', String((i + 1) * RULE_ORDER_STEP))).join(',')}]`)
}

/** Append a newly authored rule without serializing existing arbitrary values. */
export function appendConversionRule(source: string, rule: ConversionRule): string {
  const document = new ProtocolDocument(source)
  const rules = document.read('/rules')!
  return document.set('/rules', `${rules.slice(0, -1)}${document.locate('/rules')!.children.size ? ',' : ''}${JSON.stringify(rule)}]`)
}
