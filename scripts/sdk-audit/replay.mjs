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
import { assertObservation, observeNative, observeVercel } from './observe.mjs'
import { describeCapture, summarizeCoverage } from './fixtures.mjs'

const maxOutputTokens = 32
const model = 'm'
const prompt = [{ role: 'user', content: [{ type: 'text', text: 'hi' }] }]
const directory = process.argv[2]
assert(directory, 'usage: node scripts/sdk-audit/replay.mjs <gateway-capture-directory>')
const captures = []
for (const file of (await readdir(directory)).filter(name => name.endsWith('.json')).sort()) {
  const fixture = JSON.parse(await readFile(join(directory, file), 'utf8'))
  captures.push({ ...describeCapture(file, fixture), fixture })
}
assert(captures.length > 0, 'capture directory is empty')
const coverage = summarizeCoverage(captures)
let fixture
// Replay exact captured gateway bytes, without contacting a provider or repairing output.
const server = createServer(async (request, response) => {
  for await (const _chunk of request) { /* consume client request */ }
  response.writeHead(200, { 'Content-Type': fixture.stream ? 'text/event-stream' : 'application/json' })
  response.end(fixture.body)
})
await new Promise(resolve => server.listen(0, '127.0.0.1', resolve))
const base = `http://127.0.0.1:${server.address().port}`
const outcomes = []

async function checkClient(capture, label, execute) {
  try {
    assertObservation(await execute(), capture.expected, `${capture.file}: ${label} content changed`)
    outcomes.push({ file: capture.file, client: label, passed: true })
  } catch (error) {
    outcomes.push({ file: capture.file, client: label, passed: false, error: error.message })
  }
}

async function collectStream(stream) {
  const events = []
  for await (const event of stream) events.push(event)
  return events
}

async function consumeVercel(client) {
  const reply = fixture.stream
    ? await collectStream((await client.doStream({ prompt, maxOutputTokens })).stream)
    : await client.doGenerate({ prompt, maxOutputTokens })
  return observeVercel(reply, fixture.stream, client.specificationVersion)
}

async function consumeNative() {
  let reply
  switch (fixture.ingress) {
    case 'openai-chat-completions': case 'openai-responses': {
      const client = new OpenAI({ apiKey: 'mock-only', baseURL: base + '/v1', maxRetries: 0 })
      reply = fixture.ingress === 'openai-chat-completions'
        ? await client.chat.completions.create({ model, messages: [{ role: 'user', content: 'hi' }], stream: fixture.stream })
        : await client.responses.create({ model, input: 'hi', stream: fixture.stream })
      break
    }
    case 'anthropic-messages': {
      const client = new Anthropic({ apiKey: 'mock-only', baseURL: base, maxRetries: 0 })
      reply = await client.messages.create({ model, max_tokens: maxOutputTokens, messages: [{ role: 'user', content: 'hi' }], stream: fixture.stream })
      break
    }
    case 'google-generate-content': {
      const client = new GoogleGenAI({ apiKey: 'mock-only', httpOptions: { baseUrl: base, apiVersion: 'v1beta' } })
      reply = await client.models[fixture.stream ? 'generateContentStream' : 'generateContent']({ model, contents: 'hi' })
      break
    }
  }
  return observeNative(fixture.ingress, fixture.stream ? await collectStream(reply) : reply, fixture.stream)
}

try {
  for (const capture of captures) {
    fixture = capture.fixture
    await checkClient(capture, 'native-sdk', consumeNative)
    if (fixture.ingress === 'openai-chat-completions' || fixture.ingress === 'openai-responses') {
      const sdk = createOpenAI({ apiKey: 'mock-only', baseURL: base + '/v1' })
      const client = fixture.ingress === 'openai-chat-completions' ? sdk.chat(model) : sdk.responses(model)
      await checkClient(capture, 'vercel-openai', () => consumeVercel(client))
    } else if (fixture.ingress === 'anthropic-messages') {
      await checkClient(capture, 'vercel-anthropic-v4', () => consumeVercel(createAnthropic({ apiKey: 'mock-only', baseURL: base + '/v1' })(model)))
      await checkClient(capture, 'vercel-anthropic-v2', () => consumeVercel(createAnthropicV2({ apiKey: 'mock-only', baseURL: base + '/v1' })(model)))
    }
  }
  const failed = outcomes.filter(outcome => !outcome.passed).length
  console.log(JSON.stringify({ passed: outcomes.length - failed, failed, coverage, outcomes }, null, 2))
  if (failed) process.exitCode = 1
} finally {
  server.closeAllConnections()
  await new Promise(resolve => server.close(resolve))
}
