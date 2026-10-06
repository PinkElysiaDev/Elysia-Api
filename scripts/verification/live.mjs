import { join, dirname } from 'node:path'
import { readFile } from 'node:fs/promises'
import { readCredential } from './credential.mjs'

/** Runs the serial paid experiment only after explicit live opt-in. */
export async function verifyLive(run, root) {
  const suite = process.argv.find(arg => arg.startsWith('--suite='))?.slice(8) || 'all'
  if (!['all','cache','matrix','extended','followup','diagnostics','breakpoint','custom','gemini-native-tools','cache-gaps','cache-followup'].includes(suite)) throw new Error('Unknown live suite')
  if (suite === 'cache-gaps' || suite === 'cache-followup') {
    const { verifyCacheGaps } = await import('./cache-live.mjs')
    await verifyCacheGaps(run, root)
    return
  }
  run.report.liveScope = process.argv.includes('--preflight') ? 'preflight' : suite
  run.report.liveTarget = process.env.ELYSIA_LIVE_TARGET || 'all'
  await run.save()
  const credential = await readCredential()
  const args = ['test', './server', '-run', '^TestProtocolLive$', '-count=1', '-v', '-timeout=90m']
  if (process.argv.includes('--preflight')) args.push('-short')
  const passed = await run.execute('live', 'go', args, {
    env: { ...process.env, ELYSIA_LIVE_TESTS: '1', ELYSIA_VERIFY_API_KEY: credential, ELYSIA_VERIFY_DIR: run.directory, ELYSIA_LIVE_SUITE: suite, ELYSIA_LIVE_BUDGET: join(dirname(root), '.cache', 'protocol-live-budget.json') },
    timeoutMillis: 5500000,
  })
  let evidence
  try { evidence = JSON.parse(await readFile(join(run.directory, 'live.json'), 'utf8')) }
  catch (error) {
    if (error.code !== 'ENOENT') throw error
    await run.finish('inconclusive', 'Live process ended without a result file; inspect the step log')
    process.exitCode = 2
    return
  }
  const status = !passed || evidence.cases.some(item => item.status === 'failed') ? 'failed' : evidence.cases.some(item => item.status !== 'passed') ? 'inconclusive' : 'passed'
  await run.finish(status)
  if (status !== 'passed') process.exitCode = status === 'failed' ? 1 : 2
}
