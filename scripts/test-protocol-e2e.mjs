import { spawn } from 'node:child_process'
import { mkdtemp, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { dirname, join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { randomBytes } from 'node:crypto'
import { setTimeout as delay } from 'node:timers/promises'

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const web = join(root, 'packages/webui')
const runDirectory = await mkdtemp(join(tmpdir(), 'elysia-protocol-e2e-'))
const binary = join(runDirectory, process.platform === 'win32' ? 'elysia.exe' : 'elysia')
const backendPort = Number(process.env.PROTOCOL_E2E_PORT ?? 18769)
const webPort = Number(process.env.PLAYWRIGHT_PORT ?? 5279)
const backendURL = `http://127.0.0.1:${backendPort}`
const webURL = `http://127.0.0.1:${webPort}`
const token = randomBytes(24).toString('hex')
const config = join(runDirectory, 'config.json')
const environment = {
  ...process.env,
  ELYSIA_DEV_PROXY: backendURL,
  PROTOCOL_E2E_URL: backendURL,
  PROTOCOL_E2E_TOKEN: token,
  PLAYWRIGHT_PORT: String(webPort),
  PLAYWRIGHT_EXTERNAL_SERVER: '1',
}
const owned = []

function start(command, args, cwd) {
  const child = spawn(command, args, { cwd, env: environment, stdio: 'inherit', windowsHide: true })
  child.completion = new Promise((resolve, reject) => {
    child.once('error', reject)
    child.once('exit', (code, signal) => resolve({ code, signal }))
  })
  // Keep early launch failures observable while waiting for HTTP readiness.
  child.completion.catch(() => {})
  owned.push(child)
  return child
}

async function run(command, args, cwd) {
  const result = await start(command, args, cwd).completion
  if (result.code !== 0) throw new Error(`${command} failed (${result.code ?? result.signal})`)
}

async function waitReady(child, url) {
  for (let attempt = 0; attempt < 120; attempt++) {
    if (child.exitCode !== null || child.signalCode !== null) throw new Error(`Service exited before readiness: ${url}`)
    try {
      const response = await fetch(url, { signal: AbortSignal.timeout(1000) })
      if (response.ok) return
    } catch { /* Starting services may refuse connections. */ }
    await delay(250)
  }
  throw new Error(`Service readiness timed out: ${url}`)
}

try {
  await writeFile(config, JSON.stringify({
    databasePath: join(runDirectory, 'gateway.sqlite3'), host: '127.0.0.1', port: backendPort,
    panelAccessToken: token, logLifecycleVersion: 1, openBrowserOnStart: false,
    modelCatalog: { enabled: false, syncIntervalMinutes: 0 },
  }), { mode: 0o600 })
  await run('go', ['build', '-o', binary, '.'], join(root, 'backend'))
  const backend = start(binary, ['-config', config], runDirectory)
  const vite = start(process.execPath, [join(root, 'node_modules/vite/bin/vite.js'), '--host', '127.0.0.1', '--port', String(webPort), '--strictPort'], web)
  await Promise.all([waitReady(backend, `${backendURL}/health`), waitReady(vite, webURL)])
  await run(process.execPath, [join(root, 'node_modules/@playwright/test/cli.js'), 'test', ...(process.argv.length > 2 ? process.argv.slice(2) : ['protocol-v2.spec.ts', 'conversion-policy.spec.ts']), '--project=chromium', '--workers=1'], web)
} finally {
  // Close only children created by this runner. No port-based or name-based kills.
  for (const child of owned.reverse()) {
    if (child.exitCode === null && child.signalCode === null) child.kill('SIGTERM')
    await child.completion.catch(() => {})
  }
  console.log(`Isolated protocol E2E data retained at ${runDirectory}`)
}
