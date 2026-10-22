import { spawn, execFileSync } from 'node:child_process'
import { readFile, writeFile, mkdir, cp, readdir } from 'node:fs/promises'
import { join, resolve, dirname } from 'node:path'
import { fileURLToPath } from 'node:url'
import { createHash } from 'node:crypto'

// Supplemental binaries never replace a binary already used for measurement.
const repo = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const args = process.argv.slice(2)
const opt = (key, fallback) => args.find(a => a.startsWith(`--${key}=`))?.slice(key.length + 3) ?? fallback
const root = resolve(opt('output', '.tmp-dev/performance-review-2026-10-22'))
const versions = opt('versions', 'release,current').split(',')
const suffix = opt('suffix', 'supplement')
if (!/^[a-z0-9-]+$/.test(suffix)) throw new Error('Invalid binary suffix')
const reuseBase = args.includes('--reuse-base')
const sourceDirectory = version => join(root, reuseBase ? 'sources' : `${suffix}-sources`, version, 'backend')
const binaryPath = version => join(root, 'binaries', `${version}${reuseBase ? '' : '-' + suffix}.test.exe`)
const env = { ...process.env, GOMAXPROCS: '4', GOGC: '100', GOMEMLIMIT: '2GiB', CGO_ENABLED: '0' }
for (const key of Object.keys(env)) if (/^ELYSIA_(FLAME_|CONVERSION_HTTP_|REVIEW_)/.test(key)) delete env[key]
const save = async (file, value) => { await mkdir(dirname(file), { recursive: true }); await writeFile(file, typeof value === 'string' ? value : JSON.stringify(value, null, 2)) }
const sha = b => createHash('sha256').update(b).digest('hex')
async function run(name, command, argv, cwd, extra = {}) {
  const originalName = name
  for (let attempt = 1; ; attempt++) {
    try { await readFile(join(root, 'steps', name + '.json')); name = `${originalName}-attempt${attempt+1}` } catch (e) { if (e.code === 'ENOENT') break; throw e }
  }
  console.log(new Date().toISOString(), name)
  const started = Date.now(), chunks = []
  const p = spawn(command, argv, { cwd, env: { ...env, ...extra }, windowsHide: true, stdio: ['ignore', 'pipe', 'pipe'] })
  p.stdout.on('data', b => chunks.push(b)); p.stderr.on('data', b => chunks.push(b))
  const code = await new Promise((r, j) => { p.once('close', r); p.once('error', j) })
  const logPath = join(root, 'logs', name + '.log')
  await save(logPath, Buffer.concat(chunks).toString())
  const result = { name, command, argv, cwd, environment: extra, code, elapsedMS: Date.now() - started, logPath, status: code === 0 ? 'passed' : 'failed' }
  await save(join(root, 'steps', name + '.json'), result)
  if (code) console.log(Buffer.concat(chunks).toString().slice(-5000))
  return result
}
if (args[0] === 'build') {
  if (reuseBase) throw new Error('--reuse-base runs existing fixtures; it cannot overwrite the captured binary')
  for (const version of versions) {
    const directory = join(root, `${suffix}-sources`, version, 'backend')
    await mkdir(dirname(directory), { recursive: true })
    try { await readFile(join(root, 'identities', `${version}-${suffix}-binary.json`)); throw new Error(`Supplement already built: ${version}`) } catch (e) { if (e.code !== 'ENOENT') throw e }
    try { if ((await readdir(directory)).length) throw new Error(`Supplement source already exists: ${version}-${suffix}; preserve failures and choose a new suffix`) } catch (e) { if (e.code !== 'ENOENT') throw e }
    await cp(join(root, 'sources', version, 'backend'), directory, { recursive: true })
    const files = ['server/performance_review_test.go', 'server/performance_review_component_test.go', 'server/conversion_flamegraph_test.go', 'server/conversion_flamegraph_matrix_test.go', 'protocol/builtin/performance_review_test.go']
    const manifest = []
    for (const file of files) {
      let content = await readFile(join(repo, 'backend', file), 'utf8')
      if (version === 'release' && file === 'server/performance_review_component_test.go') {
        // The release does not depend on Cobra. Keep its original resolver and
        // omit the current-only Windows-hook experiment instead of adding deps.
        const marker = content.indexOf('// Current-only explanatory experiment.')
        if (marker < 0) throw new Error('Current-only benchmark adapter marker changed')
        content = content.slice(0, marker).replace(/\s*"github.com\/spf13\/cobra"\n/g, '\n')
        content = content.replace('import (', 'import (\n"time"')
          .replace('cache.get(key, "group")', 'cache.get(key, "group", time.Now())')
          .replace('cache.set(key, "group", "model")', 'cache.set(key, "group", "model", time.Now())')
      }
      if (version === 'release' && content.includes('sonic.ConfigStd.')) {
        content = content.replaceAll('sonic.ConfigStd.', 'json.').replace(/\s*"github.com\/bytedance\/sonic"\n/g, '\n')
        if (!content.includes('"encoding/json"')) content = content.replace('import (', 'import (\n"encoding/json"')
      }
      await writeFile(join(directory, file), content)
      manifest.push({ file, sha256: sha(content) })
    }
    const matrix = join(directory, 'server/conversion_flamegraph_matrix_test.go')
    let code = await readFile(matrix, 'utf8')
    if (!code.includes('return reviewToolProvider(t)')) code = code.replace('func flameMatrixProvider(t *testing.T, target *protocol.Compiled) http.Handler {', 'func flameMatrixProvider(t *testing.T, target *protocol.Compiled) http.Handler {\nif os.Getenv("ELYSIA_REVIEW_TOOL") == "1" { return reviewToolProvider(t) }')
    await writeFile(matrix, code)
    manifest.push({ file: 'server/conversion_flamegraph_matrix_test.go', sha256: sha(code) })
    const collector = join(directory, 'server/conversion_http_performance_test.go')
    let sampler = await readFile(collector, 'utf8')
    const hasGCTimeline = sampler.includes('GCCycles: memory.NumGC')
    if (!hasGCTimeline) {
      sampler = sampler.replace('type conversionMemorySample struct {', 'type conversionMemorySample struct {\nGCCycles uint32\nGCPauseNS uint64\nPreparationActive, PreparationQueued int')
      const original = 'timeline = append(timeline, conversionMemorySample{ElapsedMS: float64(time.Since(started)) / float64(time.Millisecond), Heap: memory.HeapAlloc, HeapInuse: memory.HeapInuse, HeapIdle: memory.HeapIdle, HeapReleased: memory.HeapReleased, RSS: working, Goroutines: runtime.NumGoroutine(), ActiveRequests: int(active.Load())})'
      const observePreparation = sampler.includes('var preparationStats') ? 'if preparationStats != nil { stats:=preparationStats(); point.PreparationActive,point.PreparationQueued=stats.Active,stats.Queued }' : ''
      if (!sampler.includes(original)) throw new Error('Timeline instrumentation site changed')
      sampler = sampler.replace(original, 'point := conversionMemorySample{ElapsedMS: float64(time.Since(started)) / float64(time.Millisecond), Heap: memory.HeapAlloc, HeapInuse: memory.HeapInuse, HeapIdle: memory.HeapIdle, HeapReleased: memory.HeapReleased, RSS: working, Goroutines: runtime.NumGoroutine(), ActiveRequests: int(active.Load()), GCCycles: memory.NumGC, GCPauseNS: memory.PauseTotalNs}\n' + observePreparation + '\ntimeline=append(timeline,point)')
    }
    await writeFile(collector, sampler)
    manifest.push({ file: 'server/conversion_http_performance_test.go', sha256: sha(sampler) })
    const groupsSource = await readFile(join(repo, 'backend/storage/group_query_test.go'), 'utf8')
    const groupBenchmark = 'package storage\nimport ("context";"fmt";"path/filepath";"testing")\n' + groupsSource.slice(groupsSource.indexOf('func BenchmarkListGroups'))
    // The release lacks this benchmark; current already contains the same one.
    if (version === 'release') {
      await writeFile(join(directory, 'storage/review_groups_test.go'), groupBenchmark)
      manifest.push({ file: 'storage/review_groups_test.go', sha256: sha(groupBenchmark) })
    }
    const binary = join(root, 'binaries', `${version}-${suffix}.test.exe`)
    const result = await run(`supplement-build-${version}`, 'go', ['test', '-pgo=off', ...(['merged', 'normalized'].includes(version) ? ['-tags=sonic'] : []), '-c', '-o', binary, './server'], directory)
    if (result.code) throw new Error(`Supplement build failed: ${version}`)
    await save(join(root, 'identities', `${version}-${suffix}-binary.json`), { binary, sha256: sha(await readFile(binary)), adapters: manifest, build: result })
  }
} else if (args[0] === 'tools' || args[0] === 'held') {
  const mode = args[0], streams = mode === 'held' ? ['sse'] : ['json', 'sse']
  for (const stream of streams) for (let pair = 0; pair < Number(opt('pairs', mode === 'held' ? 1 : 5)); pair++) for (const version of pair % 2 ? [...versions].reverse() : versions) {
    const name = `${opt('label', mode)}-${stream}-${pair}-${version}`, directory = join(root, 'runs', name)
    try { await readFile(join(directory, 'result.json')); continue } catch (e) { if (e.code !== 'ENOENT') throw e }
    await mkdir(directory, { recursive: true })
    const held = mode === 'held', binary = binaryPath(version)
    const settings = { ELYSIA_FLAME_OUTPUT: directory, ELYSIA_FLAME_WORKLOAD: held ? 'image' : 'small', ELYSIA_FLAME_STREAM: stream,
      ELYSIA_FLAME_SOURCE: held ? '' : 'openai-chat-completions', ELYSIA_FLAME_TARGET: held ? '' : 'openai-chat-completions',
      ELYSIA_REVIEW_TOOL: held ? '' : '1', ELYSIA_REVIEW_HELD: held ? '1' : '', ELYSIA_CONVERSION_HTTP_CANCEL: held ? '1' : '',
      ELYSIA_CONVERSION_HTTP_MIB: '2', ELYSIA_CONVERSION_HTTP_WORKERS: '4', ELYSIA_CONVERSION_HTTP_LOG: 'off' }
    const result = await run(name, binary, [`-test.run=^TestReview${held ? 'HeldProfiles' : 'ToolRoundtrip'}$`, '-test.v', '-test.timeout=5m'], sourceDirectory(version), settings)
    const identity = JSON.parse(await readFile(join(root, 'identities', `${version}-binary.json`)))
    await save(join(directory, 'result.json'), { ...result, version, revision: identity.revision, binary, pair })
  }
} else if (args[0] === 'bench') {
  for (let pair = 0; pair < Number(opt('pairs', 5)); pair++) for (const version of pair % 2 ? [...versions].reverse() : versions) {
    await run(`${opt('label', 'component')}-${pair}-${version}`, 'go', ['test', '-pgo=off', './protocol/builtin', '-run=^$', `-bench=${opt('protocol-bench', 'BenchmarkReview')}`, '-benchmem', `-benchtime=${opt('benchtime', '1s')}`, '-count=1'], sourceDirectory(version))
    await run(`${opt('label', 'storage')}-storage-${pair}-${version}`, 'go', ['test', '-pgo=off', './storage', '-run=^$', `-bench=${opt('storage-bench', 'BenchmarkListGroups')}`, '-benchmem', `-benchtime=${opt('benchtime', '1s')}`, '-count=1'], sourceDirectory(version))
    await run(`${opt('label', 'component')}-server-${pair}-${version}`, binaryPath(version), ['-test.run=^$', `-test.bench=${opt('server-bench', '^BenchmarkReview(Cache|CLI)')}`, '-test.benchmem', `-test.benchtime=${opt('benchtime', '1s')}`, '-test.count=1'], sourceDirectory(version))
  }
} else if (args[0] === 'scan') {
  for (const size of [2, 31]) for (let pair = 0; pair < Number(opt('pairs', 5)); pair++) for (const prefilter of pair % 2 ? [true, false] : [false, true]) {
    await run(`scan-${size}-${pair}-${prefilter}-current`, binaryPath('current'), ['-test.run=^$', `-test.bench=^BenchmarkReviewTurnStateScan$/^MiB=${size}$/^prefilter=${prefilter}$`, '-test.benchmem', `-test.benchtime=${opt('benchtime', '1s')}`, '-test.count=1'], sourceDirectory('current'))
  }
} else if (args[0] === 'cli-hook') {
  for (let pair = 0; pair < Number(opt('pairs', 5)); pair++) for (const disabled of pair % 2 ? [true, false] : [false, true]) {
    await run(`cli-hook-${pair}-${disabled}-current`, binaryPath('current'), ['-test.run=^$', `-test.bench=^BenchmarkReviewCLIWindowsHook$/^disabled=${disabled}$`, '-test.benchmem', `-test.benchtime=${opt('benchtime', '1s')}`, '-test.count=1'], sourceDirectory('current'))
  }
} else throw new Error('Usage: performance-review-supplement.mjs build|tools|held|bench|scan|cli-hook [--pairs=N]')
