import { readFileSync, writeFileSync, mkdirSync } from 'node:fs'
import { resolve, join } from 'node:path'
import { spawnSync } from 'node:child_process'
import { createHash } from 'node:crypto'

const [beforePath, afterPath, outputPath, casesCSV, requests, repeats] = process.argv.slice(2)
if (!casesCSV) throw new Error('Usage: node paired-performance.mjs BEFORE_REPORT AFTER_REPORT NEW_OUTPUT CASES')
const output = resolve(outputPath)
mkdirSync(output, { recursive: false })
const reports = [beforePath, afterPath].map(path => JSON.parse(readFileSync(path)))
const labels = ['before', 'after']
const result = { status: 'running', reports: [resolve(beforePath), resolve(afterPath)], order: ['before', 'after', 'after', 'before'], cases: casesCSV.split(','), runs: [], results: [] }
const median = values => {
  const sorted = values.toSorted((a, b) => a - b)
  const middle = Math.floor(sorted.length / 2)
  return sorted.length % 2 ? sorted[middle] : (sorted[middle - 1] + sorted[middle]) / 2
}
try {
  for (const name of result.cases) {
    for (const [index, label] of result.order.entries()) {
      const report = reports[labels.indexOf(label)]
      const step = report.steps.find(step => step.name === `measure-${name}`)
      if (!step) throw new Error(`Missing case ${name}`)
      const directory = join(output, `${name}-${index}-${label}`)
      mkdirSync(directory)
      const env = { ...process.env, GOMAXPROCS: report.runtime.GOMAXPROCS, GOGC: report.runtime.GOGC, GOMEMLIMIT: report.runtime.GOMEMLIMIT }
      for (const key of Object.keys(env)) if (/^ELYSIA_(FLAME_|CONVERSION_HTTP_|LOG_MEMORY_BASELINE)/.test(key)) delete env[key]
      Object.assign(env, step.environment, { ELYSIA_FLAME_OUTPUT: directory })
      if (requests && step.environment.ELYSIA_FLAME_WORKLOAD !== 'image') env.ELYSIA_FLAME_REQUESTS = requests
      if (repeats) env.ELYSIA_FLAME_REPEATS = repeats
      const sha = createHash('sha256').update(readFileSync(step.command)).digest('hex')
      if (sha !== report.binarySHA256) throw new Error(`Binary hash mismatch: ${label}`)
      console.log(`${name}: ${index + 1}/4 ${label}`)
      const run = spawnSync(step.command, step.argv, { cwd: resolve('backend'), env, encoding: 'utf8', maxBuffer: 32 << 20, timeout: 600000 })
      writeFileSync(join(directory, 'run.log'), (run.stdout ?? '') + (run.stderr ?? ''))
      if (run.status !== 0) throw new Error(`Run failed: ${run.status} ${run.error ?? ''}`)
      const measurements = JSON.parse(readFileSync(join(directory, 'measurements.json')))
      result.runs.push({ name, label, binarySHA256: sha, command: step.command, argv: step.argv, environment: env, measurements })
    }
    const summary = { name }
    for (const label of labels) {
      const samples = result.runs.filter(run => run.name === name && run.label === label).flatMap(run => run.measurements.filter(m => m.Phase !== 'first-request'))
      summary[label] = {
        samples: samples.length,
        bytes: median(samples.map(m => m.Metrics.Allocated / m.Requests)),
        allocations: median(samples.map(m => m.Metrics.Allocations / m.Requests)),
        cpuMS: median(samples.map(m => m.Metrics.CPUTimeNS / m.Requests / 1e6)),
        latencyP50MS: median(samples.map(m => m.LatencyP50P95P99NS[0] / 1e6)),
        peakHeap: Math.max(...samples.map(m => m.Metrics.PeakHeap)),
        peakRSS: Math.max(...samples.map(m => m.Metrics.PeakWorkingSet)),
      }
    }
    result.results.push(summary)
  }
  result.status = 'passed'
} finally {
  // Do not retain unrelated shell environment variables or credentials.
  for (const run of result.runs) run.environment = Object.fromEntries(Object.entries(run.environment).filter(([key]) => /^(ELYSIA_|GOMAXPROCS$|GOGC$|GOMEMLIMIT$)/.test(key)))
  writeFileSync(join(output, 'report.json'), JSON.stringify(result, null, 2) + '\n')
  console.log(JSON.stringify(result.results, null, 2))
}
