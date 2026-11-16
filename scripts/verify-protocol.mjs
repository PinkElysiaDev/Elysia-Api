import { dirname, join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { VerificationRun } from './verification/runner.mjs'
import { verifyRace } from './verification/race.mjs'
import { verifyBackend } from './verification/backend.mjs'
import { readFile, writeFile } from 'node:fs/promises'

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const mode = process.argv[2]
if (!['check', 'race', 'live', 'performance'].includes(mode)) throw new Error('Usage: node scripts/verify-protocol.mjs check [--suite=gemini-stream|output|conversion|all] [--race] [--jobs=1..4] | race [--stress-only] | live | performance')
const resume = process.argv.find(arg => arg.startsWith('--resume='))?.slice(9)
if (resume && (mode !== 'live' || !process.argv.includes('--suite=cache-gaps'))) throw new Error('Resume is only supported for cache-gaps')
const directory = resume ? resolve(resume) : join(dirname(root), '.cache', `protocol-${mode}-${Date.now()}`)
if (resume) {
  const prior = await readFile(join(directory, 'report.json'))
  await readFile(join(directory, 'cache-checkpoint.json'))
  await writeFile(join(directory, `report-before-resume-${Date.now()}.json`), prior, { flag: 'wx' })
}
const run = await VerificationRun.create(root, directory, mode)
try {
  if (mode === 'check') await verifyBackend(run, root)
  else if (mode === 'race') await verifyRace(run, root)
  else if (mode === 'live') { const { verifyLive } = await import('./verification/live.mjs'); await verifyLive(run, root) }
  else { const { verifyPerformance } = await import('./verification/performance.mjs'); await verifyPerformance(run, root) }
} catch (error) {
  await run.finish('failed', error.message)
  process.exitCode = 1
}
