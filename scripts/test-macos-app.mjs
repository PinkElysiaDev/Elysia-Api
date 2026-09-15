// Isolated native integration tests: no production data, login registrations or notifications.
import { chmodSync, cpSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { dirname, join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { execFileSync, spawnSync } from 'node:child_process'

const repo = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const source = join(repo, 'scripts', 'macos-app')
const temp = mkdtempSync(join(tmpdir(), 'elysia-native-tests-'))
const app = join(temp, 'ElysiaNativeTests.app')
const macos = join(app, 'Contents', 'MacOS')
const data = join(temp, 'data')
const previewOnly = process.argv.includes('--panel')
const bundleID = `dev.pinkelysiadev.ElysiaApi.tests.${process.pid}`
function run(cmd, args, options = {}) {
  const result = spawnSync(cmd, args, { stdio: 'inherit', ...options })
  if (result.status !== 0) throw result.error ?? new Error(`${cmd}: ${result.status ?? result.signal}`)
}
try {
  mkdirSync(macos, { recursive: true })
  mkdirSync(data)
  if (!previewOnly) {
    for (const arch of ['arm64', 'x86_64']) {
      run('xcrun', ['--sdk', 'macosx', 'clang', '-target', `${arch}-apple-macos12.0`, '-x', 'c', '-', '-o', join(data, `fixture-${arch}`)], {
        input: 'int main(void) { return 0; }\n', stdio: ['pipe', 'inherit', 'inherit'],
      })
    }
    run('xcrun', ['lipo', '-create', join(data, 'fixture-arm64'), join(data, 'fixture-x86_64'), '-output', join(data, 'fixture-universal')])
  }
  writeFileSync(join(app, 'Contents', 'Info.plist'), `<?xml version="1.0"?><plist version="1.0"><dict>
  <key>CFBundleIdentifier</key><string>${bundleID}</string>
  <key>CFBundleExecutable</key><string>ElysiaApi</string>
  <key>CFBundlePackageType</key><string>APPL</string>
  <key>NSAppTransportSecurity</key><dict><key>NSAllowsLocalNetworking</key><true/></dict>
  </dict></plist>`)
  cpSync(previewOnly ? join(repo, 'dist', 'standalone', 'ElysiaApi.app', 'Contents', 'MacOS', 'elysia-api') : join(source, 'BackendFixture.py'), join(macos, 'elysia-api'))
  chmodSync(join(macos, 'elysia-api'), 0o755)
  run('xcrun', ['--sdk', 'macosx', 'swiftc', '-D', 'NATIVE_TESTS', '-target', `${process.arch === 'arm64' ? 'arm64' : 'x86_64'}-apple-macos12.0`,
    '-o', join(macos, 'ElysiaApi'), join(source, 'MacSupport.swift'), join(source, 'NativeTests.swift'), join(source, 'main.swift')])
  run('codesign', ['--force', '--sign', '-', '--deep', app])
  run(join(macos, 'ElysiaApi'), previewOnly ? ['--panel'] : [], { timeout: 120000, env: { ...process.env, ELYSIA_NATIVE_TEST_DATA: data, ELYSIA_NATIVE_SCREENSHOT: join(repo, 'dist', 'macos-panel-preview.png') } })
} finally {
  // Also clean up an owned fixture if the test process timed out before Swift could run its cleanup.
  try {
    const pid = Number(readFileSync(join(data, 'owned-backend.pid'), 'utf8'))
    if (Number.isSafeInteger(pid) && pid > 1) {
      const command = execFileSync('ps', ['-p', String(pid), '-o', 'command='], { encoding: 'utf8' })
      if (command.includes(join(macos, 'elysia-api'))) process.kill(pid, 'SIGKILL')
    }
  } catch { /* The suite normally stopped the child already. */ }
  // Always remove this unique test domain, including timeouts; never touch production preferences.
  spawnSync('defaults', ['delete', bundleID], { stdio: 'ignore' })
  rmSync(temp, { recursive: true, force: true })
}
