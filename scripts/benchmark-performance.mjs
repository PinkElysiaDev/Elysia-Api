import { execFileSync, spawn } from 'node:child_process'
import { createHash } from 'node:crypto'
import { mkdir, readFile, writeFile, readdir } from 'node:fs/promises'
import { arch, cpus, platform, totalmem } from 'node:os'
import { dirname, join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const args = process.argv.slice(2)
const option = (name, fallback) => args.find(arg => arg.startsWith(`--${name}=`))?.slice(name.length + 3) ?? fallback
const root = resolve(option('source', resolve(dirname(fileURLToPath(import.meta.url)), '..')))
if (args.includes('--help')) {
  console.log('Usage: node scripts/benchmark-performance.mjs --output=NEW_DIRECTORY [--source=SOURCE_DIRECTORY] [--mode=all|micro|measure|profile] [--cases=chat-short,stream256,...] [--requests=32] [--repeats=3] [--idle-ms=1000] [--seconds=15] [--profiles=chat-short,stream256,image2]')
  process.exit(0)
}
const integer = (name, fallback, max) => {
  const value = Number(option(name, String(fallback)))
  if (!Number.isSafeInteger(value) || value < 1 || value > max) throw new Error(`Invalid --${name}`)
  return value
}
const mode = option('mode', 'all')
if (!['all', 'micro', 'measure', 'profile'].includes(mode)) throw new Error('Invalid mode')
const output = resolve(option('output', join(root, '.tmp-dev', `performance-${Date.now()}`)))
const requests = integer('requests', 32, 10000)
const repeats = integer('repeats', 3, 100)
const idleMS = integer('idle-ms', 1000, 60000)
const seconds = integer('seconds', 15, 180)
const protocols = { chat: 'openai-chat-completions', anthropic: 'anthropic-messages', responses: 'openai-responses', gemini: 'google-generate-content' }
const cases = {
  'chat-short': {},
  'anthropic-short': { source: 'anthropic', target: 'anthropic' },
  'responses-short': { source: 'responses', target: 'responses' },
  'gemini-short': { source: 'gemini', target: 'gemini' },
  'chat-anthropic': { target: 'anthropic' },
  'chat-responses': { target: 'responses' },
  prefix32k: { request: 'prefix32k' },
  history256k: { request: 'history256k' },
  tools32: { request: 'tools32' },
  candidates4: { candidates: 4 },
  'reload-chat': { reload: true, requests: 1 },
  burst4: { workers: 4 },
  'burst4-body': { workers: 4, log: 'body' },
  stream16: { stream: true, frames: 16 },
  stream256: { stream: true, frames: 256 },
  stream1024: { stream: true, frames: 1024 },
  'stream256-cross': { stream: true, frames: 256, target: 'anthropic' },
  'stream256-burst4': { stream: true, frames: 256, workers: 4 },
  image2: { image: true, requests: 4 },
  'image2-media': { image: true, log: 'media', requests: 4 },
}
const select = (name, fallback) => option(name, fallback).split(',').map(key => {
  if (!Object.hasOwn(cases, key)) throw new Error(`Unknown case ${key}`)
  return key
})
const selected = select('cases', Object.keys(cases).join(','))
const profiles = select('profiles', 'chat-short,stream256,image2')
const env = { ...process.env, GOMAXPROCS: process.env.GOMAXPROCS || '4', GOGC: process.env.GOGC || '100', GOMEMLIMIT: process.env.GOMEMLIMIT || '2GiB' }
// Do not accidentally inherit a different fixture, cancellation mode or output path.
for (const key of Object.keys(env)) if (/^ELYSIA_(FLAME_|CONVERSION_HTTP_|LOG_MEMORY_BASELINE)/.test(key)) delete env[key]
const sha = bytes => createHash('sha256').update(bytes).digest('hex')
const text = (command, argv, cwd = root) => execFileSync(command, argv, { cwd, env, encoding: 'utf8', maxBuffer: 64 << 20 }).trim()
await mkdir(output, { recursive: true })
if ((await readdir(output)).length) throw new Error('Use a new, empty output directory to preserve matching evidence')
const binary = join(output, platform() === 'win32' ? 'server.test.exe' : 'server.test')
const module = await readFile(join(root, 'backend', 'go.mod'), 'utf8')
const framework = module.includes('github.com/cloudwego/hertz ') ? 'hertz' : module.includes('github.com/gin-gonic/gin ') ? 'gin' : null
if (!framework) throw new Error('Unsupported HTTP framework in source go.mod')
const tags = framework === 'gin' ? ['sonic'] : []
const buildFlags = ['-pgo=off', ...tags.map(tag => `-tags=${tag}`)]
const report = {
  status: 'running', created: new Date().toISOString(),
  commit: text('git', ['rev-parse', 'HEAD']), worktree: text('git', ['status', '--porcelain']),
  go: text('go', ['version']), goEnvironment: JSON.parse(text('go', ['env', '-json', 'GOOS', 'GOARCH', 'GOAMD64', 'CGO_ENABLED', 'GOEXPERIMENT'])),
  cpu: cpus()[0]?.model, logicalCPUs: cpus().length, totalMemoryBytes: totalmem(), platform: platform(), architecture: arch(),
  runtime: { GOMAXPROCS: env.GOMAXPROCS, GOGC: env.GOGC, GOMEMLIMIT: env.GOMEMLIMIT, pgo: 'off', tags, httpFramework: framework, transport: framework === 'hertz' ? 'standard' : 'net/http' },
  settings: { mode, selected, profiles, requests, repeats, idleMS, seconds, sampleIntervalMS: 20 }, steps: [], results: [],
}
await writeFile(join(output, 'source.patch'), text('git', ['diff', '--binary', 'HEAD']) + '\n')
// Record untracked harness sources too; git diff alone does not include new files.
await writeFile(join(output, 'runner.mjs'), await readFile(fileURLToPath(import.meta.url)))
// A dirty-tree benchmark must retain actual inputs, including untracked Go
// sources. The snapshot can be rebuilt independently of the original worktree.
const sourceFiles = []
const paths = execFileSync('git', ['ls-files', '-z', '--cached', '--others', '--exclude-standard', '--', 'backend'], { cwd: root, env, encoding: 'utf8' }).split('\0').filter(Boolean)
for (const path of [...new Set(paths)].sort()) {
  let bytes
  try { bytes = await readFile(join(root, path)) } catch (error) { if (error.code === 'ENOENT') continue; throw error }
  const destination = join(output, 'source', path)
  await mkdir(dirname(destination), { recursive: true })
  await writeFile(destination, bytes)
  sourceFiles.push({ path, sha256: sha(bytes) })
}
report.sourceSHA256 = sha(JSON.stringify(sourceFiles))
await writeFile(join(output, 'source-manifest.json'), JSON.stringify(sourceFiles, null, 2) + '\n')
const save = () => writeFile(join(output, 'report.json'), JSON.stringify(report, null, 2) + '\n')
await save()

async function run(name, command, argv, overrides = {}) {
  console.log(`[${name}] ${command} ${argv.join(' ')}`)
  const started = Date.now()
  const chunks = []
  const child = spawn(command, argv, { cwd: join(root, 'backend'), env: { ...env, ...overrides }, stdio: ['ignore', 'pipe', 'pipe'] })
  child.stdout.on('data', data => chunks.push(data))
  child.stderr.on('data', data => chunks.push(data))
  const code = await new Promise((resolve, reject) => { child.once('error', reject); child.once('close', resolve) })
  const log = Buffer.concat(chunks)
  await writeFile(join(output, `${name}.log`), log)
  report.steps.push({ name, command, argv, environment: overrides, code, elapsedMS: Date.now() - started })
  await save()
  if (code !== 0) throw new Error(`${name} failed; see ${join(output, `${name}.log`)}\n${log.toString().slice(-6000)}`)
}

const median = values => {
  const sorted = [...values].sort((a, b) => a - b)
  const middle = Math.floor(sorted.length / 2)
  return sorted.length % 2 ? sorted[middle] : (sorted[middle - 1] + sorted[middle]) / 2
}

try {
  if (mode === 'all' || mode === 'micro') {
    await run('micro', 'go', ['test', ...buildFlags, './protocol', './protocol/builtin', '-run=^$', '-bench=^(BenchmarkRequestMemoryPipeline|BenchmarkRequestWorkloads|BenchmarkStreamTextDecode|BenchmarkTypedResourceCheck|BenchmarkRuntimeSchema)$', '-benchmem', '-benchtime=300ms', '-count=5', '-timeout=10m'])
  }
  if (mode !== 'micro') {
    await run('build', 'go', ['test', ...buildFlags, '-c', '-o', binary, './server'])
    report.binarySHA256 = sha(await readFile(binary))
    await save()
  }
  const work = []
  if (mode === 'all' || mode === 'measure') for (const name of selected) work.push([name, 'measure'])
  if (mode === 'all' || mode === 'profile') for (const name of profiles) for (const type of ['memory', 'cpu']) work.push([name, type])
  for (const [name, type] of work) {
    const c = { source: 'chat', target: 'chat', request: 'short', workers: 1, candidates: 1, log: 'off', frames: 16, ...cases[name] }
    const directory = join(output, `${type}-${name}`)
    await mkdir(directory)
    const settings = {
      ELYSIA_FLAME_WORKLOAD: c.image ? 'image' : 'small', ELYSIA_FLAME_MODE: type, ELYSIA_FLAME_OUTPUT: directory,
      ELYSIA_FLAME_SOURCE: c.image ? '' : protocols[c.source], ELYSIA_FLAME_TARGET: c.image ? '' : protocols[c.target],
      ELYSIA_FLAME_REQUEST: c.request, ELYSIA_FLAME_STREAM: c.stream ? 'sse' : 'json',
      ELYSIA_FLAME_FRAMES: String(c.frames), ELYSIA_FLAME_FRAME_BYTES: '256',
      ELYSIA_FLAME_REQUESTS: String(c.requests ?? requests), ELYSIA_FLAME_REPEATS: String(repeats), ELYSIA_FLAME_WARMUP: '4',
      ELYSIA_FLAME_SECONDS: String(seconds), ELYSIA_FLAME_COLD: '1', ELYSIA_FLAME_TIMELINE: type === 'measure' ? '1' : '',
      ELYSIA_FLAME_RELOAD: c.reload ? '1' : '',
      ELYSIA_FLAME_RECOVERY_MS: type === 'measure' ? String(idleMS) : '', ELYSIA_FLAME_VERIFY_EXACT: '1',
      ELYSIA_CONVERSION_HTTP_MIB: '2', ELYSIA_CONVERSION_HTTP_WORKERS: String(c.workers),
      ELYSIA_CONVERSION_HTTP_CANDIDATES: String(c.candidates), ELYSIA_CONVERSION_HTTP_LOG: c.log,
    }
    await run(`${type}-${name}`, binary, ['-test.run=^TestConversionFlamegraph$', '-test.v', '-test.timeout=20m'], settings)
    const measurements = JSON.parse(await readFile(join(directory, 'measurements.json')))
    if (type === 'measure') {
      const samples = measurements.filter(m => m.Phase !== 'first-request')
      const result = {
        name, fixture: c, repeats: samples.length, requestsPerRepeat: samples[0].Requests,
        allocatedBytesPerRequest: median(samples.map(m => m.Metrics.Allocated / m.Requests)),
        allocationsPerRequest: median(samples.map(m => m.Metrics.Allocations / m.Requests)),
        cpuMSPerRequest: median(samples.map(m => m.Metrics.CPUTimeNS / m.Requests / 1e6)),
        requestsPerSecond: median(samples.map(m => m.Requests * 1000 / m.ElapsedMS)),
        latencyP50P95P99MS: [0, 1, 2].map(i => median(samples.map(m => m.LatencyP50P95P99NS[i] / 1e6))),
        firstByteP50P95P99MS: [0, 1, 2].map(i => median(samples.map(m => m.FirstByteP50P95P99NS[i] / 1e6))),
        firstFrameP50P95P99MS: [0, 1, 2].map(i => median(samples.map(m => m.FirstFrameP50P95P99NS[i] / 1e6))),
        peakHeapBytes: Math.max(...samples.map(m => m.Metrics.PeakHeap)), peakRSSBytes: Math.max(...samples.map(m => m.Metrics.PeakWorkingSet)),
        gcCycles: samples.map(m => m.Metrics.GCCycles), gcCPUSeconds: samples.map(m => m.Metrics.GCCPUSeconds), gcPauseNS: samples.map(m => m.Metrics.GCPauseNS),
        recoveryHeapBytes: samples.map(m => m.Recovery.at(-1)?.Metrics.Heap),
        recoveryRSSBytes: samples.map(m => m.Recovery.at(-1)?.Metrics.WorkingSet),
        reloadMS: samples.map(m => m.ReloadMS ?? 0),
        firstRequest: measurements.find(m => m.Phase === 'first-request'),
      }
      report.results.push(result)
    } else {
      const profiles = type === 'cpu' ? [['cpu', 'cpu.pprof']] : [['alloc_space', 'alloc-after.pprof'], ['alloc_objects', 'alloc-after.pprof'], ['inuse_space', 'heap-drained.pprof']]
      for (const [sample, file] of profiles) {
        const argv = ['tool', 'pprof', '-top', '-nodecount=30', `-sample_index=${sample}`]
        if (sample.startsWith('alloc_')) argv.push('-base', join(directory, 'alloc-before.pprof'))
        argv.push(binary, join(directory, file))
        await writeFile(join(directory, `${sample}-top.txt`), text('go', argv))
      }
    }
    await save()
  }
  if (mode === 'all') {
    await run('cancellation', binary, ['-test.run=^TestConversionHTTPCancellationIsolated$', '-test.v', '-test.timeout=10m'], {
      ELYSIA_CONVERSION_HTTP_CANCEL: '1', ELYSIA_CONVERSION_HTTP_MIB: '2', ELYSIA_CONVERSION_HTTP_WORKERS: '4', ELYSIA_CONVERSION_HTTP_LOG: 'off', ELYSIA_CONVERSION_HTTP_OUTPUT: output,
    })
  }
  report.status = 'passed'
} catch (error) {
  report.status = 'failed'
  report.error = String(error.stack || error)
  process.exitCode = 1
  console.error(error)
} finally {
  report.finished = new Date().toISOString()
  await save()
  const rows = report.results.map(r => `| ${r.name} | ${(r.allocatedBytesPerRequest / 1024).toFixed(1)} | ${r.allocationsPerRequest.toFixed(0)} | ${r.cpuMSPerRequest.toFixed(3)} | ${r.latencyP50P95P99MS.map(n => n.toFixed(2)).join(' / ')} | ${(r.peakHeapBytes / 2 ** 20).toFixed(1)} | ${r.peakRSSBytes ? (r.peakRSSBytes / 2 ** 20).toFixed(1) : 'unavailable'} |`)
  await writeFile(join(output, 'summary.md'), `# Gateway allocation baseline\n\nStatus: ${report.status}. Runtime source: ${report.commit}; ${report.runtime.httpFramework}; source SHA256: ${report.sourceSHA256}. See report.json for environment, commands and fixture settings.\n\nMedian of per-repeat metrics; peak memory is the maximum sampled absolute value across repeats.\n\n| Case | KiB/request | allocations/request | CPU ms/request | latency P50/P95/P99 ms | peak heap MiB | peak RSS MiB |\n| --- | ---: | ---: | ---: | --- | ---: | ---: |\n${rows.join('\n')}\n`)
  // Checksums cover evidence and logs, excluding the binary (recorded above)
  // and this manifest itself. All scenarios run serially.
  const files = []
  async function visit(directory) {
    for (const entry of await readdir(directory, { withFileTypes: true })) {
      const file = join(directory, entry.name)
      if (entry.isDirectory()) await visit(file)
      else if (file !== binary && entry.name !== 'manifest.json') files.push({ path: file.slice(output.length + 1), sha256: sha(await readFile(file)) })
    }
  }
  await visit(output)
  await writeFile(join(output, 'manifest.json'), JSON.stringify(files, null, 2) + '\n')
  console.log(`Evidence: ${output}`)
}
