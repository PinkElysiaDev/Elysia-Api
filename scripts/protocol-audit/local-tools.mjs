// Actual isolated gateway + loopback provider fixtures + SDK-generated history.
// This command never loads config.local.json or contacts a real model channel.
import './tests/network-guard.mjs'
import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { createServer } from 'node:http'
import { dirname, join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { spawnSync } from 'node:child_process'
import { localInstance } from './src/local.mjs'
import { persistedCall } from './src/evidence.mjs'
import { consumeToolRoundTrip } from './src/sdk-tools.mjs'

const directory = process.argv[2]
if (!directory) throw new Error('Usage: node scripts/protocol-audit/local-tools.mjs <ELYSIA_AUDIT_CAPTURE directory>')
const root = resolve(dirname(fileURLToPath(import.meta.url)), '../..')
const codecs = {
  'openai-chat-completions': 'chat', 'openai-responses': 'responses',
  'anthropic-messages': 'anthropic', 'google-generate-content': 'gemini',
}
const fixtures = new Map()
for (const id of Object.keys(codecs)) for (const stream of [false, true]) for (const second of [false, true]) {
  const record = JSON.parse(await readFile(join(resolve(directory), `parallel-tool-${id}-${id}-${stream}-${second}.json`), 'utf8'))
  assert.equal(record.upstream, id); assert.equal(record.ingress, id)
  assert.equal(record.stream, stream); assert.equal(record.tool, !second)
  fixtures.set(`${id}/${stream}/${second}`, record.body)
}

let active, gateway
const results = []
const provider = createServer(async (request, response) => {
  try {
    assert.ok(active, 'Provider called outside a test')
    const chunks = []
    let bytes = 0
    for await (const chunk of request) { bytes += chunk.length; assert.ok(bytes < 1024 * 1024); chunks.push(chunk) }
    const body = JSON.parse(Buffer.concat(chunks).toString('utf8'))
    const second = active.upstreamRequests.length % 2 === 1
    active.upstreamRequests.push({ second, body })
    if (second) checkProviderHistory(codecs[active.upstream], body)
    response.writeHead(200, { 'content-type': active.stream ? 'text/event-stream' : 'application/json' })
    response.end(fixtures.get(`${active.upstream}/${active.stream}/${second}`))
  } catch (error) {
    if (active) active.providerErrors.push(error.message)
    response.writeHead(400, { 'content-type': 'application/json' })
    response.end(JSON.stringify({ error: { message: error.message, type: 'invalid_request_error' } }))
  }
})

try {
  await new Promise((resolve, reject) => { provider.once('error', reject); provider.listen(0, '127.0.0.1', resolve) })
  gateway = await localInstance(root, async (_name, args, cwd) => {
    const result = spawnSync(args[0], args.slice(1), { cwd, encoding: 'utf8', windowsHide: true })
    assert.equal(result.status, 0, `Local build failed: ${result.stderr}`)
  }, undefined, 60000)
  const admin = async (path, body) => {
    const response = await fetch(`${gateway.baseUrl}/api/admin/${path}`, { method: 'POST', headers: { authorization: `Bearer ${gateway.panel}`, 'content-type': 'application/json' }, body: JSON.stringify(body) })
    const reply = await response.json()
    assert.ok(response.status === 200 && reply.ok, `Setup failed: ${path}: ${JSON.stringify(reply)}`)
    return reply.data
  }
  await admin('api-tokens', { name: 'local-sdk-tools', token: gateway.token, enabled: true, allowedGroups: [], scopes: [] })
  for (const [upstream, protocol] of Object.entries(codecs)) {
    const source = `sdk-${protocol}`
    await admin('model-sources', { id: source, name: source, baseUrl: `http://127.0.0.1:${provider.address().port}`, apiKey: 'synthetic-provider-key', platform: protocol === 'chat' ? 'chat_completions' : protocol, enabled: true, autoFetchModels: false, manualModels: [{ id: 'm', name: 'm', type: 'llm', enabled: true, available: true, toolsCapable: true }] })
    await admin('model-groups', { id: source, name: source, enabled: true, models: [`${source}:m`], strategy: 'sequential', maxRetries: 0, type: 'llm', toolsCapable: true })
    for (const [ingress, client] of Object.entries(codecs)) for (const stream of [false, true]) {
      const result = { upstream, ingress, stream, passed: false, evidence: [] }
      active = { upstream, stream, upstreamRequests: [], providerErrors: [] }
      try {
        result.sdk = await consumeToolRoundTrip(client, { baseUrl: gateway.baseUrl, apiKey: gateway.token, model: source, timeoutMs: 15000, onExchange: async detail => {
          const trace = await persistedCall(gateway.baseUrl, gateway.panel, detail.response.headers['x-elysia-request-id'])
          result.evidence.push({ sdk: detail.sdk, round: detail.round, request: JSON.parse(detail.request.body), status: detail.response.status,
            persisted: { available: trace.available, status: trace.record?.statusCode, error: trace.record?.error, ingressRevision: trace.record?.ingressRevision, upstreamRevision: trace.record?.upstreamRevision, policyHash: trace.record?.conversionPolicyHash, diagnostics: trace.record?.conversionIssues } })
          assert.equal(detail.response.status, 200, detail.response.body)
          assert.ok(trace.available && !trace.record.error, 'Tool round was not persisted as a successful call')
        } }, stream)
        assert.deepEqual(active.providerErrors, [])
        assert.equal(active.upstreamRequests.length, result.sdk.length * 2, 'Unexpected retry or missing provider call')
        result.passed = true
      } catch (error) { result.error = error.message; result.sdk = error.sdk || result.sdk }
      result.upstreamRequests = active.upstreamRequests
      result.providerErrors = active.providerErrors
      results.push(result)
      active = undefined
    }
  }
} finally {
  await gateway?.close()
  provider.closeAllConnections()
  if (provider.listening) await new Promise(resolve => provider.close(resolve))
}
console.log(JSON.stringify({ cases: results.length, passed: results.filter(r => r.passed).length,
  sdkVariants: results.reduce((n, r) => n + (r.sdk?.length || 0), 0),
  upstreamCalls: results.reduce((n, r) => n + r.upstreamRequests.length, 0), results }, null, 2))
if (results.some(r => !r.passed)) process.exitCode = 1

// Check the actual converted request received by the provider, independently
// of SDK collection and without matching calls by name or array position.
function checkProviderHistory(protocol, body) {
  let calls = [], results = []
  if (protocol === 'chat') {
    calls = body.messages.flatMap(m => m.tool_calls || []).map(c => ({ id: c.id, input: JSON.parse(c.function.arguments) }))
    results = body.messages.filter(m => m.role === 'tool').map(m => ({ id: m.tool_call_id, value: JSON.parse(m.content) }))
  } else if (protocol === 'responses') {
    calls = body.input.filter(p => p.type === 'function_call').map(p => ({ id: p.call_id, input: JSON.parse(p.arguments) }))
    results = body.input.filter(p => p.type === 'function_call_output').map(p => ({ id: p.call_id, value: JSON.parse(p.output) }))
  } else if (protocol === 'anthropic') {
    const parts = body.messages.flatMap(m => Array.isArray(m.content) ? m.content : [])
    calls = parts.filter(p => p.type === 'tool_use').map(p => ({ id: p.id, input: p.input }))
    results = parts.filter(p => p.type === 'tool_result').map(p => ({ id: p.tool_use_id, value: JSON.parse(typeof p.content === 'string' ? p.content : p.content.map(t => t.text).join('')) }))
  } else {
    const parts = body.contents.flatMap(m => m.parts)
    calls = parts.filter(p => p.functionCall).map(p => ({ id: p.functionCall.id, input: p.functionCall.args }))
    results = parts.filter(p => p.functionResponse).map(p => ({ id: p.functionResponse.id, value: p.functionResponse.response }))
  }
  assert.equal(calls.length, 2, 'Second round did not keep both assistant calls')
  assert.equal(results.length, 2, 'Second round did not include both tool results')
  for (const [index, id] of ['c1', 'c2'].entries()) {
    assert.deepEqual(calls.find(c => c.id === id)?.input, { n: index + 1 }, `Second round changed arguments for ${id}`)
    let value = results.find(r => r.id === id)?.value
    // The named Gemini tool_result_object rule wraps JSON text without parsing.
    if (protocol === 'gemini' && typeof value?.result === 'string') value = JSON.parse(value.result)
    assert.deepEqual(value, { for: id, answer: `result-${id}` }, `Second round misassociated result ${id}`)
  }
}
