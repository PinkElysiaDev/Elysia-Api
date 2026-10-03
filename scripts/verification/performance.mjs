import { join } from 'node:path'
import { readFile, writeFile } from 'node:fs/promises'
import { snapshotRevision } from './snapshot.mjs'

const harnessFiles = [
  'backend/protocol/builtin/workload_benchmark_test.go',
  'backend/server/protocol_load_test.go',
  'backend/server/protocol_load_windows_test.go',
  'backend/server/protocol_load_unix_test.go',
  'backend/server/protocol_verification_fixture_test.go',
  'backend/server/relay_integration_test.go',
]

/** Measures only local deterministic workloads; no provider credential is read. */
export async function verifyPerformance(run, root) {
  const revision = process.argv.find(arg => arg.startsWith('--revision='))?.slice(11)
  const isLegacy = process.argv.includes('--legacy-c01')
  if (isLegacy && !revision) throw new Error('--legacy-c01 requires --revision=d77aac7')
  const snapshot = revision ? await snapshotRevision(root, run.directory, revision, isLegacy ? [] : harnessFiles) : undefined
  if (snapshot) { run.report.snapshot = snapshot; await run.save() }
  const cwd = join(snapshot?.checkout || root, 'backend')
  const compiler = await readFile(join(cwd, 'protocol/definition.go'), 'utf8').catch(error => { if (isLegacy && error.code === 'ENOENT') return ''; throw error })
  run.report.measuredCompilerVersion = compiler.match(/CompilerVersion\s*=\s*"([^"]+)"/)?.[1] || 'legacy-c01'
  run.report.measurement = { microCount: 10, benchtime: '300ms', profiled: false, requests: +(process.env.ELYSIA_LOAD_REQUESTS || 128), repeats: +(process.env.ELYSIA_LOAD_REPEATS || 3), warmupRequestsPerScenario: 32, concurrency: [1, 8, 32], gomaxprocs: process.env.GOMAXPROCS || 'Go default', filter: process.env.ELYSIA_LOAD_FILTER || 'all', cpuScope: 'gateway + loopback load generator + deterministic upstream', firstFrame: 'complete SSE frame or complete non-streaming body' }
  await run.save()
  const pattern = isLegacy ? 'BenchmarkProtocolConversion/claude$' : 'BenchmarkCacheConversion$|BenchmarkStreamTextDecode$|BenchmarkRequestWorkloads$'
  const args = ['test', isLegacy ? './relay' : './protocol/builtin', '-run', '^$', '-bench', pattern, '-benchmem', '-benchtime=300ms', '-count=10', '-timeout=30m']
  let isPassed = await run.execute('micro', 'go', args, { cwd })
  if (isPassed) {
    const samples = parseBenchmarks(await readFile(join(run.directory, 'micro.log'), 'utf8'))
    if (samples.length === 0 || samples.some(sample => sample.values.length < 10)) throw new Error('Expected at least ten complete samples per benchmark')
    await writeFile(join(run.directory, 'micro.json'), JSON.stringify(samples, null, 2))
  }
  if (isPassed && !isLegacy && !process.argv.includes('--micro-only')) {
    isPassed = await run.execute('load', 'go', ['test', './server', '-run', '^TestProtocolLoad$', '-count=1', '-timeout=60m', '-v'], {
      cwd, timeoutMillis: 3900000,
      env: { ...process.env, ELYSIA_LOAD_TESTS: '1', ELYSIA_VERIFY_DIR: run.directory },
    })
  }
  await run.finish(isPassed ? 'passed' : 'failed')
  if (!isPassed) process.exitCode = 1
}

export function parseBenchmarks(log) {
  const samples = new Map()
  for (const line of log.split(/\r?\n/)) {
    const match = line.match(/^(Benchmark\S+)-\d+\s+\d+\s+([\d.]+) ns\/op\s+(?:[\d.]+ MB\/s\s+)?([\d.]+) B\/op\s+([\d.]+) allocs\/op/)
    if (!match) continue
    if (!samples.has(match[1])) samples.set(match[1], { name: match[1], values: [] })
    samples.get(match[1]).values.push({ nanos: +match[2], bytes: +match[3], allocations: +match[4] })
  }
  return [...samples.values()]
}
