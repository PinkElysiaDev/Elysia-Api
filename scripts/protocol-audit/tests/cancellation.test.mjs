import test from 'node:test'
import assert from 'node:assert/strict'
import { cancellationProbe, assertCancellationRecord } from '../src/cancellation.mjs'

const frame = value => `data: ${JSON.stringify(value)}\n\n`
const content = { choices: [{ index: 0, delta: { content: '测试' } }] }
test('cancel waits for complete substantive SSE, including split UTF-8 and delimiters', () => {
  let aborted = 0
  const probe = cancellationProbe('chat', () => aborted++)
  probe.push(Buffer.from(frame({ choices: [{ index: 0, delta: { role: 'assistant', content: '' } }] })))
  assert.equal(aborted, 0)
  const bytes = Buffer.from(frame(content))
  for (const byte of bytes) probe.push(Uint8Array.of(byte))
  assert.equal(aborted, 1)
  assert.equal(probe.hasContent, true)
})
test('error instead of content and coalesced content/error never pass cancellation', () => {
  for (const prefix of ['', frame(content)]) {
    let aborted = false
    const probe = cancellationProbe('chat', () => { aborted = true })
    assert.throws(() => probe.push(Buffer.from(prefix + frame({ error: { message: 'ID changed' } }))), /ID changed/)
    assert.equal(aborted, false)
  }
})
test('already completed stream is not successful cancellation', () => {
  const probe = cancellationProbe('chat', () => assert.fail('too late to cancel'))
  probe.push(Buffer.from(frame(content) + frame({ choices: [{ finish_reason: 'stop' }] }) + 'data: [DONE]\n\n'))
  assert.equal(probe.hasTerminal, true)
})
test('all client protocols wait for generated text', () => {
  for (const [protocol, value] of [
    ['responses', { type: 'response.output_text.delta', delta: 'hi' }],
    ['anthropic', { type: 'content_block_delta', delta: { type: 'text_delta', text: 'hi' } }],
    ['gemini', { candidates: [{ content: { parts: [{ text: 'hi' }] } }] }],
  ]) {
    let aborted = false
    cancellationProbe(protocol, () => { aborted = true }).push(Buffer.from(frame(value)))
    assert.equal(aborted, true)
  }
})
test('persisted contract/provider failure is not masked by aborting the client', () => {
  assertCancellationRecord({ available: true, record: { error: 'context canceled', errorKind: 'client_canceled', statusCode: 499 } })
  for (const record of [{ error: 'ID changed' }, { error: 'context canceled' }, { error: 'ID changed before context canceled' }, { error: 'context canceled', conversionIssues: [{ severity: 'error', reason: 'invalid id' }] }, { error: 'context canceled', providerFailures: [{}] }, {}]) {
    assert.throws(() => assertCancellationRecord({ available: true, record }))
  }
  assert.throws(() => assertCancellationRecord({ available: false }))
})

test('runtime context cancellation is accepted only with confirmed client cancellation', () => {
  const cancellation = { code: 'invalid_input', stage: 'runtime', direction: 'encode_event', severity: 'error', reason: 'context canceled' }
  const record = { error: 'invalid_input at /: context canceled', errorKind: 'client_canceled', statusCode: 499, conversionIssues: [cancellation] }
  assertCancellationRecord({ available: true, record })
  for (const changes of [
    { errorKind: 'upstream_error' },
    { statusCode: 502 },
    { providerFailures: [{ code: 'upstream_error' }] },
    { conversionIssues: [cancellation, { ...cancellation, reason: 'ID changed' }] },
    { conversionIssues: [{ ...cancellation, reason: 'context canceled after ID changed' }] },
    { conversionIssues: [{ ...cancellation, stage: 'conversion.event' }] },
  ]) assert.throws(() => assertCancellationRecord({ available: true, record: { ...record, ...changes } }))
})
