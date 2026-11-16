import test from 'node:test'
import assert from 'node:assert/strict'
import { readAuditProtocols, buildAuditSource, configureAuditSource, configureAuditRoute, buildAuditGatewayEndpoint } from '../src/bindings.mjs'

const fixtures = [
  ['openai-chat-completions', '/v1/chat/completions', '/v1/chat/completions'],
  ['openai-responses', '/v1/responses', '/v1/responses'],
  ['anthropic-messages', '/v1/messages', '/v1/messages'],
  ['google-generate-content', '/v1beta/models/{model}:generateContent', '/v1beta/models/{model}:streamGenerateContent'],
]
const compiler = 'test-compiler'

function fixture() {
  const documents = fixtures.map(([id, path, streamPath]) => ({ schemaVersion: 4,
    spec: { id, operations: {
      generate: { kind: 'generate', method: 'POST', transport: 'http_json', path },
      stream: { kind: 'generate', method: 'POST', transport: 'sse', path: streamPath, ...(id === 'google-generate-content' ? { query: { alt: 'sse' } } : {}) },
    } },
    mapping: { dslVersion: 2, coreVersion: 2, capabilities: { text: true, usage: true, tools: true }, directions: {
      decode_request: {}, encode_request: {}, decode_response: {}, encode_response: {}, decode_event: {}, encode_event: {},
    } },
  }))
  const revisions = Object.fromEntries(documents.map((document, index) => [document.spec.id, {
    revision: { hash: `revision-${index}`, definition: document },
    report: { passed: true, kind: 'offline', compilerVersion: compiler, definitionHash: `revision-${index}`, samplesHash: `samples-${index}` },
  }]))
  const runtime = { runtimeReady: true, runtimeFailures: {}, loaded: {}, active: [] }
  const enabled = documents.map(document => {
    const revision = revisions[document.spec.id].revision.hash
    runtime.loaded[document.spec.id] = revision
    runtime.active.push({ protocolId: document.spec.id, revisionHash: revision })
    return { id: document.spec.id, revision, directions: Object.keys(document.mapping.directions), capabilities: document.mapping.capabilities }
  })
  const calls = [], bindings = []
  const admin = async (path, body, method = body === undefined ? 'GET' : 'POST') => {
    calls.push({ path, body: structuredClone(body), method })
    if (path === 'protocols') return structuredClone(runtime)
    if (path === 'protocols/schema') return { packageVersion: 4, compilerVersion: compiler }
    if (path === 'protocols/enabled') return { items: structuredClone(enabled) }
    if (path.startsWith('protocols/') && path.includes('/revisions/')) return structuredClone(revisions[path.split('/')[1]])
    if (path === 'protocols/bindings' && method === 'PUT') {
      const evidence = revisions[body.binding.protocolId]
      bindings.push({ ...structuredClone(body), combinations: [{ kind: 'offline', compilerVersion: compiler, targetHash: evidence.revision.hash, targetSamplesHash: evidence.report.samplesHash }] })
      return body
    }
    if (path === 'protocols/bindings') return structuredClone(bindings)
    assert.ok(['model-sources', 'model-groups'].includes(path), `Unexpected management call: ${method} ${path}`)
    return body
  }
  return { admin, runtime, revisions, calls, bindings }
}

test('source creation pins real Spec/Mapping revisions through the separate binding API', async () => {
  const state = fixture()
  const contracts = await readAuditProtocols(state.admin)
  const source = buildAuditSource({ baseUrl: 'https://provider.invalid/prefix/v1', model: 'm', vision: true }, 'source', 'synthetic', contracts.chat)
  assert.equal(source.baseUrl, 'https://provider.invalid/prefix')
  assert.equal(Object.hasOwn(source, 'platform'), false)
  assert.equal(Object.hasOwn(source, 'protocolBinding'), false)
  const start = state.calls.length
  const saved = await configureAuditRoute(state.admin, source, contracts.chat)
  assert.equal(saved.platform, 'custom:openai-chat-completions')
  assert.deepEqual(state.calls.slice(start).map(({ path, method }) => [method, path]), [
    ['POST', 'model-sources'], ['PUT', 'protocols/bindings'], ['GET', 'protocols/bindings'], ['GET', 'protocols'], ['POST', 'model-groups'],
  ])
  assert.deepEqual(state.calls.find(call => call.method === 'PUT').body, { kind: 'source', sourceId: 'source', binding: {
    protocolId: contracts.chat.id, revisionHash: contracts.chat.revision, capabilities: contracts.chat.capabilities, transports: ['http_json', 'sse'],
  } })
})

test('source URL prefixes and ingress endpoints follow Spec paths and query declarations', async () => {
  const { admin } = fixture()
  const contracts = await readAuditProtocols(admin)
  assert.equal(buildAuditSource({ baseUrl: 'https://provider.invalid/prefix', model: 'm' }, 's', '', contracts.chat).baseUrl, 'https://provider.invalid/prefix')
  assert.equal(buildAuditSource({ baseUrl: 'https://provider.invalid/v1beta/', model: 'm' }, 's', '', contracts.gemini).baseUrl, 'https://provider.invalid')
  assert.equal(buildAuditGatewayEndpoint('http://127.0.0.1:3210', contracts.gemini, 'model name', true), 'http://127.0.0.1:3210/gateway/google-generate-content/v1beta/models/model%20name:streamGenerateContent?alt=sse')
  const custom = structuredClone(contracts.chat)
  custom.spec.operations.generate.path = '/custom/generate/{model}'
  assert.equal(buildAuditGatewayEndpoint('http://127.0.0.1:3210', custom, 'm', false), 'http://127.0.0.1:3210/gateway/openai-chat-completions/custom/generate/m')
})

test('runtime drift and stale activation evidence reject setup before writes', async () => {
  for (const mutate of [
    state => { state.runtime.loaded['openai-chat-completions'] = 'changed' },
    state => { state.runtime.runtimeFailures['openai-chat-completions'] = 'failed' },
    state => { state.revisions['openai-chat-completions'].report.compilerVersion = 'old' },
    state => { state.revisions['openai-chat-completions'].revision.definition.mapping.coreVersion = 1 },
  ]) {
    const state = fixture()
    mutate(state)
    await assert.rejects(readAuditProtocols(state.admin))
    assert.ok(state.calls.every(call => call.method === 'GET'))
  }
})

test('source setup rejects missing bindings, stale combinations and activation changed during save', async () => {
  for (const mutate of [
    state => { state.bindings.length = 0 },
    state => { state.runtime.active[0].revisionHash = 'changed' },
    state => { state.bindings[0].combinations[0].targetSamplesHash = 'old' },
  ]) {
    const state = fixture()
    const contracts = await readAuditProtocols(state.admin)
    const admin = async (path, body, method) => {
      const result = await state.admin(path, body, method)
      if (path === 'protocols/bindings' && method === 'PUT') mutate(state)
      return result
    }
    await assert.rejects(configureAuditSource(admin, buildAuditSource({ baseUrl: 'https://provider.invalid', model: 'm' }, 's', '', contracts.chat), contracts.chat))
    assert.ok(state.calls.every(call => call.path !== 'model-groups'))
  }
})
