import test from 'node:test'
import assert from 'node:assert/strict'
import { createServer } from 'node:http'
import './network-guard.mjs'
import { presetIDs, runtimeReadiness, waitForRuntime, persistedCall, eventSequence, failureCategory } from '../src/evidence.mjs'
import { inspectReply } from '../src/protocols.mjs'
import { replyWire } from './fixtures.mjs'

const ready = () => ({ runtimeReady: true, active: presetIDs.map(protocolId => ({ protocolId, revisionHash: `hash-${protocolId}` })), loaded: Object.fromEntries(presetIDs.map(id => [id, `hash-${id}`])), runtimeFailures: {} })
async function serve(t, handler) {
  const server = createServer(handler)
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve))
  t.after(() => new Promise(resolve => server.close(resolve)))
  return `http://127.0.0.1:${server.address().port}`
}

test('health, preset names and stale activations cannot substitute for loaded ready runtime', () => {
  assert.equal(runtimeReadiness(ready()).ready, true)
  for (const mutate of [s => { s.runtimeReady = false }, s => { delete s.loaded[presetIDs[0]] }, s => { s.active[0].revisionHash = 'stale' }, s => { s.runtimeFailures[presetIDs[0]] = 'failed verification' }, s => { s.startupFailure = { stage: 'migration', message: 'blocked' } }]) {
    const state = ready(); mutate(state)
    assert.equal(runtimeReadiness(state).ready, false)
  }
  assert.equal(runtimeReadiness({ presets: presetIDs, runtimeReady: true }).ready, false)
})

test('startup and each restart check actual revisions and retain structured failures', async t => {
  let count = 0, broken = false
  const url = await serve(t, (req, res) => {
    assert.equal(req.url, '/api/admin/protocols')
    const state = ready(); state.runtimeReady = ++count > 1
    if (broken) state.startupFailure = { stage: 'load', code: 'invalid_protocol', reason: 'specific defect' }
    res.end(JSON.stringify({ ok: true, data: state }))
  })
  assert.deepEqual(await waitForRuntime(url, 'test', { intervalMs: 1 }), ready())
  broken = true
  await assert.rejects(waitForRuntime(url, 'test', { intervalMs: 1 }), error => error.code === 'runtime_not_ready' && error.startupFailure.state.startupFailure.reason === 'specific defect')
})

test('persisted call lookup is exact, waits for asynchronous storage, and never guesses by time', async t => {
  let count = 0
  const url = await serve(t, (req, res) => {
    assert.equal(req.url, '/api/admin/usage/logs/req_123_abcd')
    if (++count === 1) { res.writeHead(404); res.end('{"ok":false}'); return }
    res.end(JSON.stringify({ ok: true, data: { requestId: 'req_123_abcd', conversionIssues: [] } }))
  })
  assert.equal((await persistedCall(url, 'test', 'req_123_abcd', { intervalMs: 1 })).record.requestId, 'req_123_abcd')
  assert.equal((await persistedCall(url, 'test')).available, false)
  await assert.rejects(persistedCall(url, 'test', '../wrong'), /Invalid gateway/)
})

test('Anthropic final usage is validated on its own frame even if the start contains counters', () => {
  for (const replacement of [{}, { usage: {} }, { usage: { output_tokens: '2' } }]) {
    const raw = replyWire('anthropic', 'OK', false, true).replace(/data: (.+)/g, (line, data) => {
      const value = JSON.parse(data)
      if (value.type !== 'message_delta') return line
      delete value.usage; Object.assign(value, replacement)
      return `data: ${JSON.stringify(value)}`
    })
    assert.ok(inspectReply('anthropic', raw, true).issues.some(i => i.path.includes('/usage')))
  }
  assert.deepEqual(inspectReply('anthropic', replyWire('anthropic', 'OK', false, true), true).issues, [])
})

test('failure classification does not call throttling or model instruction failures conversion defects', () => {
  assert.equal(failureCategory({ kind: 'direct', httpStatuses: [429] }), 'rate_limited')
  assert.equal(failureCategory({ kind: 'direct', issues: [{ code: 'unexpected_text' }] }), 'model_instruction_mismatch')
  assert.equal(failureCategory({ kind: 'direct', issues: [{ code: 'invalid_type' }] }), 'upstream_direct_failure')
  assert.equal(failureCategory({ sdk: {}, record: { conversionIssues: [{ severity: 'error' }] } }), 'gateway_conversion_failure')
  assert.equal(failureCategory({ sdk: {} }), 'sdk_consumption_failure')
  assert.equal(failureCategory({ httpStatuses: [502] }), 'needs_trace_review')
})

test('event order summary contains no text, signature or encrypted state', () => {
  const sequence = eventSequence(replyWire('anthropic', 'private-text', true, true), true)
  assert.equal(sequence[0].type, 'message_start')
  assert.equal(sequence.at(-1).type, 'message_stop')
  assert.doesNotMatch(JSON.stringify(sequence), /private-text|signature-keep|Synthetic reasoning/)
})
