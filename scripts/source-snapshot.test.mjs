import assert from 'node:assert/strict'
import { mkdtempSync, mkdirSync, readFileSync, readdirSync, symlinkSync, unlinkSync, writeFileSync } from 'node:fs'
import { spawnSync } from 'node:child_process'
import { tmpdir } from 'node:os'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import test from 'node:test'
import { copySnapshotPlan, listSnapshotSources, readSnapshotPlan } from './source-snapshot.mjs'

test('checked-in snapshot manifest matches publishable sources', () => {
  const root = fileURLToPath(new URL('../', import.meta.url))
  const plan = readSnapshotPlan(root)
  assert.ok(plan.length > 0)
  for (const part of ['spec', 'mapping']) {
    assert.ok(plan.some(({ path }) => path === `backend/protocol/builtin/packages/anthropic/${part}.json`))
  }
})

function fixture() {
  const root = mkdtempSync(join(tmpdir(), 'elysia-snapshot-test-'))
  const write = (path, content) => { mkdirSync(dirname(join(root, path)), { recursive: true }); writeFileSync(join(root, path), content) }
  const manifest = (files) => write('scripts/source-snapshot.json', JSON.stringify({ version: 1, files }))
  return { root, write, manifest }
}

test('Git snapshots require explicit new sources and exclude deleted tracked files', () => {
  const { root, write, manifest } = fixture()
  const git = (...args) => {
    const result = spawnSync('git', args, { cwd: root, encoding: 'utf8' })
    assert.equal(result.status, 0, result.stderr)
  }
  write('backend/main.go', 'package main\n')
  write('backend/retired.go', 'package main\n')
  git('init', '--quiet')
  git('add', 'backend/main.go', 'backend/retired.go')
  unlinkSync(join(root, 'backend/retired.go'))
  write('backend/new.go', 'package main\n')
  write('backend/protocol/testdata/events.sse', 'data: {"value":9007199254740993}\n\n')
  write('backend/capture.sse', 'synthetic runtime capture')
  write('backend/unselected.go', 'synthetic local source')
  write('backend/config.json', '{"apiKey":"synthetic-secret"}')
  assert.deepEqual(listSnapshotSources(root), ['backend/main.go'])
  const sources = listSnapshotSources(root, ['backend/new.go', 'backend/protocol/testdata/events.sse'])
  assert.deepEqual(sources, ['backend/main.go', 'backend/new.go', 'backend/protocol/testdata/events.sse'])
  manifest(sources)
  assert.deepEqual(readSnapshotPlan(root).map(({ path }) => path), sources)
  manifest(['backend/new.go'])
  assert.throws(() => readSnapshotPlan(root), /manifest is stale/)
  assert.throws(() => listSnapshotSources(root, ['backend/config.json']), /Invalid source snapshot entry/)
  assert.throws(() => listSnapshotSources(root, ['backend/capture.sse']), /Invalid source snapshot entry/)
})

test('source archives without Git include only the explicit manifest', () => {
  const { root, write, manifest } = fixture()
  write('backend/main.go', 'package main\n')
  const stream = 'data: {"value":9007199254740993}\n\n'
  write('backend/protocol/testdata/events.sse', stream)
  write('backend/config.json', '{"apiKey":"synthetic-secret"}')
  write('backend/untracked.go', 'synthetic local source')
  write('backend/test-results/local.json', 'synthetic runtime log')
  manifest(['backend/main.go', 'backend/protocol/testdata/events.sse'])
  const output = join(root, 'snapshot')
  copySnapshotPlan(readSnapshotPlan(root), output)
  assert.deepEqual(readdirSync(join(output, 'backend')), ['main.go', 'protocol'])
  assert.equal(readFileSync(join(output, 'backend/main.go'), 'utf8'), 'package main\n')
  assert.equal(readFileSync(join(output, 'backend/protocol/testdata/events.sse'), 'utf8'), stream)
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
