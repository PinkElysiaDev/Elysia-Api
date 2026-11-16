import { randomInt } from 'node:crypto'
import { prepareRaceEnvironment } from './toolchain.mjs'
import { verifyBackend } from './backend.mjs'

const concurrencyPattern = 'TestProtocolDraftConcurrent|TestProtocolConcurrentReaders|TestProtocolFailedActivation|TestGenerationJob|TestRunSession|TestRunTurn_ConcurrencyGuard|TestWebSocketScheduler|TestWebSocketForceClose|TestEnqueueConcurrent|TestA2ACancel|TestAgentCallerRetryAndCancel|TestMCPCLICancel|TestLongReplay'

/** Run one full race pass, or an explicitly requested repeated concurrency stress. */
export async function verifyRace(run, root) {
  const isStressOnly = process.argv.includes('--stress-only')
  if (!isStressOnly) {
    const jobs = process.argv.filter(arg => arg.startsWith('--jobs='))
    return verifyBackend(run, root, ['--suite=all', '--race', ...jobs])
  }
  run.report.scope = 'concurrency-stress-only'
  let environment
  try {
    environment = await prepareRaceEnvironment(root)
    run.report.toolchain = environment.evidence
    await run.save()
  } catch (error) {
    await run.finish('inconclusive', `Toolchain unavailable: ${error.message}`)
    process.exitCode = 2
  }
  if (environment) {
    const options = { env: { ...environment.env, GORACE: 'halt_on_error=1' } }
    let passed = true
    for (const concurrency of [1, 2, 12]) {
      const seed = process.argv.find(arg => arg.startsWith('--seed='))?.slice(7) || randomInt(1, 2147483647)
      const env = { ...options.env, GOMAXPROCS: String(concurrency) }
      passed = await run.execute(`race-stress-${concurrency}`, 'go', ['test', '-race', './protocol/...', './storage', './relay', './server', './agent', '-run', concurrencyPattern, '-count=20', '-timeout=60m', `-shuffle=${seed}`, '-json'], { env, timeoutMillis: 3900000 })
      if (!passed) break
    }
    await run.finish(passed ? 'passed' : 'failed')
    if (!passed) process.exitCode = 1
  }
}
