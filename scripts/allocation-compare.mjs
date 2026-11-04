import { readFile, readdir, writeFile } from 'node:fs/promises'
import { resolve, join } from 'node:path'
import { compareSamples } from './verification/compare.mjs'

const [folder, before, after, confirmation] = process.argv.slice(2)
if (!folder || !before || !after) throw new Error('Usage: allocation-compare.mjs directory before after')
const directory = resolve(folder)
const read = async name => JSON.parse(await readFile(join(directory, name), 'utf8'))
const files = await readdir(directory)
const groups = {}
const isMicro = files.includes('samples.json')
if (isMicro) {
  for (const row of await read('samples.json')) {
    const group = groups[row.benchmark] ??= {}
    ;(group[row.stage] ??= []).push({ ns: row.ns, bytes: row.bytes, objects: row.objects })
  }
} else {
  for (const root of confirmation ? [directory, resolve(confirmation)] : [directory]) {
  for (const entry of await readdir(root, { withFileTypes: true })) {
    if (!entry.isDirectory()) continue
    const name = entry.name
    const run = JSON.parse(await readFile(join(root, name, 'run.json'), 'utf8'))
    if (run.status !== 'passed' || run.mode !== 'measure') throw new Error(`Not a completed timing batch: ${name}`)
    const rows = JSON.parse(await readFile(join(root, name, 'measurements.json'), 'utf8'))
    if (rows.length !== 1 || rows[0].UpstreamCalls !== rows[0].Requests) throw new Error(`Workload mismatch: ${name}`)
    const row = rows[0]
    const sample = {
      cpuMS: row.Metrics.CPUTimeNS / row.Requests / 1e6,
      throughput: row.Requests * 1000 / row.ElapsedMS,
      p50MS: row.LatencyP50P95P99NS[0] / 1e6,
      p95MS: row.LatencyP50P95P99NS[1] / 1e6,
      firstFrameMS: row.FirstFrameP50P95P99NS[0] / 1e6,
      peakRSS: row.Metrics.PeakWorkingSet,
      recoveryHeap: row.Recovery.at(-1).Metrics.Heap,
    }
    if (run.phase === 'counters') Object.assign(sample, {
      bytes: row.Metrics.Allocated / row.Requests,
      objects: row.Metrics.Allocations / row.Requests,
      gcPauseNS: row.Metrics.GCPauseNS / row.Requests,
    })
    const group = groups[run.scenario] ??= {}
    ;(group[run.stage] ??= []).push(sample)
  }
  }
}
const report = { before, after, estimator: 'Seeded bootstrap ratio of independent medians; timing and profiling are separate.', comparisons: {} }
for (const [name, group] of Object.entries(groups)) {
  const left = group[before], right = group[after]
  const minimum = isMicro ? 10 : 5
  if (!left || !right || left.length < minimum || left.length !== right.length) throw new Error(`Unmatched or incomplete rounds: ${name}`)
  report.comparisons[name] = Object.fromEntries(Object.keys(left[0]).map(metric => {
    const result = compareSamples(left.map(row => row[metric]), right.map(row => row[metric]))
    if (metric === 'throughput') {
      if (result.assessment === 'regression') result.assessment = 'improvement'
      else if (result.assessment === 'improvement') result.assessment = 'regression'
    }
    return [metric, result]
  }))
}
if (confirmation) report.confirmation = resolve(confirmation)
await writeFile(join(directory, confirmation ? 'confirmed-comparison.json' : 'comparison.json'), JSON.stringify(report, null, 2))
console.log(JSON.stringify(report, null, 2))
