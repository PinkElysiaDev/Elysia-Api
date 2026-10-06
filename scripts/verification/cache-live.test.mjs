import test from 'node:test'
import assert from 'node:assert/strict'
import { cacheCredentialEnvironment, cacheTargets } from './cache-live.mjs'

test('cache profiles select distinct credential references without embedding secrets', () => {
  assert.equal(cacheTargets['anthropic-api'].model, 'claude-haiku-4-5')
  assert.equal(cacheTargets['gemini-api'].model, 'gemini-3-flash-preview')
  const packet = Object.fromEntries([...new Set(Object.values(cacheTargets).map(target => target.keyEnv))].map(name => [name, 'synthetic-secret']))
  assert.deepEqual(cacheCredentialEnvironment(packet), packet)
  assert.throws(() => cacheCredentialEnvironment({ ...packet, unknown: 'credential' }))
  assert.throws(() => cacheCredentialEnvironment({ ...packet, ELYSIA_VERIFY_GEMINI_KEY: '' }))
  assert.equal(JSON.stringify(cacheTargets).includes('synthetic-secret'), false)
})
