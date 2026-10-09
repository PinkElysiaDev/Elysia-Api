// Replay only synthetic output produced by TestGatewayAuditTextMatrix. No live
// configuration, credentials or upstream URLs are read. Response bytes remain
// unchanged, including native frame boundaries and message metadata.
import './tests/network-guard.mjs'
import { consume } from './src/sdk.mjs'
import { readFile } from 'node:fs/promises'
import { createServer } from 'node:http'
import { join, resolve } from 'node:path'
import assert from 'node:assert/strict'

const directory = process.argv[2]
if (!directory) throw new Error('Usage: node scripts/protocol-audit/replay.mjs <ELYSIA_AUDIT_CAPTURE directory>')
const protocols = {
  'openai-chat-completions': 'chat', 'openai-responses': 'responses',
  'anthropic-messages': 'anthropic', 'google-generate-content': 'gemini',
}
const results = []
for (const upstream of Object.keys(protocols)) for (const [ingress, protocol] of Object.entries(protocols)) for (const stream of [false, true]) {
  const result = { upstream, ingress, stream, passed: false }
  let server
  try {
    const record = JSON.parse(await readFile(join(resolve(directory), `${upstream}-${ingress}-${stream}.json`), 'utf8'))
    assert.equal(record.upstream, upstream)
    assert.equal(record.ingress, ingress)
    assert.equal(record.stream, stream)
    assert.equal(typeof record.body, 'string')
    server = createServer((req, res) => {
      req.resume()
      res.writeHead(200, { 'content-type': stream ? 'text/event-stream' : 'application/json' })
      res.end(record.body)
    })
    await new Promise((resolve, reject) => { server.once('error', reject); server.listen(0, '127.0.0.1', resolve) })
    result.sdk = await consume(protocol, {
      baseUrl: `http://127.0.0.1:${server.address().port}`,
      apiKey: 'synthetic-sdk-replay', model: 'm', expectedText: 'hi', timeoutMs: 10000,
      onExchange: detail => {
        assert.equal(detail.response.status, 200)
        assert.equal(detail.response.body, record.body)
      },
    }, stream)
    result.passed = true
  } catch (error) {
    result.error = error.message
  } finally {
    if (server?.listening) {
      server.closeAllConnections()
      await new Promise(resolve => server.close(resolve))
    }
  }
  results.push(result)
}
console.log(JSON.stringify({
  cases: results.length,
  passed: results.filter(r => r.passed).length,
  variants: results.reduce((n, r) => n + (r.sdk?.length || 0), 0),
  results,
}, null, 2))
if (results.some(r => !r.passed)) process.exitCode = 1
