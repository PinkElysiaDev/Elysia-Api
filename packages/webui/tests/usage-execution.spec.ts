import { expect, test } from '@playwright/test'
import { readUsageLogDetail } from '../src/lib/usage-log-detail'

const largeBodyBytes = 9 << 20

test('usage detail keeps exact execution JSON through export serialization', () => {
  const ledger = '{"executionSpeed":"fast","executions":[{"model":"advisor","input":{"count":9007199254740993,"origin":"observed"},"topLevelRelation":"excluded"}]}'
  const source = '{"ok":true,"data":{"requestId":"r","incomingBody":' + JSON.stringify({ content: '"protocolUsage":{"fake":true}' }) + ',"protocolUsage":' + ledger + '}}'
  const detail = readUsageLogDetail(source)
  expect(detail.protocolUsageJSON).toBe(ledger)
  expect('protocolUsage' in detail).toBe(false)
  const exported = JSON.parse(JSON.stringify(detail))
  expect(exported.protocolUsageJSON).toBe(ledger)
})

test('usage detail preserves absent and null facts without an editor payload cap', () => {
  expect(readUsageLogDetail('{"ok":true,"data":{"requestId":"r"}}').protocolUsageJSON).toBeUndefined()
  expect(readUsageLogDetail('{"ok":true,"data":{"protocolUsage":null}}').protocolUsageJSON).toBe('null')
  const source = JSON.stringify({ ok: true, data: { incomingBody: { content: 'x'.repeat(largeBodyBytes) }, protocolUsage: { executions: [] } } })
  expect(readUsageLogDetail(source).protocolUsageJSON).toBe('{"executions":[]}')
})

test('usage detail rejects non-object envelopes before scanning spans', () => {
  for (const source of ['null', '[]', '{}', '{"data":null}', '{"data":false}', '{"data":[]}', '{"data":42}', '{"data":"text"}']) {
    expect(() => readUsageLogDetail(source)).toThrow('Usage log detail must be an object')
  }
})

test('usage detail selects the same last member as JSON parsing', () => {
  const source = '{"data":null,"data":{"requestId":"final","protocolUsage":null,"protocolUsage":{"executions":[]}}}'
  const detail = readUsageLogDetail(source)
  expect(detail.requestId).toBe('final')
  expect(detail.protocolUsageJSON).toBe('{"executions":[]}')
  expect(() => readUsageLogDetail('{"data":{},"data":null}')).toThrow('Usage log detail must be an object')
})
