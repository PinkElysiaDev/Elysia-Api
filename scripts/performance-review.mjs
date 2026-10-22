import { execFileSync, spawn } from 'node:child_process'
import { readFile, writeFile, mkdir, readdir } from 'node:fs/promises'
import { resolve, join, dirname } from 'node:path'
import { fileURLToPath } from 'node:url'
import { createHash } from 'node:crypto'
import { cpus, totalmem } from 'node:os'

// A review run pins production trees. Only explicit *_test.go adapters are
// installed in disposable snapshots; neither dependency manifests nor the
// checked-out production implementation are rewritten.
const root = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const args = process.argv.slice(2)
const opt = (key, fallback) => args.find(a => a.startsWith(`--${key}=`))?.slice(key.length + 3) ?? fallback
const output = resolve(opt('output', join(root, '.tmp-dev/performance-review-2026-10-22')))
const revisions = { release: '8f50807c', merged: 'ba6f8563', normalized: '93509b13', current: '760b54af' }
const selectedVersions = opt('versions', 'release,current').split(',')
for (const name of selectedVersions) if (!revisions[name]) throw new Error(`Unknown version ${name}`)
const sha = value => createHash('sha256').update(value).digest('hex')
const git = (...argv) => execFileSync('git', argv, { cwd: root, encoding: 'utf8', maxBuffer: 128 << 20, windowsHide: true })
const show = (revision, path) => git('show', `${revision}:${path}`)
const save = async (path, data) => { await mkdir(dirname(path), { recursive: true }); await writeFile(path, typeof data === 'string' ? data : JSON.stringify(data, null, 2) + '\n') }
const env = { ...process.env, GOMAXPROCS: '4', GOGC: '100', GOMEMLIMIT: '2GiB', CGO_ENABLED: '0' }
for (const key of Object.keys(env)) if (/^ELYSIA_(FLAME_|CONVERSION_HTTP_|REVIEW_)/.test(key)) delete env[key]
async function run(name, command, argv, cwd, extra = {}) {
  const logPath = join(output, 'logs', `${name}.log`)
  const started = Date.now()
  console.log(`${new Date().toISOString()} ${name}`)
  const chunks = []
  const child = spawn(command, argv, { cwd, env: { ...env, ...extra }, windowsHide: true, stdio: ['ignore', 'pipe', 'pipe'] })
  child.stdout.on('data', b => chunks.push(b)); child.stderr.on('data', b => chunks.push(b))
  const code = await new Promise((resolve, reject) => { child.once('error', reject); child.once('close', resolve) })
  await save(logPath, Buffer.concat(chunks).toString())
  const result = { name, command, argv, cwd, environment: extra, code, elapsedMS: Date.now() - started, logPath }
  await save(join(output, 'steps', `${name}.json`), result)
  if (code !== 0) console.log(`FAILED ${name}: ${Buffer.concat(chunks).toString().slice(-2500)}`)
  return result
}
function memory() {
  const command = '$m = Get-CimInstance Win32_OperatingSystem; @{physicalGiB=$m.FreePhysicalMemory/1MB;commitGiB=$m.FreeVirtualMemory/1MB} | ConvertTo-Json -Compress'
  return JSON.parse(execFileSync('powershell', ['-NoProfile', '-Command', command], { encoding: 'utf8', windowsHide: true }))
}
async function prepare() {
  await mkdir(output, { recursive: true })
  await save(join(output, 'environment.json'), { date: '2026-10-22', created: new Date().toISOString(), baseline: revisions.release, target: revisions.current, go: execFileSync('go', ['version'], { encoding: 'utf8' }).trim(), cpu: cpus()[0].model, logicalCPUs: cpus().length, totalMemoryBytes: totalmem(), memory: memory(), settings: { GOMAXPROCS: 4, GOGC: 100, ordinaryMemoryLimit: '2GiB', largeMemoryLimit: '8GiB', pgo: 'off', cgo: 'off' } })
  const commits = git('log', '--reverse', '--format=%H%x09%P%x09%s', 'v1.6.2..760b54af').trim().split('\n').map(line => {
    const [hash, parents, subject] = line.split('\t')
    const files = git('diff-tree', '--root', '--no-commit-id', '--name-status', '-r', `${hash}^1`, hash).trim().split('\n').filter(Boolean)
    return { hash, parents: parents.split(' '), subject, merge: parents.includes(' '), files, review: 'inventory-only; individual findings recorded separately' }
  })
  await save(join(output, 'commits.json'), commits)
  await save(join(output, 'net-paths.tsv'), git('diff', '--name-status', 'v1.6.2', '760b54af'))
  await save(join(output, 'commit-graph.txt'), git('log', '--graph', '--oneline', '--date-order', 'v1.6.2..760b54af'))
  const historical = git('ls-tree', '-r', '--name-only', '6a16292d', 'docs').trim().split('\n').filter(p => /^docs\/backend-performance.*\.md$/.test(p) || /^docs\/artifacts\/performance[^/]*\/(summary\.json|README\.md)$/.test(p))
  for (const path of historical) await save(join(output, 'historical', path), show('6a16292d', path))
  for (const name of selectedVersions) {
    const directory = join(output, 'sources', name)
    await mkdir(directory, { recursive: true })
    if ((await readdir(directory)).length) throw new Error(`Source already exists: ${directory}`)
    const tar = join(output, `${name}.tar`)
    execFileSync('git', ['archive', '--format=tar', `--output=${tar}`, revisions[name], 'backend'], { cwd: root, windowsHide: true })
    execFileSync('tar', ['-xf', tar, '-C', directory], { windowsHide: true })
    const original = git('ls-tree', '-r', '--format=%(objectname)%x09%(path)', revisions[name], 'backend').trim()
    await save(join(output, 'identities', `${name}-git-tree.tsv`), original)
    // Use the historical Gin collector for historical HTTP frameworks. Common
    // client/provider fixture sources are identical across the four builds.
    let http = show(name === 'release' ? '5a8115e7' : name === 'current' ? '760b54af' : '93509b13', 'backend/server/conversion_http_performance_test.go')
    // Memory-only slow-provider observation is not part of formal timings.
    http = http.replaceAll('calls.Add(1)', 'calls.Add(1)\nif os.Getenv("ELYSIA_FLAME_MODE") == "memory" { time.Sleep(750 * time.Millisecond) }')
    if (name !== 'current') http = http.replaceAll('server.engine.ServeHTTP(w, r)', 'server.serveHTTP(w, r)')
    if (name !== 'release' && name !== 'current') http = http.replace('server := newTestServerWithStore(t, []config.ModelGroupConfig{group})', 'server := newTestServerWithStore(t, []config.ModelGroupConfig{group})\nserver.engine.Use(server.requestLogging())')
    const files = {
      'conversion_http_performance_test.go': http,
      'conversion_flamegraph_test.go': await readFile(join(root, 'backend/server/conversion_flamegraph_test.go'), 'utf8'),
      'conversion_flamegraph_matrix_test.go': await readFile(join(root, 'backend/server/conversion_flamegraph_matrix_test.go'), 'utf8'),
      'performance_review_test.go': await readFile(join(root, 'backend/server/performance_review_test.go'), 'utf8'),
      'conversion_memory_windows_test.go': show('760b54af', 'backend/server/conversion_memory_windows_test.go'),
      'conversion_memory_other_test.go': show('760b54af', 'backend/server/conversion_memory_other_test.go'),
    }
    if (name === 'release') files['review_legacy_writer_test.go'] = show('6a16292d', 'docs/artifacts/performance-v1.6.2-vs-5a8115e/baseline/baseline-adapter.go.txt')
    const manifest = []
    for (let [file, code] of Object.entries(files)) {
      if (name === 'release') code = code.replaceAll('sonic.ConfigStd.', 'json.').replace(/\s*"github.com\/bytedance\/sonic"\n/g, '\n')
      if (name === 'release' && code.includes('json.') && !code.includes('"encoding/json"')) code = code.replace('import (', 'import (\n"encoding/json"')
      await save(join(directory, 'backend/server', file), code)
      manifest.push({ file: `backend/server/${file}`, sha256: sha(code), purpose: 'test-only collector adaptation' })
    }
    await save(join(output, 'identities', `${name}-adapters.json`), manifest)
  }
}
async function build() {
  for (const name of selectedVersions) {
    const binary = join(output, 'binaries', `${name}.test.exe`)
    try { await readFile(join(output, 'identities', `${name}-binary.json`)); throw new Error(`Preserved binary already exists: ${name}; use a new output directory`) } catch (e) { if (e.code !== 'ENOENT') throw e }
    await mkdir(dirname(binary), { recursive: true })
    const flags = ['test', '-pgo=off', ...(name === 'release' || name === 'current' ? [] : ['-tags=sonic']), '-c', '-o', binary, './server']
    const result = await run(`build-${name}`, 'go', flags, join(output, 'sources', name, 'backend'))
    if (result.code) throw new Error(`Build failed: ${name}`)
    await save(join(output, 'identities', `${name}-binary.json`), { revision: git('rev-parse', revisions[name]).trim(), binary, sha256: sha(await readFile(binary)), build: result })
  }
}
const protocols = { chat: 'openai-chat-completions', anthropic: 'anthropic-messages', responses: 'openai-responses', gemini: 'google-generate-content' }
export const cases = {
  'chat-short': {}, 'anthropic-short': { source: 'anthropic', target: 'anthropic' }, 'responses-short': { source: 'responses', target: 'responses' }, 'gemini-short': { source: 'gemini', target: 'gemini' },
  'chat-anthropic': { target: 'anthropic' }, 'chat-responses': { target: 'responses' },
  prefix32k: { request: 'prefix32k' }, history256k: { request: 'history256k' }, tools32: { request: 'tools32' }, candidates4: { candidates: 4 },
  'reload-chat': { reload: true, requests: 1 }, burst4: { workers: 4 }, 'burst4-body': { workers: 4, log: 'body' },
  stream16: { stream: true, frames: 16 }, stream256: { stream: true, frames: 256 }, stream1024: { stream: true, frames: 1024 }, 'stream256-cross': { stream: true, frames: 256, target: 'anthropic' }, 'stream256-burst4': { stream: true, frames: 256, workers: 4 },
  image2: { workload: 'image', requests: 4 }, 'image2-media': { workload: 'image', requests: 4, log: 'media' },
  nodes: { workload: 'nodes', requests: 4 }, 'nodes-sse': { workload: 'nodes', requests: 4, stream: true },
  image8: { workload: 'image', size: 8, requests: 4 }, image16: { workload: 'image', size: 16, requests: 4 }, image31: { workload: 'image', size: 31, requests: 1 }, 'image31-sse': { workload: 'image', size: 31, requests: 1, stream: true },
  'image2-four': { workload: 'image', workers: 4, requests: 4 }, 'image8-four': { workload: 'image', size: 8, workers: 4, requests: 4 }, 'image16-four': { workload: 'image', size: 16, workers: 4, requests: 4 }, 'image31-four': { workload: 'image', size: 31, workers: 4, requests: 4 },
  'image31-four-sse': { workload: 'image', size: 31, workers: 4, requests: 4, stream: true },
  'image2-body': { workload: 'image', requests: 4, log: 'body' }, 'image31-body': { workload: 'image', size: 31, requests: 1, log: 'body' }, 'image31-media': { workload: 'image', size: 31, requests: 1, log: 'media' },
  candidates16: { candidates: 16 }, burst32: { workers: 32 }, 'large-frame': { stream: true, frames: 16, frameBytes: 4096 },
  logprobs: { stream: true, frames: 256, logprobs: true },
  logprobs1024: { stream: true, frames: 1024, logprobs: true, requests: 32 },
  'logprobs-responses': { source: 'responses', target: 'chat', stream: true, frames: 256, logprobs: true, requests: 32 },
  'logprobs-responses1024': { source: 'responses', target: 'chat', stream: true, frames: 1024, logprobs: true, requests: 32 },
  'responses-chat-stream256': { source: 'responses', target: 'chat', stream: true, frames: 256, requests: 32 },
  'responses-chat-stream1024': { source: 'responses', target: 'chat', stream: true, frames: 1024, requests: 32 },
  'anthropic-stream16': { source: 'anthropic', target: 'anthropic', stream: true, frames: 16 },
  'responses-stream16': { source: 'responses', target: 'responses', stream: true, frames: 16 },
  'gemini-stream16': { source: 'gemini', target: 'gemini', stream: true, frames: 16 },
  'image2-candidates4': { workload: 'image', requests: 4, candidates: 4 },
  'image2-candidates16': { workload: 'image', requests: 4, candidates: 16 },
  'image2-after': { workload: 'image', requests: 4, mapping: 'after' },
  'image2-after-sse': { workload: 'image', requests: 4, mapping: 'after', stream: true },
  'image2-json-rule': { workload: 'image', requests: 4, mapping: 'json-rule' },
  'image2-json-rule-sse': { workload: 'image', requests: 4, mapping: 'json-rule', stream: true },
}
async function collect(mode) {
  const selection = opt('cases', Object.keys(cases).slice(0, 20).join(',')).split(',')
  const pairs = Number(opt('pairs', mode === 'measure' ? 5 : mode === 'cpu' ? 3 : 1))
  const label = opt('label', mode)
  const preparation = opt('preparation', 'default')
  for (const key of selection) {
    if (!cases[key]) throw new Error(`Unknown case ${key}`)
    const c = { source: 'chat', target: 'chat', request: 'short', workload: 'small', size: 2, workers: 1, candidates: 1, log: 'off', frames: 16, frameBytes: 256, ...cases[key] }
    for (let pair = 0; pair < pairs; pair++) for (const version of pair % 2 ? [...selectedVersions].reverse() : selectedVersions) {
      const name = `${label}-${key}-${pair}-${version}`
      const directory = join(output, 'runs', name)
      try { await readFile(join(directory, 'result.json')); console.log(`skip existing ${name}`); continue } catch (e) { if (e.code !== 'ENOENT') throw e }
      await mkdir(directory, { recursive: true })
      if (c.size === 31 && c.workers === 4) {
        const available = memory()
        await save(join(directory, 'memory-precheck.json'), available)
        if (available.physicalGiB < 12 || available.commitGiB < 16) { await save(join(directory, 'result.json'), { name, status: 'environment-blocked', available }); continue }
      }
      const settings = {
        GOMEMLIMIT: c.size > 2 ? '8GiB' : '2GiB',
        ELYSIA_FLAME_MODE: mode, ELYSIA_FLAME_OUTPUT: directory, ELYSIA_FLAME_WORKLOAD: c.workload,
        ELYSIA_FLAME_SOURCE: c.workload === 'small' ? protocols[c.source] : '', ELYSIA_FLAME_TARGET: c.workload === 'small' ? protocols[c.target] : '',
        ELYSIA_FLAME_REQUEST: c.request, ELYSIA_FLAME_STREAM: c.stream ? 'sse' : 'json', ELYSIA_FLAME_FRAMES: String(c.frames), ELYSIA_FLAME_FRAME_BYTES: String(c.frameBytes),
        ELYSIA_FLAME_REQUESTS: String(c.workload === 'image' ? opt('image-requests', c.requests) : c.requests ?? Number(opt('requests', 256))), ELYSIA_FLAME_REPEATS: opt('repeats', '1'), ELYSIA_FLAME_WARMUP: c.size === 31 ? '1' : '4', ELYSIA_FLAME_SECONDS: opt('seconds', '60'),
        ELYSIA_FLAME_COLD: mode === 'measure' ? '1' : '', ELYSIA_FLAME_TIMELINE: mode === 'measure' ? '1' : '', ELYSIA_FLAME_RECOVERY_MS: mode === 'measure' ? opt('idle-ms', '1000') : '', ELYSIA_FLAME_VERIFY_EXACT: '1', ELYSIA_FLAME_RELOAD: c.reload ? '1' : '',
        ELYSIA_CONVERSION_HTTP_MIB: String(c.size), ELYSIA_CONVERSION_HTTP_WORKERS: String(c.workers), ELYSIA_CONVERSION_HTTP_CANDIDATES: String(c.candidates), ELYSIA_CONVERSION_HTTP_LOG: c.log,
        ELYSIA_CONVERSION_HTTP_PREPARATION: preparation === 'off' ? 'off' : '', ELYSIA_REVIEW_LOGPROBS: c.logprobs ? '1' : '', ELYSIA_REVIEW_TRACE: opt('trace', ''), ELYSIA_REVIEW_MAPPING: c.mapping || '',
      }
      const binary = join(output, 'binaries', `${version}${opt('binary-suffix', '')}.test.exe`)
      const result = await run(name, binary, ['-test.run=^TestConversionFlamegraph$', '-test.v', '-test.timeout=30m'], join(output, 'sources', version, 'backend'), settings)
      await save(join(directory, 'result.json'), { ...result, status: result.code === 0 ? 'passed' : 'failed', binary, version, revision: revisions[version], fixture: c, preparation, pair })
    }
  }
}
if (args[0] === 'prepare') await prepare()
else if (args[0] === 'build') await build()
else if (['measure', 'cpu', 'memory'].includes(args[0])) await collect(args[0])
else throw new Error('Usage: performance-review.mjs prepare|build|measure|cpu|memory [--versions=release,current] [--cases=...] [--output=NEW_DIRECTORY]')
