import { spawn, execFileSync } from 'node:child_process'
import { readFile, writeFile, mkdir, copyFile, readdir } from 'node:fs/promises'
import { resolve, join, dirname, basename } from 'node:path'
import { fileURLToPath } from 'node:url'
import { createHash } from 'node:crypto'
import { cpus, platform, arch } from 'node:os'
import { createServer } from 'node:net'
import { VerificationRun } from './verification/runner.mjs'

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const args = process.argv.slice(2)
const option = (name, fallback) => args.find(arg => arg.startsWith(`--${name}=`))?.slice(name.length + 3) ?? fallback
const output = resolve(option('output', join(root, '.tmp-dev/conversion-flames')))
const binary = resolve(option('binary', join(output, 'server.test.exe')))
const env = { ...process.env, GOGC: '100', GOMEMLIMIT: '8GiB' }
const digest = bytes => createHash('sha256').update(bytes).digest('hex')

async function viewerPort() {
  const server = createServer()
  await new Promise((resolve, reject) => { server.once('error', reject); server.listen(0, '127.0.0.1', resolve) })
  const port = server.address().port
  await new Promise(resolve => server.close(resolve))
  return port
}

function memoryStatus() {
  if (platform() !== 'win32') return { status: 'unmeasured', reason: 'Windows commitment precheck is required for this recorded matrix' }
  const script = '$state = Get-CimInstance Win32_OperatingSystem; [Console]::WriteLine($state.FreePhysicalMemory); [Console]::WriteLine($state.FreeVirtualMemory)'
  try {
    const values = execFileSync('powershell', ['-NoProfile', '-Command', script], { encoding: 'utf8', windowsHide: true, stdio: ['ignore', 'pipe', 'pipe'] }).trim().split(/\s+/).map(Number)
    if (values.length !== 2 || values.some(value => !Number.isFinite(value) || value < 0)) throw new Error('Invalid memory sample')
    return { availablePhysicalGiB: values[0] / 2 ** 20, availableCommitGiB: values[1] / 2 ** 20 }
  } catch (error) {
    return { status: 'unmeasured', reason: 'Memory precheck failed; workload was not started', error: String(error.stderr || error.message) }
  }
}

async function metadata() {
  await mkdir(output, { recursive: true })
  const identity = {
    commit: execFileSync('git', ['rev-parse', 'HEAD'], { cwd: root, encoding: 'utf8' }).trim(),
    go: execFileSync('go', ['version'], { encoding: 'utf8' }).trim(),
    created: new Date().toISOString(), cpu: cpus()[0]?.model, logicalCPUs: cpus().length,
    platform: platform(), architecture: arch(), GOGC: env.GOGC, GOMEMLIMIT: env.GOMEMLIMIT,
    GOMAXPROCS: process.env.GOMAXPROCS || 'Go default', pgo: 'off', memory: memoryStatus(),
    binarySHA256: digest(await readFile(binary)),
    productionDiff: execFileSync('git', ['diff', '31ea3a9', '--', 'backend', ':!backend/**/*_test.go'], { cwd: root, encoding: 'utf8' }),
  }
  await writeFile(join(output, 'environment.json'), JSON.stringify(identity, null, 2))
  await writeFile(join(output, `environment-${basename(binary)}.json`), JSON.stringify(identity, null, 2))
  return identity
}

async function measure() {
  const environment = await metadata()
  const run = await VerificationRun.create(root, output, 'conversion-profiling')
  run.report.environment = environment
  const mode = option('mode', 'cpu')
  const archive = async () => copyFile(join(output, 'report.json'), join(output, `report-${mode}-${Date.now()}.json`))
  const selection = option('cases', 'image2,image31,nodes,events').split(',')
  const repeats = mode === 'cpu' ? 3 : 1
  const cases = {
    image2: ['image', 2, 1, 'off', 1], image8: ['image', 8, 1, 'off', 1], image16: ['image', 16, 1, 'off', 1], image31: ['image', 31, 1, 'off', 1],
    nodes: ['nodes', 2, 1, 'off', 1], events: ['events', 2, 1, 'off', 1], small: ['small', 2, 1, 'off', 1],
    small4: ['small', 2, 4, 'off', 1], small32: ['small', 2, 32, 'off', 1],
    image2w4: ['image', 2, 4, 'off', 1], image8w4: ['image', 8, 4, 'off', 1], image31w4: ['image', 31, 4, 'off', 1],
    image2body: ['image', 2, 1, 'body', 1], image2media: ['image', 2, 1, 'media', 1],
    image31body: ['image', 31, 1, 'body', 1], image31media: ['image', 31, 1, 'media', 1],
    candidates4: ['image', 2, 1, 'off', 4], candidates16: ['image', 2, 1, 'off', 16],
  }
  const protocols = { chat: 'openai-chat-completions', responses: 'openai-responses', anthropic: 'anthropic-messages', gemini: 'google-generate-content' }
  for (const [source, sourceID] of Object.entries(protocols)) {
    for (const [target, targetID] of Object.entries(protocols)) {
      cases[`${source}-${target}`] = ['small', 2, 1, 'off', 1, sourceID, targetID]
      cases[`${source}-${target}-tools`] = ['small', 2, 1, 'off', 1, sourceID, targetID, '1']
    }
  }
  for (const name of selection) {
    if (!cases[name]) throw new Error(`Unknown case ${name}`)
    const [workload, size, workers, log, candidates, sourceID = '', targetID = '', matrixTools = ''] = cases[name]
    for (let repeat = 0; repeat < repeats; repeat++) {
      const step = `${option('tag', '')}${mode}-${name}-${repeat + 1}`
      const memory = memoryStatus()
      // Sustained 2 MiB gateway samples peak below 0.13 GiB. One GiB
      // reserves room for both other processes and runtime overhead; larger
      // workloads retain their conservative gates, especially 31 MiB x 4.
      const commitNeeded = size === 31 && workers === 4 ? 16 : workload === 'image' && size * workers >= 32 ? 5 : workload === 'image' && size > 2 ? 2 : workload === 'small' ? 0.5 : 1
      const physicalNeeded = size === 31 && workers === 4 ? 12 : 2
      if (memory.availableCommitGiB < commitNeeded || memory.availablePhysicalGiB < physicalNeeded || memory.status === 'unmeasured') {
        run.report.steps.push({ name: step, status: 'environment_blocked', memory, commitNeeded, physicalNeeded })
        await run.save(); continue
      }
      const directory = join(output, step)
      await mkdir(directory, { recursive: true })
      await writeFile(join(directory, 'environment.json'), JSON.stringify({ ...environment, sampledAt: new Date().toISOString(), memory,
        binary, mode, workload, sizeMiB: size, workers, log, candidates, sourceID, targetID, matrixTools,
        forcedGCBeforeTiming: mode !== 'measure', exactResponseVerification: option('verify-exact', '') === '1', seconds: mode === 'cpu' ? Number(option('seconds', '60')) : null,
      }, null, 2))
      const passed = await run.execute(step, binary, ['-test.run=^TestConversionFlamegraph$', '-test.v', '-test.timeout=30m'], {
        cwd: join(root, 'backend'), timeoutMillis: 1800000,
        env: { ...env, ELYSIA_FLAME_WORKLOAD: workload, ELYSIA_FLAME_MODE: mode, ELYSIA_FLAME_OUTPUT: directory,
          ELYSIA_FLAME_SECONDS: option('seconds', '60'), ELYSIA_CONVERSION_HTTP_MIB: String(size), ELYSIA_CONVERSION_HTTP_WORKERS: String(workers),
          ELYSIA_CONVERSION_HTTP_LOG: log, ELYSIA_CONVERSION_HTTP_CANDIDATES: String(candidates), ELYSIA_FLAME_SOURCE: sourceID, ELYSIA_FLAME_TARGET: targetID, ELYSIA_FLAME_MATRIX_TOOLS: matrixTools, ELYSIA_FLAME_VERIFY_EXACT: option('verify-exact', '') },
      })
      if (!passed) { await run.finish('failed', step); await archive(); process.exitCode = 1; return }
      if (mode === 'cpu') {
        const top = execFileSync('go', ['tool', 'pprof', '-top', binary, join(directory, 'cpu.pprof')], { encoding: 'utf8', windowsHide: true })
        await writeFile(join(directory, 'cpu-top.txt'), top)
        const match = top.match(/Total samples = ([\d.]+)(ms|s)/)
        const samples = match ? Number(match[1]) * (match[2] === 'ms' ? 0.001 : 1) : 0
        Object.assign(run.report.steps.at(-1), { cpuSamplesSeconds: samples, sufficientCPUSamples: samples >= 10 })
        await run.save()
        if (samples < 10 && option('seconds', '60') !== '180') {
          const extended = join(output, `${step}-extended`)
          await mkdir(extended, { recursive: true })
          await writeFile(join(extended, 'environment.json'), JSON.stringify({ ...environment, binary, mode, workload, sizeMiB: size, workers, log, candidates, sourceID, targetID, seconds: 180 }, null, 2))
          const ok = await run.execute(`${step}-extended`, binary, ['-test.run=^TestConversionFlamegraph$', '-test.v', '-test.timeout=10m'], {
            env: { ...env, ELYSIA_FLAME_WORKLOAD: workload, ELYSIA_FLAME_MODE: mode, ELYSIA_FLAME_OUTPUT: extended, ELYSIA_FLAME_SECONDS: '180',
              ELYSIA_CONVERSION_HTTP_MIB: String(size), ELYSIA_CONVERSION_HTTP_WORKERS: String(workers), ELYSIA_CONVERSION_HTTP_LOG: log, ELYSIA_CONVERSION_HTTP_CANDIDATES: String(candidates), ELYSIA_FLAME_SOURCE: sourceID, ELYSIA_FLAME_TARGET: targetID, ELYSIA_FLAME_MATRIX_TOOLS: matrixTools },
          })
          if (!ok) { await run.finish('failed'); process.exitCode = 1; return }
          const extendedTop = execFileSync('go', ['tool', 'pprof', '-top', binary, join(extended, 'cpu.pprof')], { encoding: 'utf8', windowsHide: true })
          await writeFile(join(extended, 'cpu-top.txt'), extendedTop)
          const extendedMatch = extendedTop.match(/Total samples = ([\d.]+)(ms|s)/)
          const extendedSamples = extendedMatch ? Number(extendedMatch[1]) * (extendedMatch[2] === 'ms' ? 0.001 : 1) : 0
          Object.assign(run.report.steps.at(-1), { cpuSamplesSeconds: extendedSamples, sufficientCPUSamples: extendedSamples >= 10 })
          run.report.steps.at(-2).supersededBy = `${step}-extended`
          await run.save()
        }
      }
    }
  }
  const insufficient = run.report.steps.some(step => step.sufficientCPUSamples === false && !step.supersededBy)
  await run.finish(insufficient ? 'insufficient_samples' : run.report.steps.some(step => step.status === 'environment_blocked') ? 'partially_blocked' : 'passed')
  const selectionID = digest(selection.join(',')).slice(0, 12)
  await copyFile(join(output, 'report.json'), join(output, `report-${mode}-${selectionID}.json`))
  await archive()
}

async function render() {
  const { chromium } = await import('@playwright/test')
  const browser = await chromium.launch({ headless: true })
  let profiles = []
  try { profiles = JSON.parse(await readFile(join(output, 'flamegraphs.json'), 'utf8')) } catch (error) { if (error.code !== 'ENOENT') throw error }
  try {
    const directories = await readdir(output, { withFileTypes: true })
    for (const directory of directories.filter(entry => entry.isDirectory())) {
      if (option('filter', '') && !directory.name.includes(option('filter', ''))) continue
      profiles = profiles.filter(item => item.scenario !== directory.name)
      const folder = join(output, directory.name)
      const files = await readdir(folder)
      if (files.includes('alloc-after.pprof')) {
        execFileSync('go', ['tool', 'pprof', '-proto', '-base', join(folder, 'alloc-before.pprof'), '-output', join(folder, 'alloc.pprof'), binary, join(folder, 'alloc-after.pprof')], { windowsHide: true })
      }
      for (const file of (await readdir(folder)).filter(name => /^(cpu|alloc|heap-active|heap-drained|(?:image|nodes)-\d+-(?:decode|convert|encode|released))\.pprof$/.test(name))) {
        const sample = file.startsWith('cpu') ? 'cpu' : file.startsWith('alloc') ? 'alloc_space' : 'inuse_space'
        const profile = join(folder, file)
        const matchedBinary = directory.name === 'historical' ? join(folder, 'builtin.test.exe') : directory.name === 'phases' ? join(output, 'builtin.test.exe') : directory.name.startsWith('final-') ? join(output, 'server-matrix.test.exe') : binary
        const top = execFileSync('go', ['tool', 'pprof', '-top', `-sample_index=${sample}`, matchedBinary, profile], { encoding: 'utf8', windowsHide: true })
        await writeFile(join(folder, `${file}.top.txt`), top)
        let listing
        try {
          listing = execFileSync('go', ['tool', 'pprof', '-list=scanRaw|needsSemanticCompaction|encodeValueContainer|preserveMappedNative|convertOwnedRequest|validateRequestWireContract|equalNativeValues|equalValues', `-sample_index=${sample}`, matchedBinary, profile], { encoding: 'utf8', windowsHide: true, stdio: ['ignore', 'pipe', 'pipe'] })
        } catch (error) {
          if (error.status !== 2 || !String(error.stderr).includes('no matches found')) throw error
          listing = 'No sampled allocation remains at the selected conversion functions. This does not prove a zero live heap.\n'
        }
        await writeFile(join(folder, `${file}.list.txt`), listing)
        const child = spawn('go', ['tool', 'pprof', `-http=127.0.0.1:${await viewerPort()}`, '-no_browser', `-sample_index=${sample}`, matchedBinary, profile], { windowsHide: true, stdio: ['ignore', 'pipe', 'pipe'] })
        let text = ''
        const url = await new Promise((resolveURL, reject) => {
          const timeout = setTimeout(() => { child.kill(); reject(new Error(`pprof startup timeout: ${text}`)) }, 20000)
          const receive = chunk => { text += chunk; const match = text.match(/http:\/\/127\.0\.0\.1:\d+/); if (match) { clearTimeout(timeout); resolveURL(match[0]) } }
          child.stdout.on('data', receive); child.stderr.on('data', receive)
          child.on('error', error => { clearTimeout(timeout); reject(error) })
        })
        try {
          const page = await browser.newPage({ viewport: { width: 1800, height: 1000 }, deviceScaleFactor: 1 })
          for (const focus of ['', 'github.com/elysia-api/backend/protocol']) {
            await page.goto(`${url}/ui/flamegraph${focus ? `?f=${encodeURIComponent(focus)}` : ''}`, { waitUntil: 'networkidle' })
            await page.locator('#stack-chart').waitFor()
            const scope = directory.name === 'historical' || directory.name === 'phases' ? 'codec test harness' : 'whole gateway'
            const revision = directory.name === 'historical' ? 'a8341c2' : '31ea3a9'
            const duration = top.match(/Duration: ([^\n]+)/)?.[1] || 'heap snapshot / separate diagnostic run'
            const load = directory.name.includes('image31') ? '31 MiB base64 + tool history' : directory.name.includes('image2') ? '2 MiB base64 + tool history' : directory.name.includes('nodes') ? '1800 blocks / ~2 MiB' : directory.name.includes('events') ? '512 small deltas' : file.includes('nodes-2') ? '1800 blocks / ~2 MiB' : '2 MiB codec fixture'
            const label = `${revision} · ${directory.name}/${file} · ${load} · ${sample} · ${duration} · ${focus ? 'protocol focus (filtered denominator)' : scope}${directory.name === 'historical' ? ' · HISTORICAL SHORT SAMPLE' : ''}${directory.name === 'phases' ? ' · FORCED GC / RETAINED OBJECTS' : ''}`
            await page.evaluate(label => { const banner = document.createElement('div'); banner.textContent = label; banner.style.cssText = 'padding:12px;background:#eef;color:#112;font:18px sans-serif'; document.body.prepend(banner) }, label)
            const png = `${file}${focus ? '.protocol' : ''}.png`
            await page.screenshot({ path: join(folder, png), fullPage: true })
            profiles.push({ scenario: directory.name, sample, focus, profile: `${directory.name}/${file}`, binary: matchedBinary, png: `${directory.name}/${png}` })
          }
          await page.close()
        } finally {
          // The Go tool may launch a child; terminate only this viewer tree.
          if (platform() === 'win32') execFileSync('taskkill', ['/PID', String(child.pid), '/T', '/F'], { windowsHide: true, stdio: 'ignore' })
          else child.kill('SIGTERM')
        }
      }
    }
  } finally { await browser.close() }
  await writeFile(join(output, 'flamegraphs.json'), JSON.stringify(profiles, null, 2))
  await writeFile(join(output, 'index.html'), `<!doctype html><meta charset="utf-8"><title>Conversion flame graphs</title><style>body{font:16px sans-serif;margin:2em}img{max-width:100%}</style><h1>Conversion flame graphs</h1><p>Width = selected sample weight; horizontal position is not time. alloc_space is cumulative allocation, inuse_space is sampled live allocation sites, neither is a peak breakdown. Use the profile viewer command for interactive inspection.</p>${profiles.map(item => `<h2>${item.scenario} / ${item.sample} / ${item.focus || 'all'}</h2><a href="${item.png}"><img loading="lazy" src="${item.png}"></a>`).join('\n')}`)
}

async function history() {
  const source = resolve(process.env.LOCALAPPDATA, 'Temp/elysia-request-memory-dev72')
  const folder = join(output, 'historical'); await mkdir(folder, { recursive: true })
  for (const [from, to] of [['optimized-codecs.cpu.pprof', 'cpu.pprof'], ['optimized-codecs.alloc.pprof', 'alloc.pprof'], ['optimized-builtin.test.exe', 'builtin.test.exe']]) await copyFile(join(source, from), join(folder, to))
  await writeFile(join(folder, 'metadata.json'), JSON.stringify({ source, runtimeCommit: 'a8341c2', workload: '2 MiB codecs, short historical diagnostic; preparation included', cpuSamplesMS: 370, binarySHA256: digest(await readFile(join(folder, 'builtin.test.exe'))) }, null, 2))
}

const command = args[0]
if (command === 'run') await measure()
else if (command === 'render') await render()
else if (command === 'history') await history()
else if (command === 'view') {
  const profile = resolve(option('profile', ''))
  const folder = basename(dirname(profile))
  const localBinary = folder === 'historical' ? join(dirname(profile), 'builtin.test.exe') : folder === 'phases' ? join(output, 'builtin.test.exe') : folder.startsWith('final-') ? join(output, 'server-matrix.test.exe') : binary
  const selectedBinary = option('binary', localBinary)
  const child = spawn('go', ['tool', 'pprof', `-http=127.0.0.1:${await viewerPort()}`, '-no_browser', `-sample_index=${option('sample', 'cpu')}`, selectedBinary, profile], { stdio: 'inherit', windowsHide: true })
  await new Promise(resolve => child.once('close', resolve))
} else throw new Error('Usage: node scripts/profile-conversion.mjs history|run|render|view [--mode=cpu|memory|measure] [--cases=image2,image31,nodes,events] [--output=directory]')
