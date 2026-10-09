import assert from 'node:assert/strict'
import { spawn, spawnSync } from 'node:child_process'
import { createWriteStream } from 'node:fs'
import { mkdtemp, readdir, writeFile } from 'node:fs/promises'
import { randomBytes } from 'node:crypto'
import { createServer } from 'node:net'
import { tmpdir } from 'node:os'
import { join, resolve } from 'node:path'
import { setTimeout as delay } from 'node:timers/promises'

// Always operate on a SQLite snapshot in a new temporary directory. The source
// fixture and its historical backup metadata are never edited.
const [binary, fixture] = process.argv.slice(2)
assert(binary && fixture, 'usage: node scripts/smoke-zero-presets.mjs <binary> <zero-preset-fixture.sqlite>')
const directory = await mkdtemp(join(tmpdir(), 'elysia-zero-presets-smoke-'))
const database = join(directory, 'replay.sqlite3')
function python(source, args) {
  const result = spawnSync('python', ['-c', source, ...args], { encoding: 'utf8', windowsHide: true })
  assert.equal(result.status, 0, result.stderr)
  return result.stdout.trim()
}
python(`import sqlite3,sys,pathlib
source=sqlite3.connect(pathlib.Path(sys.argv[1]).resolve().as_uri()+'?mode=ro',uri=True)
target=sqlite3.connect(sys.argv[2])
assert source.execute('SELECT count(*) FROM protocol_activations').fetchone()[0]==0
source.backup(target)
source.close(); target.close()
`, [resolve(fixture), database])
const listener = createServer()
await new Promise((done) => listener.listen(0, '127.0.0.1', done))
const port = listener.address().port
await new Promise((done) => listener.close(done))
const token = randomBytes(24).toString('hex')
const config = join(directory, 'config.json')
await writeFile(config, JSON.stringify({ databasePath: database, host: '127.0.0.1', port, panelAccessToken: token, openBrowserOnStart: false, modelCatalog: { enabled: false } }))
const originalMetadata = python(`import sqlite3,sys
db=sqlite3.connect(sys.argv[1]); row=db.execute("SELECT value FROM settings WHERE key='protocol_engine_v2_backup'").fetchone(); print(row[0] if row else '')
`, [database])
let child, completion, log
async function stop() {
  if (child && child.exitCode === null && child.signalCode === null) child.kill('SIGTERM')
  if (completion) await completion
  if (log) await new Promise((done) => log.end(done))
  child = undefined
}
async function start(label) {
  log = createWriteStream(join(directory, `${label}.log`))
  child = spawn(resolve(binary), ['-config', config], { cwd: directory, windowsHide: true, stdio: ['ignore', 'pipe', 'pipe'] })
  child.stdout.pipe(log); child.stderr.pipe(log)
  completion = new Promise((done, reject) => { child.once('error', reject); child.once('exit', done) })
  completion.catch(() => {})
  for (let attempt = 0; attempt < 120; attempt++) {
    assert.equal(child.exitCode, null, `${label} exited`)
    let response
    try { response = await fetch(`http://127.0.0.1:${port}/api/admin/protocols`, { signal: AbortSignal.timeout(1000), headers: { Authorization: `Bearer ${token}` } }) } catch { await delay(100); continue }
    assert.equal(response.status, 200)
    return (await response.json()).data
  }
  throw new Error('startup timeout')
}
try {
  let original
  for (let restart = 0; restart <= 10; restart++) {
    const listing = await start(`start-${restart}`)
    assert.equal(listing.runtimeReady, true, JSON.stringify(listing.startupFailure))
    assert.equal(listing.runtimeError, undefined)
    for (const id of ['openai-chat-completions', 'openai-responses', 'anthropic-messages', 'google-generate-content']) {
      assert(listing.loaded[id], `${id} not loaded`)
      assert.equal(listing.active.find((entry) => entry.protocolId === id)?.generation, 1)
    }
    if (!original) original = listing
    else assert.deepEqual(listing, original, `restart ${restart} changed protocol state`)
    await stop()
  }
  const final = JSON.parse(python(`import sqlite3,sys,json
db=sqlite3.connect(sys.argv[1])
counts={table:db.execute('SELECT count(*) FROM '+table).fetchone()[0] for table in ['protocol_revisions','protocol_drafts','protocol_activations','protocol_verification_reports','custom_protocols']}
row=db.execute("SELECT value FROM settings WHERE key='protocol_engine_v2_backup'").fetchone()
print(json.dumps({'counts':counts,'historicalMetadata':row[0] if row else ''}))
`, [database]))
  assert.equal(final.historicalMetadata, originalMetadata)
  assert.equal(final.counts.protocol_verification_reports, 4)
  assert.equal(final.counts.custom_protocols, 0)
  assert.equal((await readdir(directory)).filter((name) => name.includes('.protocol-snapshot-v2-')).length, 0)
  const evidence = { passed: true, runtimeReady: true, loaded: 4, restarts: 10, counts: final.counts, historicalMetadataPreserved: true, snapshotsCreated: 0, directory }
  await writeFile(join(directory, 'evidence.json'), JSON.stringify(evidence, null, 2))
  console.log(JSON.stringify(evidence, null, 2))
} finally { await stop(); console.log(`Isolated startup replay retained at ${directory}`) }
