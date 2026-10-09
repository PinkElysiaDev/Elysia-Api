import test from 'node:test'
import assert from 'node:assert/strict'
import { createServer } from 'node:http'
import { replyWire } from './fixtures.mjs'
import { mapConcurrent } from '../src/concurrency.mjs'
let consume
try { consume = (await import('../src/sdk.mjs')).consume } catch (error) { if (error.code !== 'ERR_MODULE_NOT_FOUND') throw error }

async function endpoint(t, body, stream, status = 200, stall = false) {
  const server = createServer((req, res) => { req.resume(); res.writeHead(status, { 'content-type': stream ? 'text/event-stream' : 'application/json' }); res.write(body); if (!stall) res.end() })
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve))
  t.after(async () => { server.closeAllConnections(); await new Promise(resolve => server.close(resolve)) })
  const captured = []
  return { captured, options: { baseUrl: `http://127.0.0.1:${server.address().port}`, apiKey: 'synthetic-sdk-key', model: 'test', onExchange: detail => { captured.push(detail) } } }
}

for (const protocol of ['chat', 'responses', 'anthropic', 'gemini']) for (const stream of [false, true]) {
  test(`${protocol} ${stream ? 'SSE' : 'JSON'} SDK sends HTTP and records request, response and versions`, { skip: !consume && 'Run npm install in the script directory for SDK self-tests' }, async t => {
    const wire = replyWire(protocol, 'OK', false, stream), f = await endpoint(t, wire, stream)
    const result = await consume(protocol, f.options, stream)
    assert.ok(result.length > 0); assert.ok(result.every(p => typeof p.version === 'string' && p.version.length > 0))
    assert.equal(f.captured.length, protocol === 'gemini' ? 1 : 2)
    for (const detail of f.captured) {
      assert.equal(detail.response.body, wire); assert.equal(detail.response.status, 200)
      assert.match(detail.request.body, /Reply exactly OK/)
      const body = JSON.parse(detail.request.body)
      assert.equal(body.max_completion_tokens ?? body.max_output_tokens ?? body.max_tokens ?? body.generationConfig?.maxOutputTokens, 32768)
      assert.ok(detail.elapsedMs >= 0); assert.ok(detail.firstByteMs >= 0)
    }
  })
}

for (const stream of [false, true]) test(`Responses visible reasoning ${stream ? 'SSE' : 'JSON'} is consumed without hidden SDK errors`, { skip: !consume && 'SDK dependencies unavailable' }, async t => {
  const reasoning = { id: 'rs_visible', type: 'reasoning', status: 'completed', summary: [], content: [{ type: 'reasoning_text', text: 'Synthetic visible thought.' }] }
  // Only synthetic fixtures are parsed here, never live evidence or signatures.
  const response = JSON.parse(replyWire('responses'))
  response.output.unshift(reasoning)
  let wire = JSON.stringify(response)
  if (stream) {
    const original = replyWire('responses', 'OK', false, true).split('\n').filter(line => line.startsWith('data: ')).map(line => JSON.parse(line.slice(6)))
    const ref = { output_index: 0, item_id: reasoning.id, content_index: 0 }
    const reasoningEvents = [
      { type: 'response.output_item.added', output_index: 0, item: { ...reasoning, status: 'in_progress', content: [] } },
      { type: 'response.content_part.added', ...ref, part: { type: 'reasoning_text', text: '' } },
      { type: 'response.reasoning_text.delta', ...ref, delta: reasoning.content[0].text },
      { type: 'response.reasoning_text.done', ...ref, text: reasoning.content[0].text },
      { type: 'response.content_part.done', ...ref, part: reasoning.content[0] },
      { type: 'response.output_item.done', output_index: 0, item: reasoning },
    ]
    for (const event of original) if (event.output_index !== undefined) event.output_index++
    original.at(-1).response = response
    original.splice(1, 0, ...reasoningEvents)
    wire = original.map((event, sequence_number) => `event: ${event.type}\ndata: ${JSON.stringify({ ...event, sequence_number })}\n\n`).join('')
  }
  const f = await endpoint(t, wire, stream)
  const result = await consume('responses', f.options, stream)
  assert.deepEqual(result.map(p => p.name), ['openai', '@ai-sdk/openai'])
  assert.equal(f.captured.length, 2)
  assert.ok(f.captured.every(detail => detail.response.status === 200 && detail.response.body === wire))
})

test('strict SDK rejection retains the HTTP 200 response evidence', { skip: !consume && 'SDK dependencies unavailable' }, async t => {
  const body = JSON.parse(replyWire('responses')); delete body.output[0].id
  const f = await endpoint(t, JSON.stringify(body), false)
  await assert.rejects(consume('responses', f.options, false))
  assert.equal(f.captured.length, 2)
  assert.equal(f.captured[1].response.status, 200)
})

for (const stream of [false, true]) test(`Responses message phase ${stream ? 'SSE' : 'JSON'} survives SDK collection`, { skip: !consume && 'SDK dependencies unavailable' }, async t => {
  // Synthetic data only; live configuration is not read by this test suite.
  const response = JSON.parse(replyWire('responses'))
  response.output[0].phase = 'final_answer'
  let wire = JSON.stringify(response)
  if (stream) {
    const events = replyWire('responses', 'OK', false, true).split('\n').filter(line => line.startsWith('data: ')).map(line => JSON.parse(line.slice(6)))
    // Phase is absent at item.added, and first appears at item.done.
    for (const event of events) if (event.type === 'response.output_item.done') event.item.phase = 'final_answer'
    events.at(-1).response = response
    wire = events.map((event, sequence_number) => `event: ${event.type}\ndata: ${JSON.stringify({ ...event, sequence_number })}\n\n`).join('')
  }
  const f = await endpoint(t, wire, stream)
  const result = await consume('responses', f.options, stream)
  assert.deepEqual(result.map(p => p.name), ['openai', '@ai-sdk/openai'])
  assert.equal(f.captured.length, 2)
  const { default: OpenAI } = await import('openai')
  const client = new OpenAI({ baseURL: `${f.options.baseUrl}/v1`, apiKey: f.options.apiKey, maxRetries: 0 })
  const params = { model: 'test', input: 'Synthetic phase regression', store: false }
  const final = stream ? await client.responses.stream(params).finalResponse() : await client.responses.create(params)
  assert.equal(final.output[0].phase, 'final_answer')
})

for (const stream of [false, true]) test(`Responses mixed summary and multipart reasoning ${stream ? 'SSE' : 'JSON'} keeps independent SDK content`, { skip: !consume && 'SDK dependencies unavailable' }, async t => {
  const reasoning = {
    id: 'rs_multipart', type: 'reasoning', status: 'completed',
    summary: [{ type: 'summary_text', text: 'Synthetic summary.' }],
    content: [{ type: 'reasoning_text', text: 'First visible part.' }, { type: 'reasoning_text', text: 'Second visible part.' }],
  }
  const response = JSON.parse(replyWire('responses'))
  response.output.unshift(reasoning)
  let wire = JSON.stringify(response)
  if (stream) {
    const events = replyWire('responses', 'OK', false, true).split('\n').filter(line => line.startsWith('data: ')).map(line => JSON.parse(line.slice(6)))
    for (const e of events) if (e.output_index !== undefined) e.output_index++
    const owner = { output_index: 0, item_id: reasoning.id }
    const parts = [{ type: 'response.output_item.added', output_index: 0, item: { ...reasoning, status: 'in_progress', summary: [], content: [] } }]
    const summaryRef = { ...owner, summary_index: 0 }
    const summary = [
      { type: 'response.reasoning_summary_part.added', ...summaryRef, part: { type: 'summary_text', text: '' } },
      { type: 'response.reasoning_summary_text.delta', ...summaryRef, delta: reasoning.summary[0].text },
      { type: 'response.reasoning_summary_text.done', ...summaryRef, text: reasoning.summary[0].text },
      { type: 'response.reasoning_summary_part.done', ...summaryRef, part: reasoning.summary[0] },
    ]
    for (const [content_index, part] of reasoning.content.entries()) {
      const ref = { ...owner, content_index }
      parts.push({ type: 'response.content_part.added', ...ref, part: { type: 'reasoning_text', text: '' } })
      // Interleave summary events with visible content, as the Go regression
      // does. Their indexes are separate namespaces, not one text buffer.
      if (content_index === 0) parts.push(...summary.slice(0, 2))
      parts.push({ type: 'response.reasoning_text.delta', ...ref, delta: part.text })
      parts.push({ type: 'response.reasoning_text.done', ...ref, text: part.text })
      parts.push({ type: 'response.content_part.done', ...ref, part })
    }
    parts.push(...summary.slice(2), { type: 'response.output_item.done', output_index: 0, item: reasoning })
    events.at(-1).response = response
    events.splice(1, 0, ...parts)
    wire = events.map((event, sequence_number) => `event: ${event.type}\ndata: ${JSON.stringify({ ...event, sequence_number })}\n\n`).join('')
  }
  const f = await endpoint(t, wire, stream)
  const result = await consume('responses', f.options, stream)
  assert.deepEqual(result.map(p => p.name), ['openai', '@ai-sdk/openai'])
  assert.equal(f.captured.length, 2)
  const { default: OpenAI } = await import('openai')
  const client = new OpenAI({ baseURL: `${f.options.baseUrl}/v1`, apiKey: f.options.apiKey, maxRetries: 0 })
  const params = { model: 'test', input: 'Synthetic SDK regression', store: false }
  const final = stream ? await client.responses.stream(params).finalResponse() : await client.responses.create(params)
  assert.deepEqual(final.output[0], reasoning)
})

for (const protocol of ['chat', 'responses', 'anthropic', 'gemini']) test(`${protocol} SDK HTTP errors are captured without automatic retries`, { skip: !consume && 'SDK dependencies unavailable' }, async t => {
  const body = JSON.stringify({ error: { message: 'provider unavailable' } })
  const f = await endpoint(t, body, false, 503)
  await assert.rejects(consume(protocol, f.options, false), /provider unavailable/)
  assert.equal(f.captured.length, 1); assert.equal(f.captured[0].response.status, 503); assert.equal(f.captured[0].response.body, body)
})

test('SDK timeout retains partial response and the transport error', { skip: !consume && 'SDK dependencies unavailable' }, async t => {
  const f = await endpoint(t, 'partial-response', false, 200, true)
  await assert.rejects(consume('chat', { ...f.options, timeoutMs: 150 }, false))
  assert.equal(f.captured.length, 1); assert.equal(f.captured[0].response.body, 'partial-response')
  assert.ok(f.captured[0].error)
})

test('SDK response limit retains bounded evidence and fails the case', { skip: !consume && 'SDK dependencies unavailable' }, async t => {
  const f = await endpoint(t, replyWire('chat'), false)
  await assert.rejects(consume('chat', { ...f.options, maxResponseBytes: 128 }, false))
  assert.equal(f.captured[0].response.body.length, 128)
  assert.match(f.captured[0].error, /maxResponseBytes/)
})

test('32 parallel SDK cases use real HTTP, finish both SDK variants and keep evidence attached to each model', { skip: !consume && 'SDK dependencies unavailable', timeout: 10000 }, async t => {
  const tasks = ['chat', 'responses', 'anthropic', 'gemini'].flatMap(protocol => Array.from({ length: 8 }, (_, i) => ({ protocol, stream: i % 2 === 1, model: `${protocol}-${i}` })))
  const pending = []
  let received = 0, peak = 0
  const server = createServer(async (req, res) => {
    const chunks = []; for await (const chunk of req) chunks.push(chunk)
    const body = JSON.parse(Buffer.concat(chunks))
    const protocol = req.url.includes('/responses') ? 'responses' : req.url.includes('/messages') ? 'anthropic' : req.url.includes('/v1beta/') ? 'gemini' : 'chat'
    const stream = body.stream || req.url.includes(':streamGenerateContent')
    pending.push({ res, protocol, stream }); received++; peak = Math.max(peak, pending.length)
    if (pending.length === 32 || received === 56) for (const { res, protocol, stream } of pending.splice(0).reverse()) {
      res.writeHead(200, { 'content-type': stream ? 'text/event-stream' : 'application/json' })
      res.end(replyWire(protocol, 'OK', false, stream))
    }
  })
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve))
  t.after(async () => { server.closeAllConnections(); await new Promise(resolve => server.close(resolve)) })
  await mapConcurrent(tasks, 32, async task => {
    const captured = []
    await consume(task.protocol, { baseUrl: `http://127.0.0.1:${server.address().port}`, apiKey: 'synthetic-sdk-key', model: task.model, onExchange: detail => { captured.push(detail) } }, task.stream)
    assert.equal(captured.length, task.protocol === 'gemini' ? 1 : 2)
    for (const detail of captured) {
      assert.equal(detail.response.status, 200)
      if (task.protocol === 'gemini') assert.ok(detail.request.url.includes(task.model))
      else assert.equal(JSON.parse(detail.request.body).model, task.model)
    }
  })
  assert.equal(received, 56); assert.equal(peak, 32)
})
