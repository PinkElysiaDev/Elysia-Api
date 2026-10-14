import assert from 'node:assert/strict'
import { mkdtempSync, mkdirSync, readFileSync, readdirSync, symlinkSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import test from 'node:test'
import { copySnapshotPlan, readSnapshotPlan } from './source-snapshot.mjs'

test('checked-in snapshot manifest matches publishable sources', () => {
  const root = fileURLToPath(new URL('../', import.meta.url))
  assert.ok(readSnapshotPlan(root).length > 0)
})

function fixture() {
  const root = mkdtempSync(join(tmpdir(), 'elysia-snapshot-test-'))
  const write = (path, content) => { mkdirSync(dirname(join(root, path)), { recursive: true }); writeFileSync(join(root, path), content) }
  const manifest = (files) => write('scripts/source-snapshot.json', JSON.stringify({ version: 1, files }))
  return { root, write, manifest }
}

test('source archives without Git include only the explicit manifest', () => {
  const { root, write, manifest } = fixture()
  write('backend/main.go', 'package main\n')
  write('backend/config.json', '{"apiKey":"synthetic-secret"}')
  write('backend/untracked.go', 'synthetic local source')
  write('backend/test-results/local.json', 'synthetic runtime log')
  manifest(['backend/main.go'])
  const output = join(root, 'snapshot')
  copySnapshotPlan(readSnapshotPlan(root), output)
  assert.deepEqual(readdirSync(join(output, 'backend')), ['main.go'])
  assert.equal(readFileSync(join(output, 'backend/main.go'), 'utf8'), 'package main\n')
})

test('runtime config, traversal, duplicates and missing manifests fail closed', () => {
  const { root, write, manifest } = fixture()
  assert.throws(() => readSnapshotPlan(root), /ENOENT/)
  write('backend/main.go', 'package main\n')
  write('backend/config.json', '{}')
  for (const paths of [['backend/config.json'], ['../main.go'], ['backend/main.go', 'backend/main.go'], ['backend\\main.go']]) {
    manifest(paths)
    assert.throws(() => readSnapshotPlan(root), /Invalid source snapshot entry/)
  }
})

test('a directory link cannot export files outside the source archive', () => {
  const { root, manifest } = fixture()
  const outside = mkdtempSync(join(tmpdir(), 'elysia-snapshot-outside-'))
  writeFileSync(join(outside, 'secret.go'), 'synthetic secret')
  mkdirSync(join(root, 'backend'))
  symlinkSync(outside, join(root, 'backend', 'linked'), process.platform === 'win32' ? 'junction' : 'dir')
  manifest(['backend/linked/secret.go'])
  assert.throws(() => readSnapshotPlan(root), /escapes repository/)
})
