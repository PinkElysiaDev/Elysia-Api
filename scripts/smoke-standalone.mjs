import assert from 'node:assert/strict'
import { spawn, execFileSync } from 'node:child_process'
import { createHash, randomBytes } from 'node:crypto'
import { createWriteStream } from 'node:fs'
import { mkdtemp, readFile, writeFile } from 'node:fs/promises'
import { createServer } from 'node:net'
import { tmpdir } from 'node:os'
import { dirname, join, resolve } from 'node:path'
import { setTimeout as delay } from 'node:timers/promises'
import { fileURLToPath } from 'node:url'

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const platforms = { win32: 'windows', linux: 'linux', darwin: 'darwin' }
const architectures = { x64: 'amd64', arm64: 'arm64' }
const platform = platforms[process.platform]
const architecture = architectures[process.arch]
assert(platform && architecture, 'No standalone target for this host')
const executable = `elysia-api-${platform}-${architecture}${platform === 'windows' ? '.exe' : ''}`
const binary = join(root, 'dist', 'standalone', executable)
const binaryBytes = await readFile(binary)
const expectedCommit = execFileSync('git', ['rev-parse', '--short', 'HEAD'], { cwd: root, encoding: 'utf8', windowsHide: true }).trim()
const directory = await mkdtemp(join(tmpdir(), 'elysia-standalone-smoke-'))
const token = randomBytes(24).toString('hex')
const startupTimeoutMillis = 30000
const requestTimeoutMillis = 2000

const listener = createServer()
await new Promise((resolve, reject) => {
  listener.once('error', reject)
  listener.listen(0, '127.0.0.1', resolve)
})
const port = listener.address().port
await new Promise((resolve, reject) => listener.close(error => error ? reject(error) : resolve()))
const baseURL = `http://127.0.0.1:${port}`
const config = join(directory, 'config.json')
await writeFile(config, JSON.stringify({
  host: '127.0.0.1', port, databasePath: join(directory, 'gateway.sqlite3'),
  panelAccessToken: token, openBrowserOnStart: false, logLifecycleVersion: 1,
  modelCatalog: { enabled: false, syncIntervalMinutes: 0 },
}), { mode: 0o600 })
const log = createWriteStream(join(directory, 'server.log'))
const child = spawn(binary, ['-config', config], { cwd: directory, stdio: ['ignore', 'pipe', 'pipe'], windowsHide: true })
child.stdout.pipe(log, { end: false })
child.stderr.pipe(log, { end: false })
const completion = new Promise((resolve, reject) => {
  child.once('error', reject)
  child.once('close', (code, signal) => resolve({ code, signal }))
})
completion.catch(() => {})

async function readJSON(path, isAdmin = false) {
  const response = await fetch(`${baseURL}${path}`, {
    signal: AbortSignal.timeout(requestTimeoutMillis),
    headers: isAdmin ? { Authorization: `Bearer ${token}` } : {},
  })
  assert.equal(response.status, 200, path)
  const result = await response.json()
  if (!isAdmin) return result
  assert.equal(result.ok, true, path)
  return result.data
}

try {
  let health
  const deadline = Date.now() + startupTimeoutMillis
  while (Date.now() < deadline) {
    assert(child.exitCode === null && child.signalCode === null, 'Binary exited before readiness; inspect server.log')
    try {
      health = await readJSON('/health')
      break
    } catch { // New databases may still be migrating while the listener starts.
      await delay(250)
    }
  }
  assert(health, 'Standalone readiness timed out')
  assert.equal(health.database, true)
  assert.equal(health.status, 'ok')
  assert.equal(health.commit, expectedCommit)
  const schema = await readJSON('/api/admin/protocols/schema', true)
  assert.equal(schema.schemaVersion, 2)
  const compilerSource = await readFile(join(root, 'backend/protocol/definition.go'), 'utf8')
  const compilerVersion = compilerSource.match(/CompilerVersion\s*=\s*"([^"]+)"/)?.[1]
  assert(compilerVersion, 'Compiler version declaration missing')
  assert.equal(schema.compilerVersion, compilerVersion)
  const enabled = await readJSON('/api/admin/protocols/enabled', true)
  assert.deepEqual(enabled.items.map(item => item.id).sort(), ['anthropic-messages', 'google-generate-content', 'openai-chat-completions', 'openai-responses'])
  assert(enabled.items.every(item => item.revision && item.canGenerate && item.hasAgentPolicy))
  const ui = await fetch(`${baseURL}/ui/`, { signal: AbortSignal.timeout(requestTimeoutMillis) })
  assert.equal(ui.status, 200)
  const html = await ui.text()
  const script = html.match(/<script[^>]+src="([^"]+)"/)?.[1]
  assert(script, 'Embedded UI script missing')
  const asset = await fetch(new URL(script, ui.url), { signal: AbortSignal.timeout(requestTimeoutMillis) })
  assert.equal(asset.status, 200)
  assert((await asset.arrayBuffer()).byteLength > 0)
  const evidence = {
    executable, sha256: createHash('sha256').update(binaryBytes).digest('hex'),
    platform, architecture, health, compilerVersion: schema.compilerVersion,
    protocols: enabled.items.map(({ id, revision }) => ({ id, revision })),
    embeddedUI: true, passed: true, timestamp: new Date().toISOString(),
  }
  await writeFile(join(directory, 'evidence.json'), JSON.stringify(evidence, null, 2))
  console.log(JSON.stringify(evidence, null, 2))
} finally {
  // Only this isolated child is owned by the smoke runner.
  if (child.exitCode === null && child.signalCode === null) child.kill('SIGTERM')
  await completion
  await new Promise(resolve => log.end(resolve))
  console.log(`Isolated standalone smoke data retained at ${directory}`)
}
