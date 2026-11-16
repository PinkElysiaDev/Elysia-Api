import assert from 'node:assert/strict'

const protocolIds = {
  chat: 'openai-chat-completions', responses: 'openai-responses',
  anthropic: 'anthropic-messages', gemini: 'google-generate-content',
}
const packageVersion = 4, dslVersion = 2, coreVersion = 2
const directions = ['decode_request', 'encode_request', 'decode_response', 'encode_response', 'decode_event', 'encode_event']

/** Reads the active executable packages and their current offline evidence. */
export async function readAuditProtocols(admin) {
  const runtime = await admin('protocols')
  const schema = await admin('protocols/schema')
  const enabled = (await admin('protocols/enabled')).items
  assert.ok(runtime.runtimeReady && !runtime.runtimeError && Object.keys(runtime.runtimeFailures).length === 0, 'Protocol runtime is not ready')
  assert.equal(schema.packageVersion, packageVersion, 'Audit requires Spec/Mapping packages')
  const contracts = {}
  for (const [name, id] of Object.entries(protocolIds)) {
    const active = runtime.active.find(entry => entry.protocolId === id)
    const executable = enabled.find(entry => entry.id === id)
    assert.ok(active && executable, `Missing active protocol: ${id}`)
    assert.equal(runtime.loaded[id], active.revisionHash, `Loaded revision differs: ${id}`)
    assert.equal(executable.revision, active.revisionHash, `Executable revision differs: ${id}`)
    assert.ok(directions.every(direction => executable.directions.includes(direction)), `Incomplete core directions: ${id}`)
    const { revision, report } = await admin(`protocols/${id}/revisions/${active.revisionHash}`)
    const document = typeof revision.definition === 'string' ? JSON.parse(revision.definition) : revision.definition
    assert.equal(revision.hash, active.revisionHash, `Stored revision differs: ${id}`)
    assert.equal(document.schemaVersion, packageVersion)
    assert.equal(document.spec.id, id)
    assert.equal(document.mapping.dslVersion, dslVersion)
    assert.equal(document.mapping.coreVersion, coreVersion)
    assert.ok(report?.passed && report.kind === 'offline' && report.compilerVersion === schema.compilerVersion && report.definitionHash === revision.hash && report.samplesHash, `Stale activation evidence: ${id}`)
    contracts[name] = { id, revision: revision.hash, compiler: schema.compilerVersion, spec: document.spec, capabilities: executable.capabilities, samplesHash: report.samplesHash }
  }
  return contracts
}

function selectGenerationOperations(contract) {
  return Object.values(contract.spec.operations).filter(operation => operation.kind === 'generate')
}

function normalizeSourceBaseUrl(baseUrl, contract) {
  const url = new URL(baseUrl)
  const segments = url.pathname.split('/').filter(Boolean)
  const paths = selectGenerationOperations(contract).map(operation => operation.path.split('/').filter(Boolean))
  // Configured SDK roots commonly include the version segment already owned by Spec.
  for (let count = segments.length; count > 0; count--) {
    const suffix = segments.slice(-count).join('/')
    if (paths.every(path => path.length > count && path.slice(0, count).join('/') === suffix)) {
      segments.splice(-count)
      break
    }
  }
  url.pathname = segments.length ? `/${segments.join('/')}` : '/'
  return url.toString().replace(/\/$/, '')
}

/** Builds source metadata; protocol revisions are saved through the binding API. */
export function buildAuditSource(target, id, apiKey, contract) {
  return { id, name: id, baseUrl: normalizeSourceBaseUrl(target.baseUrl, contract), apiKey, enabled: true, autoFetchModels: false,
    manualModels: [{ id: target.model, name: target.model, type: 'llm', enabled: true, available: true, toolsCapable: true, visionCapable: target.vision === true }] }
}

/** Creates a source, pins its active revision and checks the persisted binding. */
export async function configureAuditSource(admin, source, contract) {
  const operations = selectGenerationOperations(contract)
  assert.ok(operations.length && operations.every(operation => operation.path.startsWith('/')), `Missing generation operations: ${contract.id}`)
  await admin('model-sources', source)
  const binding = { protocolId: contract.id, revisionHash: contract.revision, capabilities: contract.capabilities, transports: [...new Set(operations.map(operation => operation.transport))].sort() }
  await admin('protocols/bindings', { kind: 'source', sourceId: source.id, binding }, 'PUT')
  const stored = (await admin('protocols/bindings')).filter(entry => entry.kind === 'source' && entry.sourceId === source.id)
  assert.equal(stored.length, 1, `Missing unique source binding: ${source.id}`)
  assert.ok(!stored[0].unbound, `Source remains unbound: ${source.id}`)
  assert.deepEqual(stored[0].binding, binding, `Persisted binding differs: ${source.id}`)
  assert.ok(stored[0].combinations?.length && stored[0].combinations.every(report => report.kind === 'offline' && report.targetHash === contract.revision && report.targetSamplesHash === contract.samplesHash && report.compilerVersion === contract.compiler), `Stale combination evidence: ${source.id}`)
  const runtime = await admin('protocols')
  assert.equal(runtime.loaded[contract.id], contract.revision, `Loaded revision changed during setup: ${contract.id}`)
  assert.equal(runtime.active.find(entry => entry.protocolId === contract.id)?.revisionHash, contract.revision, `Active revision changed during setup: ${contract.id}`)
  // Metadata updates must retain the API's displayed protocol or they explicitly unbind it.
  return { ...source, platform: `custom:${contract.id}` }
}

/** Creates the independently bound source and its one-model audit group. */
export async function configureAuditRoute(admin, source, contract) {
  const saved = await configureAuditSource(admin, source, contract)
  await admin('model-groups', { id: source.id, name: source.id, enabled: true, models: [`${source.id}:${source.manualModels[0].id}`], strategy: 'sequential', maxRetries: 0, type: 'llm', toolsCapable: true, visionCapable: source.manualModels[0].visionCapable })
  return saved
}

/** Selects the exact ingress endpoint and static query declared by the active Spec. */
export function buildAuditGatewayEndpoint(baseUrl, contract, model, isStream) {
  const transport = isStream ? 'sse' : 'http_json'
  const operations = selectGenerationOperations(contract).filter(operation => operation.transport === transport)
  assert.equal(operations.length, 1, `Ambiguous generation operation: ${contract.id}/${transport}`)
  const operation = operations[0]
  assert.equal(operation.method, 'POST')
  const path = operation.path.replaceAll('{model}', encodeURIComponent(model))
  assert.ok(!/[{}]/.test(path), `Unresolved operation parameters: ${path}`)
  const url = new URL(`${baseUrl.replace(/\/$/, '')}/gateway/${contract.id}${path}`)
  for (const [name, value] of Object.entries(operation.query || {})) url.searchParams.set(name, value)
  return url.toString()
}
