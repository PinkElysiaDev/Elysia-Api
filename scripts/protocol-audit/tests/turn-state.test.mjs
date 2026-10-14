import './network-guard.mjs'
import test from 'node:test'
import assert from 'node:assert/strict'
import { redactor } from '../src/audit.mjs'
import { inspectReply } from '../src/protocols.mjs'
import { replyWire } from './fixtures.mjs'

test('turn state is redacted in objects, nested wire strings and SSE without rounding integers', () => {
  const clean = redactor([])
  const raw = '{"type":"response.metadata","headers":{"X-Codex-Turn-State":"synthetic-private-state"},"n":9007199254740993}'
  for (const value of [raw, { body: raw }, JSON.stringify({ body: raw }), `event: response.metadata\ndata: ${raw}\n\n`]) {
    const result = JSON.stringify(clean(value))
    assert.doesNotMatch(result, /synthetic-private-state/)
    assert.match(result, /REDACTED/)
    assert.match(result, /9007199254740993/)
  }
  assert.equal(clean({ headers: { 'x-codex-turn-state': 'synthetic-private-state' } }).headers['x-codex-turn-state'], '[REDACTED]')
})

test('native compliance and explicit gateway control compatibility remain separate', () => {
  const frame = 'data: {"type":"response.metadata","headers":{"x-codex-turn-state":"synthetic-state"}}\n\n'
  const raw = frame + replyWire('responses', 'OK', false, true)
  assert.ok(inspectReply('responses', raw, true).issues.length)
  const compatible = inspectReply('responses', raw, true, true, { gatewayCompatibility: true })
  assert.deepEqual(compatible.issues, [])
  assert.equal(compatible.text, 'OK')
  assert.equal(compatible.warnings[0].code, 'gateway_control_compatibility')
  for (const invalid of [frame.replace('"synthetic-state"', 'null'), frame.replace('"headers":', '"extra":1,"headers":'), frame.replace('"synthetic-state"', '"s\\nt"')]) {
    assert.ok(inspectReply('responses', invalid + replyWire('responses', 'OK', false, true), true, true, { gatewayCompatibility: true }).issues.length)
  }
  assert.ok(inspectReply('responses', replyWire('responses', 'OK', false, true) + frame, true, true, { gatewayCompatibility: true }).issues.length)
})

test('invalid or missing Responses sequence does not poison the last legal watermark', () => {
  for (const invalid of [undefined, null, 'bad', -1, 0]) {
    const frames = replyWire('responses', 'OK', false, true).split('\n\n').filter(Boolean)
    const extra = { type: 'response.in_progress', sequence_number: invalid }
    frames.splice(1, 0, `data: ${JSON.stringify(extra)}`)
    const issues = inspectReply('responses', frames.join('\n\n')+'\n\n', true).issues
    assert.ok(issues.some(issue => issue.path === '/frames/1/sequence_number'))
    assert.ok(!issues.some(issue => issue.path === '/frames/2/sequence_number'), JSON.stringify(issues))
  }
})

test('partial turn-state captures are redacted before report truncation', () => {
  const clean = redactor([])
  for (const raw of ['{"x-codex-turn-state":"synthetic-private', '{"body":"{\\"x-codex-turn-state\\":\\"synthetic-private', 'data: {"x-codex-turn-state":"synthetic-private']) {
    assert.doesNotMatch(clean(raw), /synthetic-private/)
  }
})
