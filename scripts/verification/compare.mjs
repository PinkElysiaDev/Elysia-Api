import { readFile, writeFile } from 'node:fs/promises'
import { join } from 'node:path'

const median = values => { const sorted = [...values].sort((a,b) => a-b); const middle = Math.floor(sorted.length/2); return sorted.length%2 ? sorted[middle] : (sorted[middle-1]+sorted[middle])/2 }

/** Returns a seeded bootstrap interval for the ratio of independent medians. */
export function compareSamples(before, after, seed = 20261003) {
  if (!before.length || !after.length) throw new Error('Cannot compare missing measurements')
  if ([...before,...after].some(value => !Number.isFinite(value) || value <= 0)) return { assessment:'below-measurement-resolution', samples:[before.length,after.length] }
  let state = seed >>> 0
  const random = () => { state = (Math.imul(state, 1664525) + 1013904223) >>> 0; return state / 4294967296 }
  const sample = values => Array.from({ length: values.length }, () => values[Math.floor(random() * values.length)])
  const ratios = Array.from({ length: 10000 }, () => median(sample(after)) / median(sample(before))).sort((a, b) => a - b)
  const interval = [ratios[250], ratios[9749]]
  return { beforeMedian: median(before), afterMedian: median(after), ratio: median(after) / median(before), interval95: interval, samples: [before.length, after.length], seed,
    assessment: interval[0] > 1.05 ? 'regression' : interval[1] < 0.95 ? 'improvement' : 'inconclusive' }
}

export async function comparePerformance(beforeDirectory, afterDirectory, output) {
  const before = JSON.parse(await readFile(join(beforeDirectory, 'micro.json'), 'utf8'))
  const after = JSON.parse(await readFile(join(afterDirectory, 'micro.json'), 'utf8'))
  const micro = []
  for (const candidate of after) {
    const baseline = before.find(item => item.name === candidate.name || candidate.name === 'BenchmarkCacheConversion' && item.name === 'BenchmarkProtocolConversion/claude')
    if (!baseline) continue
    const metrics = {}
    for (const name of ['nanos', 'bytes', 'allocations']) metrics[name] = compareSamples(baseline.values.map(value => value[name]), candidate.values.map(value => value[name]))
    micro.push({ name: candidate.name, metrics })
  }
  const report = { beforeDirectory, afterDirectory, micro, load: [] }
  if (micro.length === 0) throw new Error('No equivalent microbenchmark workloads to compare')
  const load = async directory => { try { return JSON.parse(await readFile(join(directory, 'load.json'), 'utf8')) } catch (error) { if (error.code === 'ENOENT') return []; throw error } }
  const first = await load(beforeDirectory), second = await load(afterDirectory)
  const key = value => [value.path, value.workload, value.delayed, value.concurrency].join('/')
  for (const group of new Set(second.map(key))) {
    const baseline = first.filter(value => key(value) === group)
    const candidate = second.filter(value => key(value) === group)
    if (!baseline.length) continue
    if ([...baseline, ...candidate].some(value => value.failures !== 0)) { report.load.push({ name: group, assessment: 'failed-requests' }); continue }
    const metrics = {}
    for (const metric of ['cpuPerRequestNS', 'requestsPerSecond', 'allocatedBytesPerRequest', 'allocationsPerRequest', 'gcPauseNS']) {
      const left = baseline.map(value => value[metric]), right = candidate.map(value => value[metric])
      if (left.some(value => value <= 0)) { metrics[metric] = { assessment: 'below-measurement-resolution' }; continue }
      metrics[metric] = compareSamples(left, right)
      if (metric === 'requestsPerSecond') {
        const verdict = metrics[metric].assessment
        if (verdict === 'regression') metrics[metric].assessment = 'improvement'
        else if (verdict === 'improvement') metrics[metric].assessment = 'regression'
      }
    }
    for (const [index, name] of ['p50', 'p95', 'p99'].entries()) {
      metrics[name] = compareSamples(baseline.map(value => value.latencyP50P95P99NS[index]), candidate.map(value => value.latencyP50P95P99NS[index]))
      metrics[`firstFrame${name.toUpperCase()}`] = compareSamples(baseline.map(value => value.firstFrameP50P95P99NS[index]), candidate.map(value => value.firstFrameP50P95P99NS[index]))
    }
    report.load.push({ name: group, metrics, retainedHeapDeltaBytes: { before: baseline.map(value => value.retainedHeapDeltaBytes), after: candidate.map(value => value.retainedHeapDeltaBytes) }, gcCycles: { before: baseline.map(value => value.gcCycles), after: candidate.map(value => value.gcCycles) } })
  }
  await writeFile(output, JSON.stringify(report, null, 2))
  return report
}
