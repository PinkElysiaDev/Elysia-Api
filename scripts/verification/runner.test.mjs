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
})
