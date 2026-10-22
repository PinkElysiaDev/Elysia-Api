import { readdir, readFile, writeFile } from 'node:fs/promises'
import { resolve, join } from 'node:path'

const root = resolve(process.argv[2] || '.tmp-dev/performance-review-2026-10-22')
const median = a => { const v = [...a].sort((a, b) => a - b); return v.length % 2 ? v[(v.length - 1) / 2] : (v[v.length / 2 - 1] + v[v.length / 2]) / 2 }
const runs = [], failures = [], cold = [], reloads = [], groups = new Map()
for (const folder of await readdir(join(root, 'runs'))) {
  let result
  try { result = JSON.parse(await readFile(join(root, 'runs', folder, 'result.json'))) } catch (e) { if (e.code === 'ENOENT') continue; throw e }
  if (result.status !== 'passed') { failures.push(result); continue }
  let rows
  try { rows = JSON.parse(await readFile(join(root, 'runs', folder, 'measurements.json'))) } catch (e) { if (e.code === 'ENOENT') continue; throw e }
  for (const row of rows) {
    if (row.Phase === 'first-request') cold.push({ run: folder, version: result.version, measurement: row, note: 'First HTTP call after runtime fixture initialization, not process-start time.' })
    if (row.Reload) reloads.push({ run: folder, version: result.version, elapsedMS: row.ReloadMS, metrics: row.Reload, note: 'Separate from post-reload generation timing.' })
  }
  for (const row of rows.filter(r => r.Phase !== 'first-request' && r.Mode === 'measure')) {
    const key = folder.replace(/-\d+-(release|current|merged|normalized)$/, '')
    const value = { run: folder, version: result.version, requests: row.Requests, pair: `${result.pair}:${row.Repeat}`, preparationMode: result.preparation,
      upstreamCalls: row.UpstreamCalls ?? null, generationCountVerified: true,
      bytes: row.Metrics.Allocated / row.Requests, allocations: row.Metrics.Allocations / row.Requests,
      cpuMS: row.Metrics.CPUTimeNS / row.Requests / 1e6, latencyMS: row.LatencyP50P95P99NS[0] / 1e6,
      firstFrameMS: row.FirstFrameP50P95P99NS[0] / 1e6, heapMiB: row.Metrics.PeakHeap / 2 ** 20, rssMiB: row.Metrics.PeakWorkingSet / 2 ** 20,
      gcCycles: row.Metrics.GCCycles, gcCPUSeconds: row.Metrics.GCCPUSeconds,
      preparation: row.Metrics.Preparation ?? null, recovery: row.Recovery?.at(-1) ?? null }
    runs.push(value)
    if (!groups.has(key)) groups.set(key, {})
    const group = groups.get(key)
    ;(group[result.version] ??= []).push(value)
  }
}
const metrics = ['bytes', 'allocations', 'cpuMS', 'latencyMS', 'firstFrameMS', 'heapMiB', 'rssMiB']
const summaries = []
for (const [name, versions] of groups) {
  const summary = { name, versions: {}, changes: {} }
  for (const [version, rows] of Object.entries(versions)) {
    summary.versions[version] = { repeats: rows.length, requests: rows.reduce((n, r) => n + r.requests, 0), ...Object.fromEntries(metrics.map(k => [k, { median: median(rows.map(r => r[k])), minimum: Math.min(...rows.map(r => r[k])), maximum: Math.max(...rows.map(r => r[k])) }])) }
  }
  summary.edges = {}
  for (const [beforeName, afterName] of [['release', 'current'], ['merged', 'normalized'], ['normalized', 'current'], ['merged', 'current']]) {
    const before = versions[beforeName], after = versions[afterName]
    if (!before || !after) continue
    const edge = summary.edges[`${beforeName}->${afterName}`] = {}
    for (const k of metrics) {
      const pairs = before.flatMap(b => { const a = after.find(a => a.pair === b.pair); return a && b[k] > 0 ? [100 * (a[k] / b[k] - 1)] : [] })
      edge[k] = { ratioOfMediansPercent: median(before.map(r => r[k])) > 0 ? 100 * (median(after.map(r => r[k])) / median(before.map(r => r[k])) - 1) : null, pairedPercent: pairs, pairedMedianPercent: pairs.length ? median(pairs) : null, mixedDirection: pairs.some(x => x > 0) && pairs.some(x => x < 0) }
    }
  }
  summary.changes = summary.edges['release->current'] ?? summary.edges['merged->current'] ?? {}
  summaries.push(summary)
}
const formal = runs.filter(r => !/^(smoke|custom-smoke|sustain|timeline|trace)-/.test(r.run))
await writeFile(join(root, 'summary.json'), JSON.stringify({ generated: new Date().toISOString(), definitions: { time: 'gateway CPU includes usage settlement; HTTP latency ends on full consumption', peaks: '20ms sampled absolute peaks; median and maximum both retained', percentages: 'positive is regression for cost metrics; no cross-scenario weighted score', generationCount: 'Every passed collector checks provider delta equals Requests. Explicit count absent in original binaries is null, not zero.', formal: 'Excludes smoke, sustained-lifecycle, timeline-only and trace labels; tool roundtrips are recorded separately.' }, counts: { formalRuns: formal.length, formalRequests: formal.reduce((n, r) => n + r.requests, 0), formalScenarios: new Set(formal.map(r => r.run.replace(/-\d+-(release|current|merged|normalized)$/, ''))).size }, runs, failures, summaries }, null, 2))
await writeFile(join(root, 'cold-and-reload.json'), JSON.stringify({ cold, reloads }, null, 2))
const table = ['| 场景 | 版本 | 次数 | KiB/请求 | CPU ms/请求 | P50 ms | 堆峰值中位/最大 MiB | 工作集峰值中位/最大 MiB |', '| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: |']
for (const s of summaries) for (const [version, v] of Object.entries(s.versions)) table.push(`| ${s.name} | ${version} | ${v.requests} | ${(v.bytes.median / 1024).toFixed(2)} | ${v.cpuMS.median.toFixed(3)} | ${v.latencyMS.median.toFixed(3)} | ${v.heapMiB.median.toFixed(2)} / ${v.heapMiB.maximum.toFixed(2)} | ${v.rssMiB.median.toFixed(2)} / ${v.rssMiB.maximum.toFixed(2)} |`)
await writeFile(join(root, 'comparison.md'), '# 同机测量结果\n\n' + table.join('\n') + '\n')
console.log(JSON.stringify({ groups: summaries.length, runs: runs.length, requests: runs.reduce((n, r) => n + r.requests, 0), failures: failures.length }, null, 2))
