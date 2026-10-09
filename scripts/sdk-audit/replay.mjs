import assert from 'node:assert/strict'
import { readdir, readFile } from 'node:fs/promises'
import { createServer } from 'node:http'
import { join } from 'node:path'
import OpenAI from 'openai'
import Anthropic from '@anthropic-ai/sdk'
import { GoogleGenAI } from '@google/genai'
import { createOpenAI } from '@ai-sdk/openai'
import { createAnthropic } from '@ai-sdk/anthropic'
import { createAnthropic as createAnthropicV2 } from 'anthropic-v2'

// Input is emitted by TestGatewayAuditTextMatrix with ELYSIA_AUDIT_CAPTURE.
// These are actual gateway bytes; this script never repairs captured fixtures.
const directory = process.argv[2]
assert(directory, 'usage: node scripts/sdk-audit/replay.mjs <gateway-capture-directory>')
let fixture
const server = createServer(async (req, res) => {
  for await (const _chunk of req) { /* consume client request */ }
  res.writeHead(200, { 'Content-Type': fixture.stream ? 'text/event-stream' : 'application/json' })
  res.end(fixture.body)
})
await new Promise(resolve => server.listen(0, '127.0.0.1', resolve))
const base = `http://127.0.0.1:${server.address().port}`
const prompt = [{ role: 'user', content: [{ type: 'text', text: 'hi' }] }]
const outcomes = []
async function consume(label, client) {
  let parts = []
  if (fixture.stream) {
    const result = await client.doStream({ prompt, maxOutputTokens: 32 })
    for await (const part of result.stream) {
      assert.notEqual(part.type, 'error', `${fixture.upstream} -> ${fixture.ingress} ${label}: ${JSON.stringify(part.error?.value ?? part.error)}`)
      parts.push(part)
    }
    assert(parts.some(p => p.type === 'finish'), `${label}: missing finish`)
  } else {
    parts = await client.doGenerate({ prompt, maxOutputTokens: 32 })
  }
  assert(JSON.stringify(parts).includes(fixture.tool ? 'lookup' : 'hi'), `${label}: missing generated content`)
  outcomes.push({ upstream: fixture.upstream, ingress: fixture.ingress, stream: fixture.stream, client: label })
}
try {
  for (const file of (await readdir(directory)).filter(f => f.endsWith('.json')).sort()) {
    fixture = JSON.parse(await readFile(join(directory, file), 'utf8'))
    let result
    if (fixture.ingress === 'openai-chat-completions' || fixture.ingress === 'openai-responses') {
      const client = new OpenAI({ apiKey: 'mock-only', baseURL: base + '/v1', maxRetries: 0 })
      const chat = fixture.ingress === 'openai-chat-completions'
      result = chat
        ? await client.chat.completions.create({ model: 'm', messages: [{ role: 'user', content: 'hi' }], stream: fixture.stream })
        : await client.responses.create({ model: 'm', input: 'hi', stream: fixture.stream })
      const sdk = createOpenAI({ apiKey: 'mock-only', baseURL: base + '/v1' })
      await consume('vercel-openai', chat ? sdk.chat('m') : sdk.responses('m'))
    } else if (fixture.ingress === 'anthropic-messages') {
      const client = new Anthropic({ apiKey: 'mock-only', baseURL: base, maxRetries: 0 })
      result = await client.messages.create({ model: 'm', max_tokens: 32, messages: [{ role: 'user', content: 'hi' }], stream: fixture.stream })
      await consume('vercel-anthropic-v4', createAnthropic({ apiKey: 'mock-only', baseURL: base + '/v1' })('m'))
      await consume('vercel-anthropic-v2', createAnthropicV2({ apiKey: 'mock-only', baseURL: base + '/v1' })('m'))
    } else {
      const client = new GoogleGenAI({ apiKey: 'mock-only', httpOptions: { baseUrl: base, apiVersion: 'v1beta' } })
      result = await client.models[fixture.stream ? 'generateContentStream' : 'generateContent']({ model: 'm', contents: 'hi' })
    }
    let native = result
    if (fixture.stream) {
      native = []
      for await (const event of result) {
        assert(!event.error && event.type !== 'error', JSON.stringify(event))
        native.push(event)
      }
      assert(native.length > 0, 'native SDK returned no events')
    }
    assert(JSON.stringify(native).includes(fixture.tool ? 'lookup' : 'hi'), `${file}: native SDK content missing`)
    outcomes.push({ upstream: fixture.upstream, ingress: fixture.ingress, stream: fixture.stream, client: 'native-sdk' })
  }
  console.log(JSON.stringify({ passed: outcomes.length, outcomes }, null, 2))
} finally {
  server.closeAllConnections()
  await new Promise(resolve => server.close(resolve))
}
