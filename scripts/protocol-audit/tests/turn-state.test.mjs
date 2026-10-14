import './network-guard.mjs'
import test from 'node:test'
import assert from 'node:assert/strict'
import { redactor } from '../src/audit.mjs'

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
