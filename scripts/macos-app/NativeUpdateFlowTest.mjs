// Exercises the production AppDelegate/update handoff in isolated test app bundles.
import assert from 'node:assert/strict'
import { closeSync, cpSync, existsSync, mkdirSync, openSync, readFileSync, readdirSync, rmSync, writeFileSync } from 'node:fs'
import { homedir } from 'node:os'
import { join } from 'node:path'
import { execFileSync, spawn, spawnSync } from 'node:child_process'

const pause = ms => new Promise(resolve => setTimeout(resolve, ms))
const read = path => existsSync(path) ? readFileSync(path, 'utf8').trim() : ''
const alive = pid => { try { process.kill(pid, 0); return true } catch { return false } }

export async function runNativeUpdateFlow(options) {
  await runFlow({ ...options, brokenUI: false })
  if (!options.previewOnly) await runFlow({ ...options, brokenUI: true })
}

async function runFlow({ app, temp, bundleID, brokenUI }) {
  const directory = join(temp, `native update '$()\` ${brokenUI ? 'broken UI' : 'ready UI'} flow`)
  const current = join(directory, "Current '$()` ElysiaApi.app")
  const staging = join(directory, '.ElysiaApi-update-e2e')
  const staged = join(staging, 'ElysiaApi.app')
  const data = join(directory, 'data')
  const flowID = `${bundleID}.update${brokenUI ? '.broken' : ''}`
  mkdirSync(data, { recursive: true })
  if (brokenUI) writeFileSync(join(data, 'fixture-mode'), 'broken-ui')
  cpSync(app, current, { recursive: true })
  cpSync(app, staged, { recursive: true })
  for (const [bundle, version] of [[current, 'v1.0.0'], [staged, 'v99.0.0']]) {
    const contents = join(bundle, 'Contents')
    execFileSync('/usr/libexec/PlistBuddy', ['-c', `Set :CFBundleIdentifier ${flowID}`, join(contents, 'Info.plist')])
    mkdirSync(join(contents, 'Resources'), { recursive: true })
    writeFileSync(join(contents, 'Resources', 'version.txt'), `${version}\n`)
    execFileSync('/usr/bin/codesign', ['--force', '--sign', '-', '--deep', bundle], { stdio: 'ignore' })
  }
  const outputPath = join(data, 'native-starter.log')
  const output = openSync(outputPath, 'a', 0o600)
  const starter = spawn(join(current, 'Contents', 'MacOS', 'ElysiaApi'), ['--native-update-parent'], {
    stdio: ['ignore', output, output],
    env: { ...process.env, ELYSIA_NATIVE_APP_TEST: '1', ELYSIA_NATIVE_TEST_DATA: data, ELYSIA_NATIVE_STAGED_APP: staged,
      // Allow multiple 3-second health polls and cold WebKit startup before testing UI readiness.
      ...(brokenUI ? { ELYSIA_NATIVE_UPDATE_LAUNCH_TIMEOUT: '12' } : {}) },
  })
  closeSync(output)
  let startError
  starter.on('error', error => { startError = error })
  let exitCode
  starter.on('exit', code => { exitCode = code })
  const pidFiles = ['native-ready.pid', 'old-backend.pid', 'owned-backend.pid', 'owned-helper.pid', 'new-native.pid', 'new-backend.pid', 'rollback-native.pid']
  const ownedPIDs = () => new Set([starter.pid, ...pidFiles.map(name => Number(read(join(data, name))))].filter(pid => Number.isSafeInteger(pid) && pid > 1))
  try {
    const deadline = Date.now() + 70000
    let sawLoadedBrokenHTML = false
    let sawRecoverableBackup = false
    while (Date.now() < deadline) {
      if (startError) throw startError
      const log = read(join(data, 'update.log'))
      if (brokenUI) {
        if (log.includes('Update committed')) assert.fail('loaded HTML without a ready React UI must never commit the update')
        const newNative = Number(read(join(data, 'new-native.pid')))
        if (newNative > 1 && Number(read(join(data, 'new-html-loaded.pid'))) === newNative) {
          sawLoadedBrokenHTML = true
          for (const backup of readdirSync(directory).filter(name => name.startsWith('.ElysiaApi-backup-'))) {
            if (read(join(directory, backup, 'Contents', 'Resources', 'version.txt')) === 'v1.0.0') sawRecoverableBackup = true
          }
        }
        const rollbackNative = Number(read(join(data, 'rollback-native.pid')))
        const backend = Number(read(join(data, 'owned-backend.pid')))
        const failedBackend = Number(read(join(data, 'new-backend.pid')))
        if (log.includes('已恢复并启动旧版本') && rollbackNative > 1 && alive(rollbackNative)
            && backend > 1 && backend !== failedBackend && alive(backend) && await backendHealthy(data)) break
      } else if (read(join(data, 'native-ready.pid')) && log.includes('Update committed')) break
      await pause(100)
    }
    const oldBackend = Number(read(join(data, 'old-backend.pid')))
    assert(!alive(oldBackend) && !alive(starter.pid) && exitCode === 0, 'the old shell and its backend must exit before replacement')
    const activeBackend = Number(read(join(data, 'owned-backend.pid')))
    assert(activeBackend > 1 && activeBackend !== oldBackend && alive(activeBackend), 'the relaunched shell must own one active backend')
    const log = read(join(data, 'update.log'))
    if (brokenUI) {
      const failedNative = Number(read(join(data, 'new-native.pid')))
      const failedBackend = Number(read(join(data, 'new-backend.pid')))
      const restoredNative = Number(read(join(data, 'rollback-native.pid')))
      assert(sawLoadedBrokenHTML, 'broken UI regression must complete HTML navigation before the readiness timeout')
      assert(sawRecoverableBackup, 'the old bundle must remain recoverable while the new HTML fails application readiness')
      assert(!read(join(data, 'native-ready.pid')) && !log.includes('Update committed'), 'loaded HTML without a ready React UI must never commit the update')
      assert(failedNative > 1 && failedBackend > 1 && !alive(failedNative) && !alive(failedBackend), 'rollback must stop the failed native shell and its owned backend')
      assert(restoredNative > 1 && restoredNative !== failedNative && alive(restoredNative) && await backendHealthy(data), 'rollback must relaunch the old shell and a healthy owned backend')
      assert.equal(read(join(current, 'Contents', 'Resources', 'version.txt')), 'v1.0.0', 'readiness timeout must restore the old bundle')
      assert(log.includes('已恢复并启动旧版本'), 'the helper must record readiness timeout and successful rollback')
    } else {
      const nativePID = Number(read(join(data, 'native-ready.pid')))
      assert(nativePID > 1 && alive(nativePID), 'the updated native shell and WebView must acknowledge readiness')
      assert.equal(read(join(current, 'Contents', 'Resources', 'version.txt')), 'v99.0.0', 'the current bundle must be the staged version')
      assert(log.includes('Update committed'), 'the helper must commit after readiness acknowledgement')
    }
    assert(!existsSync(staging), 'finished handoff must release its staging directory')
    assert(!readdirSync(directory).some(name => name.startsWith('.ElysiaApi-backup-')), 'committed or restored handoff must release its transaction backup')
    console.log(brokenUI
      ? 'PASS: loaded HTML with a broken React UI preserves the backup, times out, stops the failed shell/backend, and restores the old app'
      : 'PASS: real native update stops the old backend, replaces after shell exit, and relaunches a ready shell/backend/WebView')
  } catch (error) {
    console.error(`Native starter log:\n${read(outputPath).slice(-6000)}\nUpdate helper log:\n${read(join(data, 'update.log')).slice(-6000)}\nBackend log:\n${read(join(data, 'elysia-api.log')).slice(-3000)}`)
    console.error('Native readiness evidence:', Object.fromEntries([...pidFiles, 'new-html-loaded.pid'].map(name => [name, read(join(data, name))])))
    throw error
  } finally {
    // Only signal PIDs whose command still refers to this unique test directory.
    const owned = [...ownedPIDs()].filter(pid => {
      try { return execFileSync('/bin/ps', ['-p', String(pid), '-o', 'command='], { encoding: 'utf8' }).includes(directory) }
      catch { return false }
    })
    for (const pid of owned) { try { process.kill(pid, 'SIGTERM') } catch {} }
    const deadline = Date.now() + 19000
    while (owned.some(alive) && Date.now() < deadline) await pause(100)
    for (const pid of owned) { try { process.kill(pid, 'SIGKILL') } catch {} }
    spawnSync('/usr/bin/defaults', ['delete', flowID], { stdio: 'ignore' })
    for (const path of [join(homedir(), 'Library', 'WebKit', flowID), join(homedir(), 'Library', 'Caches', flowID),
      join(homedir(), 'Library', 'Saved Application State', `${flowID}.savedState`)]) rmSync(path, { recursive: true, force: true })
    rmSync(directory, { recursive: true, force: true })
  }
}

async function backendHealthy(data) {
  try {
    const config = JSON.parse(read(join(data, 'config.json')))
    const response = await fetch(`http://127.0.0.1:${config.port}/health`, { signal: AbortSignal.timeout(500) })
    await response.arrayBuffer()
    return response.status === 200
  } catch { return false }
}
