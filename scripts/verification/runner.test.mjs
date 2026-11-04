import assert from 'node:assert/strict'
import { mkdtemp, readFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { dirname, join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import test from 'node:test'
import { VerificationRun } from './runner.mjs'

const root = resolve(dirname(fileURLToPath(import.meta.url)), '../..')

test('reports preserve command failures, timeout and unexecuted environment separately', async () => {
  const directory = await mkdtemp(join(tmpdir(), 'elysia-verification-runner-'))
  const run = await VerificationRun.create(root, directory, 'runner-test')
  assert.equal(run.report.status, 'not_run')
  assert.equal(await run.execute('pass', process.execPath, ['-e', 'console.log("evidence")']), true)
  assert.equal(await run.execute('fail', process.execPath, ['-e', 'process.exit(7)']), false)
  assert.equal(await run.execute('timeout', process.execPath, ['-e', 'setInterval(() => {}, 1000)'], { timeoutMillis: 200 }), false)
  assert.equal(await run.execute('missing-command', join(directory, 'missing'), []), false)
  await run.finish('inconclusive', 'test environment unavailable')
  const report = JSON.parse(await readFile(join(directory, 'report.json'), 'utf8'))
  assert.equal(report.status, 'inconclusive')
  assert.deepEqual(report.steps.map(step => step.status), ['passed', 'failed', 'failed', 'failed'])
  assert.equal(report.steps[1].code, 7)
  assert.equal(report.steps[2].timedOut, true)
  assert.match(await readFile(report.steps[0].log, 'utf8'), /evidence/)
  assert(report.commit && report.compilerVersion && report.goVersion)
  assert.equal(report.source.baseCommit, report.commit)
  for (const step of report.steps) assert.equal(step.source.baseCommit, report.commit)
})

test('parallel tasks respect slots, group exclusion, barriers and failed dependencies', async () => {
  const directory = await mkdtemp(join(tmpdir(), 'elysia-verification-batch-'))
  const run = await VerificationRun.create(root, directory, 'batch-test')
  const task = (name, options = {}) => ({ name, command: process.execPath, args: ['-e', 'setTimeout(() => {}, 150)'], ...options })
  const tasks = [task('left', { slots: 2, group: 'package' }), task('right', { slots: 2 }), task('same-package', { group: 'package' }),
    task('measure', { exclusive: true }), task('after'), task('fail', { args: ['-e', 'process.exit(7)'] }), task('blocked', { after: ['fail'] })]
  assert.equal(await run.executeTasks(tasks), false)
  const records = await Promise.all(run.report.tasks.filter(task => task.report).map(async task => {
    const report = JSON.parse(await readFile(task.report, 'utf8'))
    return { ...task, step: report.steps[0] }
  }))
  const events = records.flatMap(record => [{ time: Date.parse(record.step.startedAt), slots: record.slots }, { time: Date.parse(record.step.finishedAt), slots: -record.slots }]).sort((a, b) => a.time - b.time || a.slots - b.slots)
  let used = 0
  for (const event of events) { used += event.slots; assert(used >= 0 && used <= 4) }
  const byName = Object.fromEntries(records.map(record => [record.name, record.step]))
  assert(Date.parse(byName['same-package'].startedAt) >= Date.parse(byName.left.finishedAt))
  assert(Date.parse(byName.measure.startedAt) >= Date.parse(byName['same-package'].finishedAt))
  assert(Date.parse(byName.after.startedAt) >= Date.parse(byName.measure.finishedAt))
  assert.equal(run.report.tasks.at(-1).status, 'not_run')
  assert.equal(new Set(records.map(record => record.report)).size, records.length)
})
