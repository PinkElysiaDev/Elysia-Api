import assert from 'node:assert/strict'
import { spawn } from 'node:child_process'
import { createWriteStream } from 'node:fs'
import { mkdtemp, readFile, readdir, writeFile } from 'node:fs/promises'
import { randomBytes } from 'node:crypto'
import { createServer } from 'node:net'
import { tmpdir } from 'node:os'
import { join, resolve } from 'node:path'
import { setTimeout as delay } from 'node:timers/promises'

// A historical binary creates the database through public administrator APIs.
// Only isolated children and synthetic protocol copies are changed.
const oldBinary = process.argv[2]
const newBinary = process.argv[3]
assert(oldBinary && newBinary, 'usage: node scripts/smoke-projection-upgrade.mjs <old-binary> <new-binary>')
const directory = await mkdtemp(join(tmpdir(), 'elysia-projection-upgrade-smoke-'))
const token = randomBytes(24).toString('hex')
const listener = createServer()
await new Promise((done) => listener.listen(0, '127.0.0.1', done))
const port = listener.address().port
await new Promise((done) => listener.close(done))
const base = `http://127.0.0.1:${port}`
const config = join(directory, 'config.json')
await writeFile(config, JSON.stringify({ databasePath: join(directory, 'upgrade.sqlite3'), host: '127.0.0.1', port, panelAccessToken: token, openBrowserOnStart: false, modelCatalog: { enabled: false } }))
let child, completion, log
async function api(path, method = 'GET', body) {
  const response = await fetch(`${base}/api/admin/protocols${path}`, { method, signal: AbortSignal.timeout(10000), headers: { Authorization: `Bearer ${token}`, 'Content-Type': 'application/json' }, body: body === undefined ? undefined : JSON.stringify(body) })
  // Protocol fixtures deliberately contain integers above Number.MAX_SAFE_INTEGER.
  // Keep their original lexemes when copying the definition back to the API.
  const result = JSON.parse(await response.text(), (_key, value, context) =>
    typeof value === 'number' && Number.isInteger(value) && !Number.isSafeInteger(value)
      ? JSON.rawJSON(context.source) : value)
  assert.equal(response.status, 200, `${path}: ${JSON.stringify(result)}`)
  return result.data
}
async function start(binary, label) {
  log = createWriteStream(join(directory, `${label}.log`))
  child = spawn(resolve(binary), ['-config', config], { cwd: directory, windowsHide: true, stdio: ['ignore', 'pipe', 'pipe'] })
  child.stdout.pipe(log); child.stderr.pipe(log)
  completion = new Promise((done, reject) => { child.once('error', reject); child.once('exit', done) })
  completion.catch(() => {})
  for (let i = 0; i < 120; i++) {
    assert.equal(child.exitCode, null, `${label} exited before readiness`)
    try { if ((await fetch(`${base}/health`, { signal: AbortSignal.timeout(1000) })).ok) return } catch { /* startup */ }
    await delay(250)
  }
  throw new Error(`${label} readiness timeout`)
}
async function stop() {
  if (child && child.exitCode === null && child.signalCode === null) child.kill('SIGTERM')
  if (completion) await completion
  if (log) await new Promise((done) => log.end(done))
  child = undefined
}
async function snapshot() {
  const listing = await api('')
  const reports = {}
  for (const activation of listing.active) reports[activation.protocolId] = await api(`/${activation.protocolId}/revisions/${activation.revisionHash}`)
  return { listing, reports, backups: (await readdir(directory)).filter((name) => name.includes('.pre-protocol-v2-')).sort() }
}
try {
  await start(oldBinary, 'old')
  const oldCompiler = (await api('/schema')).compilerVersion
  const original = (await api('')).drafts.find((draft) => draft.protocolId === 'openai-responses').definition
  const definition = { ...original, id: 'historical-responses-copy', name: 'Historical custom', requires: [] }
  const saved = await api(`/${definition.id}/draft`, 'PUT', definition)
  const verified = await api(`/${definition.id}/verify`, 'POST', { draftHash: saved.draft.hash })
  assert.equal(verified.report.passed, true, JSON.stringify(verified.report.issues))
  await api(`/${definition.id}/activate`, 'POST', { revisionHash: verified.revision.hash, expectedActive: '' })
  const unsavedDraft = { ...definition, name: 'Operator unfinished draft' }
  const draftResponse = await fetch(`${base}/api/admin/protocols/${definition.id}/draft`, { method: 'PUT', headers: { Authorization: `Bearer ${token}`, 'Content-Type': 'application/json', 'If-Match': saved.draft.hash }, body: JSON.stringify(unsavedDraft) })
  assert.equal(draftResponse.status, 200)
  const before = await snapshot()
  await stop()
  await start(newBinary, 'new')
  const compiler = (await api('/schema')).compilerVersion
  assert.equal(compiler, '2.0.0-dev.22')
  const upgraded = await snapshot()
  const custom = upgraded.listing.active.find((entry) => entry.protocolId === definition.id)
  assert.equal(custom.revisionHash, verified.revision.hash)
  assert.equal(upgraded.listing.loaded[definition.id], verified.revision.hash)
  assert.deepEqual(upgraded.listing.drafts.find((entry) => entry.protocolId === definition.id), before.listing.drafts.find((entry) => entry.protocolId === definition.id))
  assert.equal(upgraded.reports[definition.id].report.compilerVersion, compiler)
  const preview = await api('/conversion-policies/preview', 'POST', { policy: { schemaVersion: 1, id: 'upgrade-preview', rules: [] }, phase: 'request', context: { source: { definitionId: definition.id }, target: { definitionId: 'google-generate-content' } }, input: { schemaVersion: 1, source: {}, content: [], parameters: { responses_include: ['reasoning.encrypted_content'] } } })
  assert.equal(preview.output.parameters?.responses_include, undefined)
  assert.equal(preview.effective.origins['responses-include'], 'engine-default')
  const configAfterUpgrade = await readFile(config, 'utf8')
  await stop()
  for (let restart = 0; restart < 10; restart++) {
    await start(newBinary, `restart-${restart}`)
    assert.deepEqual(await snapshot(), upgraded, `restart ${restart} changed revisions, reports, drafts, activations or backups`)
    await stop()
    assert.equal(await readFile(config, 'utf8'), configAfterUpgrade)
  }
  const evidence = { oldCompiler, compiler, customRevisionPreserved: true, customDraftPreserved: true, inheritedProjection: true, restarts: 10, protocolStateStable: true, passed: true }
  await writeFile(join(directory, 'evidence.json'), JSON.stringify(evidence, null, 2))
  console.log(JSON.stringify(evidence, null, 2))
} finally {
  await stop()
  console.log(`Isolated upgrade smoke retained at ${directory}`)
}
