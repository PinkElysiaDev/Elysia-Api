import { dirname, join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { VerificationRun } from './verification/runner.mjs'
import { verifyRace } from './verification/race.mjs'

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const mode = process.argv[2]
if (!['race'].includes(mode)) throw new Error('Usage: node scripts/verify-protocol.mjs race')
const run = await VerificationRun.create(root, join(dirname(root), '.cache', `protocol-${mode}-${Date.now()}`), mode)
try {
  await verifyRace(run, root)
} catch (error) {
  await run.finish('failed', error.message)
  process.exitCode = 1
}
