import { readFile, readdir, writeFile } from 'node:fs/promises'
import { resolve, join } from 'node:path'
import { compareSamples } from './verification/compare.mjs'

const directory = resolve(process.argv[2] ?? '.tmp-dev/three-version-2026-11-04')
const read = async path => JSON.parse(await readFile(path, 'utf8'))
const pairs = [['R-release', 'R-build'], ['R-build', 'A'], ['A', 'C-default'], ['C-default', 'C-package'], ['R-release', 'C-package'], ['A', 'C-package'], ['R-build', 'C-package']]
const metrics = {
  cpuMS: row => row.Metrics.CPUTimeNS / row.Requests / 1e6,
  throughput: row => row.Requests * 1000 / row.ElapsedMS,
  p50MS: row => row.LatencyP50P95P99NS[0] / 1e6,
  p95MS: row => row.LatencyP50P95P99NS[1] / 1e6,
  p99MS: row => row.LatencyP50P95P99NS[2] / 1e6,
  firstFrameMS: row => row.FirstFrameP50P95P99NS[0] / 1e6,
  bytes: row => row.Metrics.Allocated / row.Requests,
  allocations: row => row.Metrics.Allocations / row.Requests,
  gcPauseNS: row => row.Metrics.GCPauseNS / row.Requests,
  gcCycles: row => row.Metrics.GCCycles,
  peakRSS: row => row.Metrics.PeakWorkingSet,
  recoveryRSS: row => row.Recovery.at(-1).Metrics.WorkingSet,
  recoveryHeap: row => row.Recovery.at(-1).Metrics.Heap,
}
const report = { estimator: 'Seeded bootstrap ratio of independent medians; cyclic collection order is not a paired estimator.', phases: {} }
for (const phase of ['standalone', 'counters']) {
  const folder = join(directory, `${phase}-measure`)
  const groups = {}
  const records = []
  for (const name of await readdir(folder)) {
    const run = await read(join(folder, name, 'run.json'))
    if (run.status !== 'passed') throw new Error(`Incomplete measurement: ${name}`)
    const rows = await read(join(folder, name, 'measurements.json'))
    if (rows.length !== 1 || rows[0].UpstreamCalls !== rows[0].Requests) throw new Error(`Workload mismatch: ${name}`)
    const row = rows[0]
    const samples = Object.fromEntries(Object.entries(metrics).filter(([key]) => phase === 'counters' || !['bytes', 'allocations', 'gcPauseNS'].includes(key)).map(([key, read]) => [key, read(row)]))
    const group = groups[run.scenario] ??= {}
    ;(group[run.version] ??= []).push(samples)
    records.push({ path: name, version: run.version, scenario: run.scenario, binarySHA256: run.binarySHA256, samples })
  }
  const comparisons = {}
  for (const [scenario, versions] of Object.entries(groups)) {
    comparisons[scenario] = {}
    for (const [name, samples] of Object.entries(versions)) if (samples.length !== 5) throw new Error(`${scenario}/${name}: expected five rounds, got ${samples.length}`)
    for (const [before, after] of pairs) {
      if (!versions[before] || !versions[after]) continue
      comparisons[scenario][`${before} -> ${after}`] = Object.fromEntries(Object.keys(versions[before][0]).map(key => {
        const result = compareSamples(versions[before].map(row => row[key]), versions[after].map(row => row[key]))
        if (key === 'throughput') {
          if (result.assessment === 'regression') result.assessment = 'improvement'
          else if (result.assessment === 'improvement') result.assessment = 'regression'
        }
        return [key, result]
      }))
    }
  }
  report.phases[phase] = { records, comparisons }
}
await writeFile(join(directory, 'comparison.json'), JSON.stringify(report, null, 2))
console.log(join(directory, 'comparison.json'))
