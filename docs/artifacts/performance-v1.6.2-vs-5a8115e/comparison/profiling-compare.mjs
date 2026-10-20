import { readFile, writeFile, mkdir } from 'node:fs/promises'
import { execFileSync } from 'node:child_process'
import { createHash } from 'node:crypto'
import { resolve, join } from 'node:path'

const [beforeArg, afterArg, beforeExtraArg, afterExtraArg, outputArg] = process.argv.slice(2)
if (!outputArg) throw new Error('Usage: node profiling-compare.mjs BEFORE AFTER BEFORE_EXTRA AFTER_EXTRA OUTPUT')
const directories = [beforeArg, afterArg, beforeExtraArg, afterExtraArg].map(resolvePath => resolve(resolvePath))
const output = resolve(outputArg)
await mkdir(output, { recursive: true })
const reports = await Promise.all(directories.map(async d => JSON.parse(await readFile(join(d, 'report.json')))))
if (reports.some(r => r.status !== 'passed')) throw new Error('All four collections must pass')
if (JSON.stringify(reports[0].settings) !== JSON.stringify(reports[1].settings)) throw new Error('Main settings differ')
const sha = data => createHash('sha256').update(data).digest('hex')
for (const [i, directory] of directories.entries()) {
  const manifest = JSON.parse(await readFile(join(directory, 'manifest.json')))
  for (const file of manifest) if (sha(await readFile(join(directory, file.path))) !== file.sha256) throw new Error(`Evidence checksum mismatch: ${directory}/${file.path}`)
  const binary = join(directories[i % 2], 'server.test')
  if (sha(await readFile(binary)) !== reports[i].binarySHA256) throw new Error(`Binary checksum mismatch: ${binary}`)
}
const median = values => { const v = [...values].sort((a, b) => a - b); return (v[Math.floor((v.length - 1) / 2)] + v[Math.floor(v.length / 2)]) / 2 }
const change = (a, b) => a ? (b / a - 1) * 100 : null
const result = {
  before: reports[0].commit, after: reports[1].commit,
  environment: Object.fromEntries(['go', 'goEnvironment', 'cpu', 'logicalCPUs', 'totalMemoryBytes', 'runtime', 'settings'].map(k => [k, reports[1][k]])),
  baselineRuntime: reports[0].runtime,
  evidence: directories, comparisons: [], cpu: [], allocations: [], micro: [], cancellation: [],
}
function summarize(rows) {
  const samples = rows.filter(r => r.Phase !== 'first-request')
  return {
    repeats: samples.length, requestsPerRepeat: samples[0].Requests,
    allocatedBytesPerRequest: median(samples.map(r => r.Metrics.Allocated / r.Requests)),
    allocationsPerRequest: median(samples.map(r => r.Metrics.Allocations / r.Requests)),
    cpuMSPerRequest: median(samples.map(r => r.Metrics.CPUTimeNS / r.Requests / 1e6)),
    requestsPerSecond: median(samples.map(r => r.Requests * 1000 / r.ElapsedMS)),
    latencyMS: [0, 1, 2].map(i => median(samples.map(r => r.LatencyP50P95P99NS[i] / 1e6))),
    firstByteMS: [0, 1, 2].map(i => median(samples.map(r => r.FirstByteP50P95P99NS[i] / 1e6))),
    firstFrameMS: [0, 1, 2].map(i => median(samples.map(r => r.FirstFrameP50P95P99NS[i] / 1e6))),
    firstRequestMS: rows.find(r => r.Phase === 'first-request')?.ElapsedMS,
    peakHeapBytes: Math.max(...samples.map(r => r.Metrics.PeakHeap)),
    peakRSSBytes: Math.max(...samples.map(r => r.Metrics.PeakWorkingSet)),
    recoveryHeapBytes: median(samples.map(r => r.Recovery.at(-1).Metrics.Heap)),
    recoveryRSSBytes: median(samples.map(r => r.Recovery.at(-1).Metrics.WorkingSet)),
    gcCycles: samples.map(r => r.Metrics.GCCycles),
    gcCPUSeconds: samples.map(r => r.Metrics.GCCPUSeconds),
    gcPauseNS: samples.map(r => r.Metrics.GCPauseNS),
    maxActiveAfterSettle: Math.max(...samples.map(r => r.Metrics.ActiveRequests)),
    maxQueueAfterSettle: Math.max(...samples.map(r => r.Metrics.QueueDepth)),
    maxPayloadAfterSettle: Math.max(...samples.map(r => r.Metrics.QueuedPayloadBytes)),
    droppedTimelineSamples: samples.reduce((n, r) => n + (r.Metrics.DroppedSamples || 0), 0),
    repeatsRaw: samples.map(r => ({ requests: r.Requests, elapsedMS: r.ElapsedMS, metrics: { ...r.Metrics, Timeline: undefined }, recovery: r.Recovery })),
  }
}
function compare(name, before, after) {
  if (before.repeats !== after.repeats || before.requestsPerRepeat !== after.requestsPerRepeat) throw new Error(`Sample count mismatch: ${name}`)
  for (const row of [before, after]) if (row.maxActiveAfterSettle || row.maxQueueAfterSettle || row.maxPayloadAfterSettle || row.droppedTimelineSamples) throw new Error(`Incomplete recovery/timeline: ${name}`)
  const changesPercent = {}
  for (const k of ['allocatedBytesPerRequest', 'allocationsPerRequest', 'cpuMSPerRequest', 'requestsPerSecond', 'peakHeapBytes', 'peakRSSBytes', 'recoveryHeapBytes', 'recoveryRSSBytes']) changesPercent[k] = change(before[k], after[k])
  changesPercent.latencyP50 = change(before.latencyMS[0], after.latencyMS[0])
  result.comparisons.push({ name, before, after, changesPercent })
}
for (const r of reports[0].results) {
  const rows = await Promise.all(directories.slice(0, 2).map(async d => JSON.parse(await readFile(join(d, `measure-${r.name}`, 'measurements.json')))))
  compare(r.name, summarize(rows[0]), summarize(rows[1]))
}
for (const name of ['nodes', 'events', 'image31']) {
  const rows = await Promise.all(directories.slice(2).map(async d => JSON.parse(await readFile(join(d, `measure-${name}`, 'measurements.json')))))
  for (const stream of [false, true]) {
    const groups = rows.map(r => r.filter(s => s.Stream === stream))
    if (!groups[0].length) continue
    compare(`legacy-${name}-${stream ? 'sse' : 'json'}`, summarize(groups[0]), summarize(groups[1]))
  }
}
for (const version of [0, 1]) {
  for (const [index, cases] of [[version, ['chat-short', 'stream256', 'image2']], [version + 2, ['nodes', 'events', 'image31']]]) {
    const binary = join(directories[version], 'server.test')
    for (const name of cases) {
      const dir = directories[index]
      const top = execFileSync('go', ['tool', 'pprof', '-top', '-nodecount=0', '-nodefraction=0', binary, join(dir, `cpu-${name}`, 'cpu.pprof')], { encoding: 'utf8', maxBuffer: 16 << 20 })
      await writeFile(join(output, `${version ? 'after' : 'before'}-cpu-${name}-all.txt`), top)
      const functions = []
      for (const line of top.split('\n')) {
        const m = line.match(/^\s*([\d.]+\w*)\s+([\d.]+)%\s+[\d.]+%\s+([\d.]+\w*)\s+([\d.]+)%\s+(.+)$/)
        if (m) functions.push({ name: m[5], flat: m[1], flatPercent: +m[2], cumulative: m[3], cumulativePercent: +m[4] })
      }
      result.cpu.push({ version: version ? 'after' : 'before', name, total: top.match(/Total samples = ([^(\n]+)/)?.[1].trim(), functions })
      for (const sample of ['alloc_space', 'alloc_objects', 'inuse_space']) {
        const args = ['tool', 'pprof', '-top', '-nodecount=30', `-sample_index=${sample}`]
        if (sample.startsWith('alloc_')) args.push('-base', join(dir, `memory-${name}`, 'alloc-before.pprof'))
        args.push(binary, join(dir, `memory-${name}`, sample === 'inuse_space' ? 'heap-drained.pprof' : 'alloc-after.pprof'))
        const text = execFileSync('go', args, { encoding: 'utf8', maxBuffer: 16 << 20 })
        await writeFile(join(output, `${version ? 'after' : 'before'}-${sample}-${name}.txt`), text)
        result.allocations.push({ version: version ? 'after' : 'before', name, sample, text })
      }
    }
  }
  for (let i = 0; i < 5; i++) {
    const row = JSON.parse(await readFile(join(directories[version], `cancellation-${i}.json`)))
    result.cancellation.push({ version: version ? 'after' : 'before', batch: i, reclaimedHeapBytes: row.Reclaimed.Heap, reclaimedRSSBytes: row.Reclaimed.WorkingSet, active: row.Reclaimed.ActiveRequests })
  }
}
const parseMicro = text => {
  const groups = new Map()
  for (const line of text.split('\n')) {
    const m = line.match(/^(Benchmark\S+)-\d+\s+\d+\s+([\d.]+) ns\/op(?:\s+[\d.]+ MB\/s)?\s+(\d+) B\/op\s+(\d+) allocs\/op/)
    if (m) { const rows = groups.get(m[1]) || []; rows.push([+m[2], +m[3], +m[4]]); groups.set(m[1], rows) }
  }
  return groups
}
const micros = await Promise.all(directories.slice(0, 2).map(async d => parseMicro(await readFile(join(d, 'micro.log'), 'utf8'))))
for (const [name, rows] of micros[0]) {
  const after = micros[1].get(name)
  if (after) result.micro.push({ name, beforeSamples: rows.length, afterSamples: after.length, before: [0, 1, 2].map(i => median(rows.map(r => r[i]))), after: [0, 1, 2].map(i => median(after.map(r => r[i]))) })
}
await writeFile(join(output, 'comparison.json'), JSON.stringify(result, null, 2) + '\n')
const fmt = n => n.toFixed(2), pct = n => `${n >= 0 ? '+' : ''}${n.toFixed(1)}%`
const rows = result.comparisons.map(r => {
  const a = r.before, b = r.after, c = r.changesPercent
  return `| ${r.name} | ${fmt(a.allocatedBytesPerRequest / 1024)} → ${fmt(b.allocatedBytesPerRequest / 1024)} (${pct(c.allocatedBytesPerRequest)}) | ${Math.round(a.allocationsPerRequest)} → ${Math.round(b.allocationsPerRequest)} | ${fmt(a.cpuMSPerRequest)} → ${fmt(b.cpuMSPerRequest)} (${pct(c.cpuMSPerRequest)}) | ${fmt(a.latencyMS[0])} → ${fmt(b.latencyMS[0])} (${pct(c.latencyP50)}) | ${fmt(a.peakHeapBytes / 2 ** 20)} → ${fmt(b.peakHeapBytes / 2 ** 20)} | ${fmt(a.peakRSSBytes / 2 ** 20)} → ${fmt(b.peakRSSBytes / 2 ** 20)} |`
})
await writeFile(join(output, 'comparison.md'), `# v1.6.2 → ${result.after.slice(0, 7)}\n\nPer-repeat medians; sampled heap/RSS peaks are maxima over repeats. Profiling timings excluded.\n\n| Case | KiB/request (change) | allocs/request | CPU ms/request (change) | latency P50 ms (change) | peak heap MiB | peak RSS MiB |\n| --- | ---: | ---: | ---: | ---: | ---: | ---: |\n${rows.join('\n')}\n`)
console.log(`Compared ${result.comparisons.length} workloads, ${result.cpu.length} CPU profiles, ${result.micro.length} common microbenchmarks: ${output}`)
