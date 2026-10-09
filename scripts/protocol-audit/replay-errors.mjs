// Only captures from the gateway's synthetic negative-stream tests are read.
// No audit configuration or real API credentials are used.
import './tests/network-guard.mjs'
import { consumeResponsesFailure, consumeChatFailure } from './src/sdk-errors.mjs'
import { readFile } from 'node:fs/promises'
import { createServer } from 'node:http'
import { join, resolve } from 'node:path'
import assert from 'node:assert/strict'

const directory = process.argv[2]
if (!directory) throw new Error('Usage: node scripts/protocol-audit/replay-errors.mjs <ELYSIA_AUDIT_CAPTURE directory> [responses|chat]')
const protocol = process.argv[3] || 'responses'
const suite = {
  responses: { prefix: 'responses-native-error-', names: ['truncated', 'invalid-terminal', 'provider-nested', 'provider-flat'], consume: consumeResponsesFailure },
  chat: { prefix: 'chat-contract-error-', names: ['missing-role', 'changed-id', 'changed-model', 'changed-created'], consume: consumeChatFailure },
}[protocol]
if (!suite) throw new Error('Error replay supports responses or chat')
const results = []
for (const name of suite.names) {
  const result = { name, passed: false }
  let server
  try {
    const record = JSON.parse(await readFile(join(resolve(directory), `${suite.prefix}${name}.json`), 'utf8'))
    assert.equal(record.status, 200)
    assert.ok(record.trailer['X-Elysia-Stream-Error']?.length)
    server = createServer((req, res) => {
      req.resume()
      res.writeHead(200, { 'content-type': 'text/event-stream', trailer: 'X-Elysia-Stream-Error' })
      res.write(record.body)
      res.addTrailers({ 'X-Elysia-Stream-Error': record.trailer['X-Elysia-Stream-Error'][0] })
      res.end()
    })
    await new Promise((resolve, reject) => { server.once('error', reject); server.listen(0, '127.0.0.1', resolve) })
    result.sdk = await suite.consume({ baseUrl: `http://127.0.0.1:${server.address().port}`, cause: record.cause })
    result.passed = true
  } catch (error) { result.error = error.message }
  finally {
    if (server?.listening) { server.closeAllConnections(); await new Promise(resolve => server.close(resolve)) }
  }
  results.push(result)
}
console.log(JSON.stringify({ protocol, cases: results.length, passed: results.filter(r => r.passed).length, results }, null, 2))
if (results.some(r => !r.passed)) process.exitCode = 1
