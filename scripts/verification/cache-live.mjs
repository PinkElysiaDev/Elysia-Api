import { dirname, join } from 'node:path'
import { readFile, access } from 'node:fs/promises'
import { setTimeout as delay } from 'node:timers/promises'
import { readCredential } from './credential.mjs'

export const cacheTargets = {
  'chat-completions-api': { model: 'gpt-6.1-sol', keyEnv: 'ELYSIA_VERIFY_OPENAI_KEY' },
  'responses-api': { model: 'gpt-6.1-sol', keyEnv: 'ELYSIA_VERIFY_OPENAI_KEY' },
  'anthropic-api': { model: 'claude-haiku-4-5', keyEnv: 'ELYSIA_VERIFY_ANTHROPIC_KEY' },
  'gemini-api': { model: 'gemini-3-flash-preview', keyEnv: 'ELYSIA_VERIFY_GEMINI_KEY' },
}

/** Validates an in-memory credential packet; reports never contain its values. */
export function cacheCredentialEnvironment(packet) {
  const required = [...new Set(Object.values(cacheTargets).map(target => target.keyEnv))]
  if (!packet || Array.isArray(packet) || typeof packet !== 'object' || Object.keys(packet).some(key => !required.includes(key))) throw new Error('Invalid credential packet keys')
  for (const key of required) if (typeof packet[key] !== 'string' || !packet[key].trim() || /[\r\n]/.test(packet[key])) throw new Error(`Missing or invalid credential reference: ${key}`)
  return Object.fromEntries(required.map(key => [key, packet[key]]))
}

/** Executes one bounded multi-model experiment, including durable TTL cohorts. */
export async function verifyCacheGaps(run, root) {
  const isFollowup = process.argv.includes('--suite=cache-followup')
  const parent = process.argv.find(arg => arg.startsWith('--parent='))?.slice(9)
  if (isFollowup && !parent) throw new Error('cache-followup requires its original --parent evidence directory')
  const names = [...new Set(Object.values(cacheTargets).map(target => target.keyEnv))]
  let credentials
  if (names.every(name => process.env[name])) credentials = cacheCredentialEnvironment(Object.fromEntries(names.map(name => [name, process.env[name]])))
  else {
    console.log('Provide one JSON object with the three ELYSIA_VERIFY_*_KEY values via hidden stdin. It is passed only to the test child.')
    let packet
    try { packet = JSON.parse(await readCredential()) } catch { throw new Error('Invalid hidden credential JSON') }
    credentials = cacheCredentialEnvironment(packet)
  }
  run.report.cacheValidation = { origin: 'https://moyuu.cc', targets: cacheTargets, maximumCalls: 96, cleanupReserve: 4, maximumHours: 2 }
  await run.save()
  const budget = join(dirname(root), '.cache', 'protocol-cache-budget.json')
  const env = { ...process.env, ...credentials, ELYSIA_LIVE_TESTS: '1', ELYSIA_LIVE_SUITE: isFollowup ? 'cache-followup' : 'cache-gaps', ELYSIA_CACHE_PARENT: parent || '', ELYSIA_LIVE_TARGETS: JSON.stringify(cacheTargets), ELYSIA_VERIFY_DIR: run.directory, ELYSIA_LIVE_BUDGET: budget }
  let passed
  try {
    if (isFollowup) {
      await readFile(join(parent, 'cache-checkpoint.json'))
      const deadline = Date.now() + 2 * 60 * 60 * 1000
      for (;;) {
        try { await access(`${budget}.lock`) } catch (error) { if (error.code === 'ENOENT') break; throw error }
        if (Date.now() >= deadline) throw new Error('Parent experiment still owns the paid boundary')
        await delay(1000)
      }
    }
    passed = await run.execute('cache-live', 'go', ['test', './server', '-run', '^TestProtocolLive$', '-count=1', '-v', '-timeout=125m'], { env, timeoutMillis: 126 * 60 * 1000 })
  }
  finally { for (const name of names) { delete env[name]; delete credentials[name] } }
  let evidence
  try { evidence = JSON.parse(await readFile(join(run.directory, 'live.json'), 'utf8')) }
  catch { await run.finish('inconclusive', 'No durable live result; inspect the process log and checkpoint'); process.exitCode = 2; return }
  const failed = !passed || evidence.cases.some(item => item.status === 'failed')
  const positive = Object.keys(cacheTargets).every(target => evidence.cases.some(item => item.target === target && item.stream && item.assessment?.read === 'observed_nonzero' && item.storedRequestId && item.status === 'passed'))
  const status = failed ? 'failed' : positive && evidence.cases.every(item => item.status === 'passed') ? 'passed' : 'inconclusive'
  await run.finish(status, 'Transport assertions and nonzero counters are separate; consult per-case assessments and timing observations')
  if (status !== 'passed') process.exitCode = failed ? 1 : 2
}
