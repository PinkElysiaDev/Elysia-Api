import { readFileSync, writeFileSync, mkdirSync } from 'node:fs'
import { resolve, join } from 'node:path'
import { spawnSync } from 'node:child_process'
import { createHash } from 'node:crypto'

const [input, destination, boundedPolicy = '1', workers = '8', requests = '16'] = process.argv.slice(2)
if (!destination) throw new Error('Usage: node preparation-memory.mjs ITEM4_REPORT NEW_OUTPUT')
const report = JSON.parse(readFileSync(input))
const step = report.steps.find(s => s.name === 'measure-image2')
if (report.status !== 'passed' || !step) throw new Error('Accepted selected HTTP matrix required')
const output = resolve(destination)
mkdirSync(output, { recursive: false })
const hash = createHash('sha256').update(readFileSync(step.command)).digest('hex')
if (hash !== report.binarySHA256) throw new Error('Binary mismatch')
const result = { status: 'running', binarySHA256: hash, sourceReport: resolve(input), order: ['off',boundedPolicy,boundedPolicy,'off'], runs: [], results: [] }
const median = values => { const a = values.toSorted((x,y) => x-y); const i = Math.floor(a.length/2); return a.length%2 ? a[i] : (a[i-1]+a[i])/2 }
try {
  for (const [index, policy] of result.order.entries()) {
    const directory = join(output, `${index}-${policy}`)
    mkdirSync(directory)
    const env = { ...process.env, GOMAXPROCS:'4', GOGC:'100', GOMEMLIMIT:'2GiB' }
    for (const key of Object.keys(env)) if (/^ELYSIA_(FLAME_|CONVERSION_HTTP_|LOG_MEMORY_BASELINE)/.test(key)) delete env[key]
    const settings = { ...step.environment, ELYSIA_FLAME_OUTPUT:directory, ELYSIA_FLAME_REQUESTS:requests, ELYSIA_FLAME_REPEATS:'3', ELYSIA_CONVERSION_HTTP_WORKERS:workers, ELYSIA_CONVERSION_HTTP_PREPARATION:policy }
    Object.assign(env, settings)
    console.log(`preparation policy=${policy}, run ${index+1}/4`)
    const run = spawnSync(step.command, step.argv, { cwd:resolve('backend'), env, encoding:'utf8', maxBuffer:32<<20, timeout:600000 })
    writeFileSync(join(directory,'run.log'), (run.stdout??'')+(run.stderr??''))
    if (run.status !== 0) throw new Error(`Controlled fixture failed: ${run.status}`)
    const measurements = JSON.parse(readFileSync(join(directory,'measurements.json')))
    const samples = measurements.filter(m => m.Phase !== 'first-request')
    for (const m of samples) {
      const p = m.Metrics.Preparation
      if (!p || m.Requests !== Number(requests) || p.Active !== 0 || p.Queued !== 0 || p.ReservedBodyBytes !== 0 || p.Rejected !== 0 || p.TimedOut !== 0) throw new Error('Admission did not settle without rejection')
      if (policy !== 'off' && (p.PeakActive !== Number(boundedPolicy) || p.PeakReservedBodyBytes > 64*2**20 || p.PeakQueued < 1)) throw new Error('Bounded preparation was not exercised')
      if (policy === 'off' && p.PeakActive <= Number(boundedPolicy)) throw new Error('Unbounded baseline did not exercise contention')
    }
    result.runs.push({ policy, command:step.command, argv:step.argv, settings, measurements })
  }
  for (const policy of ['off',boundedPolicy]) {
    const a = result.runs.filter(r => r.policy===policy).flatMap(r=>r.measurements.filter(m=>m.Phase!=='first-request'))
    result.results.push({ policy, requests:a.reduce((n,m)=>n+m.Requests,0), bytes:median(a.map(m=>m.Metrics.Allocated/m.Requests)), allocations:median(a.map(m=>m.Metrics.Allocations/m.Requests)), cpuMS:median(a.map(m=>m.Metrics.CPUTimeNS/m.Requests/1e6)), latencyP50MS:median(a.map(m=>m.LatencyP50P95P99NS[0]/1e6)), peakHeap:Math.max(...a.map(m=>m.Metrics.PeakHeap)), peakRSS:Math.max(...a.map(m=>m.Metrics.PeakWorkingSet)), peakPreparation:Math.max(...a.map(m=>m.Metrics.Preparation.PeakActive)), peakQueued:Math.max(...a.map(m=>m.Metrics.Preparation.PeakQueued)), peakReservedBodyBytes:Math.max(...a.map(m=>m.Metrics.Preparation.PeakReservedBodyBytes)) })
  }
  result.status='passed'
} finally {
  writeFileSync(join(output,'report.json'),JSON.stringify(result,null,2)+'\n')
  console.log(JSON.stringify(result.results,null,2))
}
