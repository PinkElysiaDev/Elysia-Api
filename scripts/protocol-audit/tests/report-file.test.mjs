import test from 'node:test'
import assert from 'node:assert/strict'
import { mkdtemp, readFile, writeFile, rename, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { atomicFile } from '../src/report-file.mjs'

test('Windows report publication retries temporary locks without repeating evidence generation', async t => {
  const root = await mkdtemp(join(tmpdir(), 'audit-report-'))
  t.after(() => rm(root, { recursive: true, force: true }))
  const path = join(root, 'report.json')
  await writeFile(path, 'old')
  let attempts = 0; const waits = []
  await atomicFile(path, 'new', { platform: 'win32', wait: async ms => waits.push(ms), rename: async (from, to) => {
    assert.equal(await readFile(to, 'utf8'), 'old')
    assert.equal(await readFile(from, 'utf8'), 'new')
    if (++attempts < 3) throw Object.assign(new Error('sharing violation'), { code: 'EPERM' })
    await rename(from, to)
  } })
  assert.equal(await readFile(path, 'utf8'), 'new'); assert.deepEqual(waits, [25, 50])
  for (const [platform, code, expected] of [['win32', 'EACCES', 8], ['win32', 'ENOSPC', 1], ['linux', 'EPERM', 1]]) {
    attempts = 0
    await assert.rejects(atomicFile(path, 'unpublished', { platform, wait: async () => {}, rename: async () => {
      attempts++; throw Object.assign(new Error(code), { code })
    } }), { code })
    assert.equal(attempts, expected); assert.equal(await readFile(path, 'utf8'), 'new')
  }
})
