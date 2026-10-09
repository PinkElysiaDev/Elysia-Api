import test from 'node:test'
import assert from 'node:assert/strict'
import { createServer, request } from 'node:http'
import { captureProxy } from '../src/plugin-proxy.mjs'

test('plugin capture forwards original bytes and trailers and drains evidence on shutdown', async t => {
  const input = '{ "arguments": "{ \\\"n\\\": 9007199254740993 }", "signature":"synthetic" }'
  const output = 'data: { "n":9007199254740993, "signature":"synthetic" }\n\n'
  let observed, captured
  const upstream = createServer(async (req, res) => {
    const chunks = []; for await (const chunk of req) chunks.push(chunk)
    observed = { body: Buffer.concat(chunks).toString(), path: req.url }
    res.writeHead(200, { 'content-type': 'text/event-stream', trailer: 'X-Elysia-Stream-Error', 'x-elysia-request-id': 'request-1' })
    res.write(output); res.addTrailers({ 'X-Elysia-Stream-Error': 'synthetic-error' }); res.end()
  })
  await new Promise(resolve => upstream.listen(0, '127.0.0.1', resolve))
  t.after(() => new Promise(resolve => upstream.close(resolve)))
  const proxy = await captureProxy(`http://127.0.0.1:${upstream.address().port}`, async entry => {
    await new Promise(resolve => setTimeout(resolve, 20)); captured = entry
  }, error => { throw error })
  t.after(() => proxy.close())
  const result = await new Promise((resolve, reject) => {
    const req = request(`${proxy.baseUrl}/v1/messages?x=1`, { method: 'POST' }, res => {
      const chunks = []; res.on('data', b => chunks.push(b))
      res.on('end', () => resolve({ body: Buffer.concat(chunks).toString(), trailers: res.trailers }))
    }); req.on('error', reject); req.end(input)
  })
  await proxy.close()
  assert.equal(observed.body, input); assert.equal(observed.path, '/v1/messages?x=1')
  assert.equal(result.body, output); assert.equal(result.trailers['x-elysia-stream-error'], 'synthetic-error')
  assert.equal(captured.requestId, 'request-1'); assert.equal(captured.complete, true)
})

test('plugin capture rejects absolute destinations before forwarding credentials', async t => {
  let forwarded = 0
  const upstream = createServer((_req, res) => { forwarded++; res.end('unexpected') })
  await new Promise(resolve => upstream.listen(0, '127.0.0.1', resolve))
  t.after(() => new Promise(resolve => upstream.close(resolve)))
  const proxy = await captureProxy(`http://127.0.0.1:${upstream.address().port}`, () => {}, () => {})
  t.after(() => proxy.close())
  for (const path of ['http://example.invalid/messages', '//example.invalid/messages']) {
    const status = await new Promise((resolve, reject) => {
      const url = new URL(proxy.baseUrl)
      const req = request({ hostname: url.hostname, port: url.port, path, headers: { authorization: 'synthetic' } }, res => { res.resume(); res.on('end', () => resolve(res.statusCode)) })
      req.on('error', reject); req.end()
    })
    assert.equal(status, 400)
  }
  assert.equal(forwarded, 0)
})
