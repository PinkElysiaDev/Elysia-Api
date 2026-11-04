import { readFile } from 'node:fs/promises'
import { resolve } from 'node:path'
import { VerificationRun } from './runner.mjs'

const [manifest] = process.argv.slice(2)
if (!manifest) throw new Error('Usage: node scripts/verification/batch.mjs manifest.json')
const plan = JSON.parse(await readFile(resolve(manifest), 'utf8'))
const run = await VerificationRun.create(plan.root, plan.directory, 'batch')
const passed = await run.executeTasks(plan.tasks, plan.slots || 4)
await run.finish(passed ? 'passed' : 'failed', 'Bounded parallel tasks with exclusive measurement barriers')
if (!passed) process.exitCode = 1
