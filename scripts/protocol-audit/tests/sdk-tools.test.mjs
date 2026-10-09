import './network-guard.mjs'
import test from 'node:test'
import assert from 'node:assert/strict'
import { createServer } from 'node:http'
import { readFile } from 'node:fs/promises'
import { replyWire } from './fixtures.mjs'
import { consumeToolRoundTrip } from '../src/sdk-tools.mjs'

// Synthetic native captures: two same-name functions with different IDs and
// inputs, mixed text, and reverse-order completion of their argument fragments.
const first = JSON.parse(await readFile(new URL('./fixtures/parallel-tools.json', import.meta.url), 'utf8'))

async function endpoint(t, protocol, stream, mutate = v => v) {
  const bodies = [], checks = []
  const server = createServer(async (req, res) => {
    const chunks = []
    for await (const chunk of req) chunks.push(chunk)
    const body = JSON.parse(Buffer.concat(chunks).toString('utf8'))
    bodies.push(body)
    const second = bodies.length % 2 === 0
    res.writeHead(200, { 'content-type': stream ? 'text/event-stream' : 'application/json' })
    res.end(mutate(second ? replyWire(protocol, 'hi', false, stream) : first[`${protocol}/${stream}`], second))
  })
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve))
  t.after(async () => { server.closeAllConnections(); await new Promise(resolve => server.close(resolve)) })
  return { bodies, checks, options: { baseUrl: `http://127.0.0.1:${server.address().port}`, onExchange: e => { checks.push(e) } } }
}

for (const protocol of ['chat', 'responses', 'anthropic', 'gemini']) for (const stream of [false, true]) {
  test(`${protocol} ${stream ? 'SSE' : 'JSON'} SDK sends both parallel results and complete history`, async t => {
    const f = await endpoint(t, protocol, stream)
    const used = await consumeToolRoundTrip(protocol, f.options, stream)
    assert.equal(used.length, protocol === 'gemini' ? 1 : 2)
    assert.equal(f.bodies.length, used.length * 2)
    assert.equal(f.checks.length, f.bodies.length)
    for (const [i, body] of f.bodies.entries()) {
      if (i % 2 === 0) continue
      const wire = JSON.stringify(body)
      for (const id of ['c1', 'c2']) { assert.ok(wire.includes(`result-${id}`)); assert.ok(wire.includes(id)) }
      assert.ok(!wire.includes('parsed_arguments') && !wire.includes('"parsed":'), 'SDK-only parsing properties leaked into wire history')
      if (protocol === 'responses') assert.equal(body.store, false)
    }
    assert.ok(used.every(u => u.rounds === 2 && u.requests === 2 && u.calls.length === 2))
  })
}

test('tool SDK rejects swapped parallel arguments before sending the second request', async t => {
  const f = await endpoint(t, 'chat', false, (wire, second) => {
    if (second) return wire
    const data = JSON.parse(wire)
    data.choices[0].message.tool_calls.reverse()
    for (const [i, call] of data.choices[0].message.tool_calls.entries()) call.function.arguments = JSON.stringify({ n: i + 1 })
    return JSON.stringify(data)
  })
  await assert.rejects(consumeToolRoundTrip('chat', f.options, false), /changed arguments/)
  assert.equal(f.bodies.length, 1)
})

test('tool SDK rejects the original sparse Chat tool indexes', async t => {
  const f = await endpoint(t, 'chat', true, (wire, second) => second ? wire : wire.split('\n').map(line => {
    if (!line.startsWith('data: {')) return line
    const chunk = JSON.parse(line.slice(6))
    for (const choice of chunk.choices || []) for (const call of choice.delta?.tool_calls || []) call.index++
    return `data: ${JSON.stringify(chunk)}`
  }).join('\n'))
  await assert.rejects(consumeToolRoundTrip('chat', f.options, true))
  assert.equal(f.bodies.length, 1)
})

test('tool SDK does not retry a failed second round', async t => {
  const f = await endpoint(t, 'responses', true, (wire, second) => second
    ? 'event: error\ndata: {"type":"error","sequence_number":0,"error":{"type":"api_error","message":"synthetic second round failure"}}\n\n' : wire)
  await assert.rejects(consumeToolRoundTrip('responses', f.options, true), /synthetic second round failure/)
  assert.equal(f.bodies.length, 2)
})

test('tool SDK rejects hidden Vercel errors even after a finish event', async t => {
  const f = await endpoint(t, 'anthropic', true)
  let exchanges = 0
  const fetch = globalThis.fetch
  globalThis.fetch = async (input, options) => {
    const response = await fetch(input, options)
    exchanges++
    // Let the official SDK finish both rounds, then corrupt only the AI SDK.
    if (exchanges !== 4) return response
    return new Response((await response.text()) + 'event: error\ndata: {"type":"error","error":{"type":"api_error","message":"hidden tool round error"}}\n\n', { status: 200, headers: response.headers })
  }
  try { await assert.rejects(consumeToolRoundTrip('anthropic', f.options, true), error => error.message === 'hidden tool round error') }
  finally { globalThis.fetch = fetch }
  assert.equal(f.bodies.length, 4)
})
