import './network-guard.mjs'
import test from 'node:test'
import assert from 'node:assert/strict'
import { failureAnalysis, failureCategory, failureCounts, compatibilityObservations, finishCompatibilityCoverage, compatibilityCounts } from '../src/evidence.mjs'
import { checkPersistence } from '../src/persistence.mjs'

test('structured upstream evidence precedes SDK consumption and preserves mixed defects', () => {
  const conversionIssues = [{ code: 'conversion_rejected', severity: 'error', direction: 'encode_response' }]
  const transportFailure = { kind: 'upstream_idle_timeout', phase: 'awaiting_response' }
  for (const [record, category] of [
    [{ transportFailure }, 'upstream_idle_timeout'],
    [{ transportFailure, conversionIssues }, 'upstream_idle_timeout'],
    [{ upstreamHttpStatus: 524, transportFailure, conversionIssues }, 'upstream_http_timeout'],
    [{ upstreamHttpStatus: 400, providerFailures: [{ type: 'new_api_error', code: 'input_tokens_out_of_range' }], transportFailure, conversionIssues }, 'upstream_http_failure'],
    [{ upstreamHttpStatus: 200, providerFailures: [{ type: 'upstream_error', message: 'Synthetic failure' }], transportFailure, conversionIssues }, 'upstream_generation_failure'],
    [{ upstreamHttpStatus: 200, conversionIssues: [{ code: 'upstream_contract_violation', severity: 'error' }] }, 'upstream_contract_failure'],
  ]) {
    const input = { sdk: [{ name: 'openai', status: 'failed' }], httpStatuses: [200], record }
    assert.equal(failureCategory(input), category)
    const failure = failureAnalysis(input)
    if (record.conversionIssues === conversionIssues) assert.ok(failure.secondaryCategories.includes('gateway_conversion_failure'))
    if (record.transportFailure) assert.deepEqual(failure.transportFailure, transportFailure)
    const counts = failureCounts([{ status: 'failed', failureCategory: category, failure }])
    assert.equal(counts.primary[category], 1)
    if (record.conversionIssues === conversionIssues) assert.equal(counts.secondary.gateway_conversion_failure, 1)
  }
})

test('extension observation, actual rule execution and successful consumption are independent', () => {
  const frame = { type: 'response.metadata', headers: { 'X-Codex-Turn-State': 'synthetic-private' } }
  const raw = JSON.stringify([frame, { type: 'response.completed' }])
  const record = { conversionIssues: [{ ruleId: 'responses-turn-state', code: 'conversion_degraded' }] }
  for (const status of ['passed', 'failed']) {
    const item = { status, calls: [{ record, compatibility: compatibilityObservations(raw, record) }] }
    finishCompatibilityCoverage(item)
    assert.deepEqual(compatibilityCounts([item])['responses-turn-state'], { observed: 1, triggered: 1, consumed: status === 'passed' ? 1 : 0, triggeredAndConsumed: status === 'passed' ? 1 : 0 })
    assert.doesNotMatch(JSON.stringify(item), /synthetic-private/)
  }
  const preserved = { status: 'passed', calls: [{ compatibility: compatibilityObservations(`data: ${JSON.stringify(frame)}\n\n`) }] }
  finishCompatibilityCoverage(preserved)
  assert.deepEqual(compatibilityCounts([preserved])['responses-turn-state'], { observed: 1, triggered: 0, consumed: 1, triggeredAndConsumed: 0 })
  const ordinary = { status: 'passed', calls: [{ compatibility: compatibilityObservations('{"object":"chat.completion","model":"m"}') }] }
  finishCompatibilityCoverage(ordinary)
  assert.deepEqual(compatibilityCounts([ordinary])['responses-turn-state'], { observed: 0, triggered: 0, consumed: 0, triggeredAndConsumed: 0 })
  assert.equal(compatibilityObservations(undefined)[0].extensionObserved, null)
  const missing = compatibilityObservations('{"object":"chat.completion","vendor_integer":9007199254740993}')
  assert.equal(missing.find(entry => entry.ruleId === 'chat-response-model').extensionObserved, true)
})

test('provider failure cannot suppress restart, configuration checks or token revocation', async () => {
  const cases = [], id = 'synthetic'
  let source = { id, name: id, enabled: true }, tokens = [{ name: 'audit-relay', enabled: true }], restarts = 0
  const admin = async (path, body, method) => {
    if (path === 'model-sources') return { items: [structuredClone(source)] }
    if (path === 'model-groups') return { items: [{ id, name: id, enabled: true, models: ['m'] }] }
    if (path === 'api-tokens') { if (body) tokens.push(body); return { items: structuredClone(tokens) } }
    if (method === 'PUT') { source = structuredClone(body); return }
    if (path === `model-sources/${id}/enabled`) { source.enabled = body.enabled; return }
    if (method === 'DELETE') { tokens = tokens.filter(token => token.name !== `revoke-${id}`); return }
    assert.fail(path)
  }
  await checkPersistence({ id, source, token: 'synthetic-token', admin, needRoute() {},
    run: async (id, expected, action) => { try { await action(); cases.push({ id, status: 'passed' }); return true } catch (error) { cases.push({ id, status: error.code === 'blocked' ? 'blocked' : 'failed' }); return false } },
    restart: async () => { restarts++; source = structuredClone(source); tokens = structuredClone(tokens) }, runtimeEvidence() {},
    recovery: async () => { throw new Error('HTTP 524 synthetic failure') },
    call: async options => { if (!options.expected) throw new Error('HTTP 524 synthetic failure'); return { status: options.expected } },
    checkTokenReply() {},
  })
  assert.equal(restarts, 1)
  for (const name of ['call', 'enable-recovery', 'restart-recovery', 'token-call', 'revoke-recovery']) assert.equal(cases.find(item => item.id === name).status, 'failed')
  for (const name of ['save-config', 'disable', 'enable', 'restart-state', 'create-token', 'revoke-token', 'revoked-token-rejection']) assert.equal(cases.find(item => item.id === name).status, 'passed')
  assert.ok(!tokens.some(token => token.name.startsWith('revoke-')))
})
