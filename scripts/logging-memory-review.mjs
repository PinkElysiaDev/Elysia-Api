import { execFileSync, spawn } from 'node:child_process'
import { createHash } from 'node:crypto'
import { createWriteStream } from 'node:fs'
import { mkdir, readFile, writeFile } from 'node:fs/promises'
import { cpus, freemem, platform, totalmem } from 'node:os'
import { dirname, join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

// Both binaries must already contain the same test collectors. This driver
// never builds, edits source snapshots, changes admission, or runs in parallel.
const root = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const args = process.argv.slice(2)
const option = (name, fallback = '') => args.find(value => value.startsWith(`--${name}=`))?.slice(name.length + 3) ?? fallback
const mode = args[0]
if (!['measure', 'memory', 'cpu', 'finalize'].includes(mode)) {
  throw new Error('Usage: logging-memory-review.mjs measure|memory|cpu|finalize --baseline-binary=... --baseline-cwd=... --baseline-revision=b9968171 --current-binary=... --current-cwd=... --current-revision=... --output=... [--cases=image2-body-png,image31-media-png] [--pairs=5]')
}
const required = name => {
  const value = option(name)
  if (!value) throw new Error(`Missing --${name}`)
  return value
}
const integer = (name, fallback, minimum, maximum) => {
  const value = Number(option(name, String(fallback)))
  if (!Number.isSafeInteger(value) || value < minimum || value > maximum) throw new Error(`Invalid --${name}`)
  return value
}
const output = resolve(required('output'))
const pairs = integer('pairs', 5, 1, 100)
const pairOffset = integer('pair-offset', 0, 0, 10000)
if (option('cancel') === '1' && mode !== 'finalize') throw new Error('--cancel=1 is only valid for the Finalize ownership diagnostic')
const sha256 = value => createHash('sha256').update(value).digest('hex')
const writeJSON = async (path, value) => {
  await mkdir(dirname(path), { recursive: true })
  await writeFile(path, JSON.stringify(value, null, 2) + '\n')
}
const readJSON = async path => {
  try { return JSON.parse(await readFile(path, 'utf8')) } catch (error) {
    if (error.code !== 'ENOENT') throw error
    return null
  }
}
const collectorFiles = [
  'server/conversion_http_performance_test.go', 'server/conversion_flamegraph_test.go',
  'server/conversion_flamegraph_matrix_test.go', 'server/logging_memory_review_test.go',
  'server/performance_review_test.go',
]
const identities = {}
for (const version of ['baseline', 'current']) {
  const binary = resolve(required(`${version}-binary`))
  const cwd = resolve(required(`${version}-cwd`))
  const revision = execFileSync('git', ['rev-parse', `${required(`${version}-revision`)}^{commit}`], { cwd: root, encoding: 'utf8', windowsHide: true }).trim()
  const collectors = []
  for (const path of collectorFiles) collectors.push({ path, sha256: sha256(await readFile(join(cwd, path))) })
  identities[version] = {
    binary, cwd, revision, sha256: sha256(await readFile(binary)), collectors,
    revisionEvidence: 'caller-selected pinned source; retain the corresponding build command alongside this binary identity',
    build: execFileSync('go', ['version', '-m', binary], { encoding: 'utf8', windowsHide: true }).trim(),
  }
}
for (let index = 0; index < collectorFiles.length; index++) {
  if (identities.baseline.collectors[index].sha256 !== identities.current.collectors[index].sha256) {
    throw new Error(`Collectors differ between binaries' source directories: ${collectorFiles[index]}`)
  }
}

function memoryPrecheck() {
  if (platform() !== 'win32') return { physicalGiB: freemem() / 2 ** 30, commitGiB: null, method: 'available physical memory only; four-large-request gate requires committed-memory evidence' }
  const command = '$os = Get-CimInstance Win32_OperatingSystem; $memory = Get-CimInstance Win32_PerfRawData_PerfOS_Memory; @{physicalGiB=($os.FreePhysicalMemory * 1024 / 1GB);commitGiB=(($memory.CommitLimit - $memory.CommittedBytes) / 1GB);commitLimitBytes=$memory.CommitLimit;committedBytes=$memory.CommittedBytes;method="Win32_OperatingSystem + Win32_PerfRawData_PerfOS_Memory"} | ConvertTo-Json -Compress'
  return JSON.parse(execFileSync('powershell', ['-NoProfile', '-Command', command], { encoding: 'utf8', windowsHide: true }))
}

const environment = { ...process.env, GOMAXPROCS: '4', GOGC: '100', CGO_ENABLED: '0' }
for (const name of Object.keys(environment)) {
  if (/^ELYSIA_(FLAME_|CONVERSION_HTTP_|REVIEW_|LOGGING_|HTTP_FINALIZE_)/.test(name)) delete environment[name]
}
const manifestPath = join(output, 'logging-review-manifest.json')
const previous = await readJSON(manifestPath)
if (previous && JSON.stringify(previous.identities) !== JSON.stringify(identities)) throw new Error('Output directory already belongs to different binaries or collector sources')
if (!previous) await writeJSON(manifestPath, {
  created: new Date().toISOString(), identities,
  machine: { platform: platform(), cpu: cpus()[0]?.model, logicalCPUs: cpus().length, totalMemoryBytes: totalmem(), memory: memoryPrecheck() },
  settings: { GOMAXPROCS: 4, GOGC: 100, smallMemoryLimit: '2GiB', largeMemoryLimit: '8GiB', pgo: 'must be off in supplied build evidence', preparation: 'production default; no fixture override' },
  scope: 'measure has no profiler/forced GC; memory and Finalize are separate diagnostics; complete gateway output is validated by shared test collectors',
  imageFixture: { png: 'valid deterministic PNG, noisy RGBA and private ancillary padding, exact requested base64 length', generator: 'math/rand/v2.PCG', seeds: ['0x4c6f674d656d6f72', '0x7952657669657731'], repeat: 'legacy repeated A base64 control; not representative of compressed images' },
})

const cases = {
  'small-off': { workload: 'small', log: 'off' },
  'small-body': { workload: 'small', log: 'body' },
  'small-media': { workload: 'small', log: 'media' },
  'small-chat-off': { workload: 'small', log: 'off', source: 'openai-chat-completions', target: 'openai-chat-completions', requests: 4096 },
  'small-chat-body': { workload: 'small', log: 'body', source: 'openai-chat-completions', target: 'openai-chat-completions', requests: 4096 },
  'stream1024-off': { workload: 'small', log: 'off', source: 'openai-responses', target: 'openai-chat-completions', stream: true, frames: 1024, requests: 32 },
  'stream1024-body': { workload: 'small', log: 'body', source: 'openai-responses', target: 'openai-chat-completions', stream: true, frames: 1024, requests: 32 },
  'stream256-body': { workload: 'small', log: 'body', stream: true, frames: 256 },
  'response4-body': { workload: 'small', log: 'body', responseBytes: 4 << 20, requests: 4 },
  'frame4-body': { workload: 'small', log: 'body', stream: true, frames: 1, frameBytes: 4 << 20, requests: 4 },
}
for (const size of [2, 8, 16, 31]) for (const log of ['off', 'body', 'media']) for (const image of ['repeat', 'png']) for (const workers of [1, 4]) {
  const name = `image${size}-${log}${image === 'png' ? '-png' : ''}${workers === 4 ? '-four' : ''}`
  cases[name] = { workload: 'image', size, log, image, workers, requests: size === 31 ? workers : Math.max(4, workers) }
}
const selection = option('cases', 'image2-body-png,image2-media-png,image31-body-png,image31-media-png').split(',')
for (const name of selection) if (!cases[name]) throw new Error(`Unknown case ${name}`)

async function execute(binary, argv, cwd, settings, directory) {
  const logPath = join(directory, 'process.log')
  const log = createWriteStream(logPath, { flags: 'wx' })
  const started = new Date().toISOString()
  const start = performance.now()
  const command = { binary, argv, cwd, environment: { GOMAXPROCS: environment.GOMAXPROCS, GOGC: environment.GOGC, CGO_ENABLED: environment.CGO_ENABLED, ...settings } }
  await writeJSON(join(directory, 'command.json'), command)
  let exitCode = null
  let signal = null
  let error = null
  try {
    const child = spawn(binary, argv, { cwd, env: { ...environment, ...settings }, windowsHide: true, stdio: ['ignore', 'pipe', 'pipe'] })
    child.stdout.pipe(log, { end: false })
    child.stderr.pipe(log, { end: false })
    await new Promise((resolve, reject) => {
      child.once('error', reject)
      child.once('close', (code, killedBy) => { exitCode = code; signal = killedBy; resolve() })
    })
  } catch (failure) { error = String(failure) }
  await new Promise((resolve, reject) => { log.once('error', reject); log.end(resolve) })
  return { command, logPath, started, ended: new Date().toISOString(), elapsedMS: performance.now() - start, exitCode, signal, error }
}

const results = []
for (const name of selection) {
  const fixture = { workload: 'small', source: 'anthropic-messages', target: 'anthropic-messages', size: 2, workers: 1, image: 'repeat', stream: false, frames: 16, frameBytes: 256, requests: integer('requests', 256, 1, 10000), ...cases[name] }
  if (mode === 'finalize' && fixture.workload !== 'image') throw new Error('Finalize ownership diagnostic requires an image gateway fixture')
  for (let pair = pairOffset; pair < pairOffset + pairs; pair++) {
    const order = pair % 2 === 0 ? ['baseline', 'current'] : ['current', 'baseline']
    for (const version of order) {
      const runName = `${mode}-${name}-${pair}-${version}${option('cancel') === '1' ? '-cancel' : ''}`
      const directory = join(output, 'runs', runName)
      const resultPath = join(directory, 'result.json')
      const existing = await readJSON(resultPath)
      if (existing) { results.push(existing); console.log(`Preserved ${runName}: ${existing.status}`); continue }
      await mkdir(directory, { recursive: true })
      const memory = memoryPrecheck()
      await writeJSON(join(directory, 'memory-precheck.json'), memory)
      const identity = identities[version]
      const base = { name: runName, mode, fixture, pair, order, version, revision: identity.revision, binarySHA256: identity.sha256, memory }
      if (fixture.size === 31 && fixture.workers === 4 && !(memory.physicalGiB >= 12 && memory.commitGiB >= 16)) {
        const blocked = { ...base, status: 'environment-blocked', reason: '31 MiB / four clients requires at least 12 GiB free physical memory and 16 GiB remaining commit; default preparation admission remains active' }
        await writeJSON(resultPath, blocked)
        results.push(blocked)
        continue
      }
      const settings = {
        GOMEMLIMIT: fixture.size > 2 ? '8GiB' : '2GiB',
        ELYSIA_FLAME_MODE: mode === 'finalize' ? 'memory' : mode, ELYSIA_FLAME_OUTPUT: directory,
        ELYSIA_FLAME_WORKLOAD: fixture.workload, ELYSIA_FLAME_SOURCE: fixture.workload === 'small' ? fixture.source : '', ELYSIA_FLAME_TARGET: fixture.workload === 'small' ? fixture.target : '',
        ELYSIA_FLAME_REQUEST: 'short', ELYSIA_FLAME_STREAM: mode === 'finalize' || fixture.stream ? 'sse' : 'json',
        ELYSIA_FLAME_FRAMES: String(fixture.frames), ELYSIA_FLAME_FRAME_BYTES: String(fixture.frameBytes),
        ELYSIA_FLAME_REQUESTS: String(fixture.requests), ELYSIA_FLAME_REPEATS: '1', ELYSIA_FLAME_WARMUP: fixture.size === 31 ? '1' : '4',
        ELYSIA_FLAME_SECONDS: String(integer('seconds', 60, 1, 180)), ELYSIA_FLAME_VERIFY_EXACT: '1',
        ELYSIA_FLAME_TIMELINE: '1', ELYSIA_FLAME_RECOVERY_MS: mode === 'measure' ? option('idle-ms', '1000') : '',
        ELYSIA_CONVERSION_HTTP_MIB: String(fixture.size), ELYSIA_CONVERSION_HTTP_WORKERS: String(fixture.workers), ELYSIA_CONVERSION_HTTP_CANDIDATES: '1', ELYSIA_CONVERSION_HTTP_LOG: fixture.log,
        ELYSIA_LOGGING_IMAGE: fixture.image, ELYSIA_LOGGING_RESPONSE_BYTES: fixture.responseBytes ? String(fixture.responseBytes) : '',
        ELYSIA_LOGGING_FINALIZE: mode === 'finalize' ? '1' : '', ELYSIA_LOGGING_CANCEL: option('cancel') === '1' ? '1' : '', ELYSIA_LOGGING_BATCHES: String(integer('batches', 5, 1, 20)),
      }
      const test = mode === 'finalize' ? 'TestLoggingReviewGatewayFinalize' : 'TestConversionFlamegraph'
      console.log(`${new Date().toISOString()} ${runName}`)
      const execution = await execute(identity.binary, [`-test.run=^${test}$`, '-test.v', '-test.timeout=40m'], identity.cwd, settings, directory)
      const result = { ...base, ...execution, status: execution.exitCode === 0 ? 'passed' : 'failed' }
      await writeJSON(resultPath, result)
      results.push(result)
    }
  }
}
await writeJSON(join(output, `${mode}-runs-${pairOffset}-${pairs}${option('cancel') === '1' ? '-cancel' : ''}.json`), results)
if (results.some(result => result.status === 'failed')) process.exitCode = 1
