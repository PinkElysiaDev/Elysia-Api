// Verify tray mode releases the real UI process and its WebKit services, not only Swift objects.
import assert from 'node:assert/strict'
import { closeSync, cpSync, existsSync, mkdirSync, openSync, readFileSync, renameSync, rmSync, writeFileSync } from 'node:fs'
import { homedir } from 'node:os'
import { join } from 'node:path'
import { execFileSync, spawn, spawnSync } from 'node:child_process'

const pause = ms => new Promise(resolve => setTimeout(resolve, ms))
const read = path => existsSync(path) ? readFileSync(path, 'utf8').trim() : ''
const alive = pid => { try { process.kill(pid, 0); return true } catch { return false } }

function webKitServices(pid) {
  try {
    const output = execFileSync('/bin/launchctl', ['print', `pid/${pid}`], { encoding: 'utf8', timeout: 3000 })
    return [...output.matchAll(/^\s*(\d+)\s+\S+\s+com\.apple\.WebKit\.(WebContent|Networking|GPU)(?:\.[^\s]+)?\s*$/gm)]
      .map(match => ({ pid: Number(match[1]), kind: match[2] })).filter(service => service.pid > 1 && alive(service.pid))
  } catch { return [] }
}

function residentKB(pids) {
  try {
    return execFileSync('/bin/ps', ['-p', pids.join(','), '-o', 'rss='], { encoding: 'utf8', timeout: 3000 })
      .trim().split(/\s+/).reduce((sum, value) => sum + Number(value), 0)
  } catch { return 0 }
}

async function wait(message, condition, timeout = 15000) {
  const deadline = Date.now() + timeout
  while (Date.now() < deadline) {
    if (await condition()) return
    await pause(100)
  }
  assert.fail(message)
}

export async function runNativeTrayFlow({ app, temp, bundleID, previewOnly }) {
  const directory = join(temp, 'native tray flow')
  const current = join(directory, 'ElysiaApi.app')
  const data = join(directory, 'data')
  const flowID = `${bundleID}.tray`
  mkdirSync(data, { recursive: true })
  if (!previewOnly) writeFileSync(join(data, 'fixture-mode'), 'tray-fixture')
  cpSync(app, current, { recursive: true })
  execFileSync('/usr/libexec/PlistBuddy', ['-c', `Set :CFBundleIdentifier ${flowID}`, join(current, 'Contents', 'Info.plist')])
  execFileSync('/usr/bin/codesign', ['--force', '--sign', '-', '--deep', current], { stdio: 'ignore' })
  const outputPath = join(data, 'tray-starter.log')
  const executable = join(current, 'Contents', 'MacOS', 'ElysiaApi')
  const environment = { ...process.env, ELYSIA_NATIVE_APP_TEST: '1', ELYSIA_NATIVE_TRAY_TEST: '1', ELYSIA_NATIVE_TEST_DATA: data }
  let startError
  const supervisorPIDs = new Set()
  const launchOwner = () => {
    const output = openSync(outputPath, 'a', 0o600)
    const owner = spawn(executable, ['--background'], { stdio: ['ignore', output, output], env: environment })
    closeSync(output)
    if (owner.pid) supervisorPIDs.add(owner.pid)
    owner.on('error', error => { startError = error })
    return owner
  }
  let starter = launchOwner()
  const uiPIDs = new Set()
  const servicePIDs = new Set()
  const duplicatePIDs = new Set()
  const liveUIs = () => execFileSync('/bin/ps', ['-axo', 'pid=,command='], { encoding: 'utf8', timeout: 3000 })
    .split('\n').filter(line => line.includes(executable) && /\s--webui-process(?:\s|$)/.test(line))
    .map(line => Number(line.trim().match(/^\d+/)[0])).sort((left, right) => left - right)
  const command = value => {
    const destination = join(data, 'tray-test-command')
    writeFileSync(`${destination}.next`, value)
    renameSync(`${destination}.next`, destination)
  }
  const healthy = async () => {
    try {
      const config = JSON.parse(read(join(data, 'config.json')))
      const response = await fetch(`http://127.0.0.1:${config.port}/health`, { signal: AbortSignal.timeout(500) })
      await response.arrayBuffer()
      return response.status === 200
    } catch { return false }
  }
  const heartbeatCount = () => Number(read(join(data, 'tray-heartbeats.txt')))
  let persistentOpenCount = 0
  const launchSecondShell = async () => {
    const secondOutput = openSync(outputPath, 'a', 0o600)
    const duplicate = spawn(executable, [], { stdio: ['ignore', secondOutput, secondOutput], env: environment })
    closeSync(secondOutput)
    if (duplicate.pid) duplicatePIDs.add(duplicate.pid)
    let error
    duplicate.on('error', value => { error = value })
    await wait('a second main shell must forward to the owner and exit cleanly', () => {
      if (error) throw error
      return duplicate.exitCode === 0 && !alive(duplicate.pid)
    })
  }
  let backendPID
  try {
    await wait('tray launch must start its owned healthy backend', async () => {
      if (startError) throw startError
      backendPID = Number(read(join(data, 'owned-backend.pid')))
      return backendPID > 1 && alive(backendPID) && await healthy()
    })
    assert(alive(starter.pid), 'the tray supervisor must remain alive')
    assert.equal(webKitServices(starter.pid).length, 0, 'background launch must not create WebKit services in the supervisor')
    let previousUI = 0
    const openPanel = async (show = () => command('show')) => {
      const beforeHeartbeat = heartbeatCount()
      await show()
      let uiPID
      await wait('show must start a fresh UI process and load its real DOM', () => {
        uiPID = Number(read(join(data, 'tray-ui.pid')))
        return uiPID > 1 && uiPID !== previousUI && alive(uiPID)
          && Number(read(join(data, 'tray-ui-ready.pid'))) === uiPID
      }, 25000)
      previousUI = uiPID
      uiPIDs.add(uiPID)
      let services
      await wait('the ready UI must own a real WebContent process', () => {
        services = webKitServices(uiPID)
        return services.some(service => service.kind === 'WebContent')
      })
      services.forEach(service => servicePIDs.add(service.pid))
      assert.equal(webKitServices(starter.pid).length, 0, 'WebKit must belong to the disposable UI, not the tray supervisor')
      assert.deepEqual(liveUIs(), [uiPID], 'the tray supervisor must own exactly one live UI process')
      assert.equal(Number(read(join(data, 'owned-backend.pid'))), backendPID, 'opening the UI must retain the original backend')
      if (!previewOnly) {
        await wait('the fixture WebUI must run its heartbeat while open', () => heartbeatCount() >= beforeHeartbeat + 2)
        await wait('persistent localStorage must increment across fresh UI processes', () =>
          Number(read(join(data, 'tray-open-count.txt'))) === persistentOpenCount + 1)
        persistentOpenCount += 1
      }
      return { uiPID, services }
    }

    for (let cycle = 1; cycle <= 3; cycle++) {
      const { uiPID, services } = await openPanel()
      if (cycle === 1) {
        for (let attempt = 0; attempt < 3; attempt++) {
          command('show')
          await wait('the tray hook must consume repeated show commands', () => !existsSync(join(data, 'tray-test-command')))
        }
        await launchSecondShell()
        assert.equal(Number(read(join(data, 'tray-ui.pid'))), uiPID, 'repeated show and a second shell must reuse the existing UI')
        assert.deepEqual(liveUIs(), [uiPID], 'showing an open panel must not create a second UI process')
        assert.equal(Number(read(join(data, 'owned-backend.pid'))), backendPID, 'a second shell must not create or restart the backend')
        assert(alive(starter.pid) && alive(backendPID) && await healthy(), 'the original supervisor and backend must retain ownership after a second launch')
        console.log('PASS: repeated show and a second shell with an open panel reuse one UI and the original backend')
      }
      const panelRSS = residentKB([uiPID, ...services.map(service => service.pid)])
      const supervisorRSS = residentKB([starter.pid])
      assert(panelRSS > 0 && supervisorRSS > 0, 'RSS measurements must sample the live UI and supervisor')
      command('close')
      await wait(`close cycle ${cycle} must terminate the UI and every owned WebKit process`, () =>
        !alive(uiPID) && services.every(service => !alive(service.pid)))
      assert(alive(starter.pid) && alive(backendPID) && await healthy(), 'closing the UI must leave the tray supervisor and API service healthy')
      assert.equal(Number(read(join(data, 'owned-backend.pid'))), backendPID, 'closing the UI must not restart the backend')
      assert.equal(webKitServices(starter.pid).length, 0, 'closed-window tray mode must have no WebKit services')
      assert.deepEqual(liveUIs(), [], 'closing the panel must leave no UI process in this test app')
      if (!previewOnly) {
        // Wait out a request already sent at close, then require the page to stay quiet.
        await pause(300)
        const closedHeartbeat = heartbeatCount()
        await pause(1200)
        assert.equal(heartbeatCount(), closedHeartbeat, 'closing the UI must stop its JavaScript and periodic HTTP traffic')
      }
      console.log(`PASS: tray close cycle ${cycle} exits the UI/WebKit processes and keeps the original API service healthy; `
        + `UI/WebKit RSS ${(panelRSS / 1024).toFixed(1)} MB → 0 MB, supervisor RSS ${(supervisorRSS / 1024).toFixed(1)} MB → ${(residentKB([starter.pid]) / 1024).toFixed(1)} MB`)
    }

    const { uiPID, services } = await openPanel(launchSecondShell)
    assert(alive(starter.pid) && alive(backendPID) && await healthy(), 'a second launch from tray mode must show the panel in the original service owner')
    console.log('PASS: a second shell in closed-window tray mode reopens the original owner’s UI without restarting its backend')
    starter.kill('SIGKILL')
    await wait('a supervisor crash must release its UI, WebKit services and owned backend', () =>
      !alive(starter.pid) && !alive(uiPID) && !alive(backendPID) && services.every(service => !alive(service.pid)), 20000)
    console.log('PASS: supervisor death exits its disposable UI/WebKit and the backend supervised through parent EOF')

    // Exercise AppKit's actual terminateLater path from the supervisor's command callback.
    const previousBackend = backendPID
    rmSync(join(data, 'tray-heartbeats.txt'), { force: true })
    startError = undefined
    starter = launchOwner()
    await wait('a new tray owner must start a fresh healthy backend after the previous owner exits', async () => {
      if (startError) throw startError
      backendPID = Number(read(join(data, 'owned-backend.pid')))
      return backendPID > 1 && backendPID !== previousBackend && alive(backendPID) && await healthy()
    })
    const finalPanel = await openPanel()
    command('quit')
    await wait('ordinary quit must finish AppKit cleanup and stop its supervisor, UI, WebKit and backend', () =>
      !alive(starter.pid) && !alive(finalPanel.uiPID) && !alive(backendPID)
      && finalPanel.services.every(service => !alive(service.pid)), 20000)
    assert.equal(starter.exitCode, 0, 'ordinary quit must exit cleanly after releasing all owned resources')
    console.log('PASS: ordinary quit completes terminateLater cleanup and exits the supervisor, UI/WebKit and backend')
    if (!previewOnly) console.log(`PASS: persistent WebUI localStorage survives ${persistentOpenCount} UI processes and a supervisor restart`)
  } catch (error) {
    console.error(`Tray starter log:\n${read(outputPath).slice(-6000)}\nBackend log:\n${read(join(data, 'elysia-api.log')).slice(-3000)}`)
    console.error('Tray process evidence:', {
      supervisor: starter.pid, allSupervisors: [...supervisorPIDs], backend: backendPID,
      ui: [...uiPIDs], webKit: [...servicePIDs], duplicateShells: [...duplicatePIDs],
      lastUI: read(join(data, 'tray-ui.pid')), readyUI: read(join(data, 'tray-ui-ready.pid')),
    })
    throw error
  } finally {
    if (alive(starter.pid)) command('quit')
    const owned = [...new Set([...supervisorPIDs, backendPID, Number(read(join(data, 'owned-backend.pid'))),
      Number(read(join(data, 'tray-ui.pid'))), ...uiPIDs, ...duplicatePIDs])].filter(pid => {
      if (!(pid > 1)) return false
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
