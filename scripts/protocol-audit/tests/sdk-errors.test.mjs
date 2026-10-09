import './network-guard.mjs'
import test from 'node:test'
import assert from 'node:assert/strict'
import { createServer } from 'node:http'
import { consumeResponsesFailure } from '../src/sdk-errors.mjs'
import { replyWire } from './fixtures.mjs'

async function endpoint(t, body) {
  const server = createServer((req, res) => { req.resume(); res.writeHead(200, { 'content-type': 'text/event-stream' }); res.end(body) })
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve))
  t.after(async () => { server.closeAllConnections(); await new Promise(resolve => server.close(resolve)) })
  return `http://127.0.0.1:${server.address().port}`
}
const original = replyWire('responses', 'hi', false, true).split('\n').filter(line => line.startsWith('data: ')).map(line => JSON.parse(line.slice(6)))
const delta = original.findIndex(event => event.type === 'response.output_text.delta')
function failingWire(error) {
  return [...original.slice(0, delta + 1), error].map((event, i) => `event: ${event.type}\ndata: ${JSON.stringify({ ...event, sequence_number: 41 + i * 2 })}\n\n`).join('')
}
for (const nested of [true, false]) test(`Responses ${nested ? 'nested' : 'flat'} errors reach both SDKs after text`, async t => {
  const payload = { message: 'synthetic provider failed', code: null, param: null }
  const body = failingWire(nested ? { type: 'error', error: { ...payload, type: 'api_error' } } : { type: 'error', ...payload })
  const result = await consumeResponsesFailure({ baseUrl: await endpoint(t, body), cause: payload.message })
  assert.equal(result.length, 2)
})
test('Responses error replay rejects successful EOF as failure evidence', async t => {
  const baseUrl = await endpoint(t, replyWire('responses', 'hi', false, true))
  await assert.rejects(consumeResponsesFailure({ baseUrl, cause: 'synthetic provider failed' }), /did not report exactly one failure/)
})
test('Responses error replay rejects unrelated SDK validation errors', async t => {
  const baseUrl = await endpoint(t, failingWire({ type: 'error', error: { type: 'api_error', message: 42 } }))
  await assert.rejects(consumeResponsesFailure({ baseUrl, cause: 'synthetic provider failed' }))
})
