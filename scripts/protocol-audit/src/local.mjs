import { createServer as createSocketServer } from 'node:net'
import { defaults } from './audit.mjs'
import { mkdtemp, writeFile, rm } from 'node:fs/promises'
import { join } from 'node:path'
import { tmpdir } from 'node:os'
import { spawn } from 'node:child_process'
import { randomUUID } from 'node:crypto'
import { waitForRuntime } from './evidence.mjs'

export async function localInstance(root, command, signal, timeoutMs = defaults.timeoutMs, maxLogBytes = defaults.maxResponseBytes) {
  const directory = await mkdtemp(join(tmpdir(), 'elysia-audit-'))
  const panel = randomUUID(), token = randomUUID()
  const socket = createSocketServer()
  await new Promise((resolve, reject) => { socket.once('error', reject); socket.listen(0, '127.0.0.1', resolve) })
  const port = socket.address().port
  await new Promise(resolve => socket.close(resolve))
  const baseUrl = `http://127.0.0.1:${port}`
  const binary = join(directory, process.platform === 'win32' ? 'gateway.exe' : 'gateway')
  const path = join(directory, 'config.json')
  const config = { host: '127.0.0.1', port, databasePath: join(directory, 'gateway.sqlite3'), secretKeyPath: join(directory, 'secret.key'), panelAccessToken: panel, openBrowserOnStart: false, modelCatalog: { enabled: false, syncIntervalMinutes: 0 }, outbound: { deniedIpRanges: [] }, logLifecycleVersion: 1, httpTimeout: Math.ceil(timeoutMs / 1000) }
  // Only this isolated synthetic-traffic instance captures bodies. Its database
  // is deleted after redacted call evidence has been exported.
  config.usageLog = { bodyMaxKB: Math.ceil(maxLogBytes / 1024), bodyOnErrorOnly: false, externalizeMedia: false }
  const startups = []
  let child, completion, stopped = false, bytes = 0, truncated = false
  const chunks = []
  const capture = value => {
    const data = Buffer.from(value), room = maxLogBytes - bytes
    if (room > 0) { const part = data.subarray(0, room); chunks.push(part); bytes += part.length }
    if (data.length > room) truncated = true
  }
  const log = () => Buffer.concat(chunks).toString('utf8') + (truncated ? `\n[audit: backend log truncated at ${maxLogBytes} bytes]\n` : '')
  const stop = async () => {
    if (!child || child.exitCode !== null || child.signalCode !== null) return
    child.kill('SIGTERM')
    const timer = setTimeout(() => child.kill('SIGKILL'), 1500)
    await completion; clearTimeout(timer)
  }
  const start = async () => {
    if (signal?.aborted) throw new Error('Run interrupted')
    const environment = Object.fromEntries(Object.entries(process.env).filter(([name]) => !name.startsWith('ELYSIA_')))
    child = spawn(binary, ['-config', path], { cwd: directory, env: environment, stdio: ['ignore', 'pipe', 'pipe'], windowsHide: true })
    completion = new Promise(resolve => { child.once('error', error => { capture(error.message); resolve() }); child.once('close', resolve) })
    child.stdout.on('data', capture); child.stderr.on('data', capture)
    const state = await waitForRuntime(baseUrl, panel, { signal, timeoutMs: Math.min(timeoutMs, 30000), alive: () => child.pid && child.exitCode === null && child.signalCode === null })
    startups.push({ observedAt: new Date().toISOString(), ...state })
  }
  const close = async () => { if (stopped) return; stopped = true; await stop(); await rm(directory, { recursive: true, force: true }) }
  try {
    await writeFile(path, JSON.stringify(config), { mode: 0o600 })
    await command('backend-build', ['go', 'build', '-o', binary, '.'], join(root, 'backend'))
    await start()
  } catch (error) { await close(); error.backendLog = log(); throw error }
  return { baseUrl, panel, token, startups, restart: async () => { await stop(); await start() }, close, log }
}
