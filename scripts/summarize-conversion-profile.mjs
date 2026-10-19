import { readFile, readdir, writeFile } from 'node:fs/promises'
import { join, resolve } from 'node:path'
import { execFileSync } from 'node:child_process'

const directory = resolve(process.argv[2] || '.tmp-dev/conversion-flames')
const median = values => [...values].sort((a, b) => a - b)[Math.floor(values.length / 2)]
const result = { cpu: [], measurements: [], phases: [], ledger: {}, limitations: ['CPU cumulative rows are nested, not additive.', 'Scan/key spans are logical work, not unique data or heap allocation.', 'Instrumented ledger timings are excluded.', 'Heap snapshots are allocation sites, not object retention edges.'] }
for (const entry of await readdir(directory, { withFileTypes: true })) {
  if (!entry.isDirectory()) continue
  const folder = join(directory, entry.name)
  const files = await readdir(folder)
  if (files.includes('cpu.pprof') && entry.name.startsWith('cpu-')) {
    const top = execFileSync('go', ['tool', 'pprof', '-top', '-nodecount=0', '-nodefraction=0', join(directory, 'server.test.exe'), join(folder, 'cpu.pprof')], { encoding: 'utf8', windowsHide: true })
    await writeFile(join(folder, 'cpu-top-all.txt'), top)
    const functions = []
    for (const line of top.split('\n')) {
      const match = line.match(/^\s*([\d.]+\w*)\s+([\d.]+)%\s+[\d.]+%\s+([\d.]+\w*)\s+([\d.]+)%\s+(.+)$/)
      if (match) functions.push({ name: match[5], flat: match[1], flatPercent: +match[2], cumulative: match[3], cumulativePercent: +match[4] })
    }
    result.cpu.push({ scenario: entry.name, total: top.match(/Total samples = ([^(\n]+)/)?.[1].trim(), functions })
  }
  if (files.includes('measurements.json')) {
    const rows = JSON.parse(await readFile(join(folder, 'measurements.json')))
    let environment
    if (files.includes('environment.json')) environment = JSON.parse(await readFile(join(folder, 'environment.json')))
    for (const stream of [false, true]) {
      const group = rows.filter(row => row.Stream === stream)
      if (!group.length) continue
      result.measurements.push({ scenario: entry.name, mode: group[0].Mode, stream, samples: group.length, requests: group.map(row => row.Requests),
        timingMethod: environment?.forcedGCBeforeTiming === false ? 'natural-gc' : group[0].Mode === 'measure' ? 'diagnostic-reset-gc-before-interval' : 'profiled-diagnostic',
        purpose: entry.name.startsWith('verified-') ? 'exact-response-verification' : entry.name.startsWith('timing-') ? 'formal-timing' : 'diagnostic',
        elapsedMS: group.map(row => row.ElapsedMS), medianMS: median(group.map(row => row.ElapsedMS)),
        peakHeapMiB: group.map(row => row.Metrics.PeakHeap / 2 ** 20),
        medianPeakHeapMiB: median(group.map(row => row.Metrics.PeakHeap / 2 ** 20)),
        medianAllocatedMiB: median(group.map(row => row.Metrics.Allocated / 2 ** 20)),
        allocatedMiB: group.map(row => row.Metrics.Allocated / 2 ** 20),
        peakWorkingSetMiB: group.map(row => row.Metrics.PeakWorkingSet / 2 ** 20),
      })
    }
  }
}
for (const size of [2, 31]) {
  try {
    const ledger = JSON.parse(await readFile(join(directory, `ledger-${size}MiB.json`)))
    result.ledger[size] = {}
    for (const [changed, phases] of Object.entries(ledger)) {
      result.ledger[size][changed] = {}
      for (const [phase, entries] of Object.entries(phases)) {
        const totals = {}
        for (const [key, metric] of Object.entries(entries)) {
          const operation = key.split('/').at(-1)
          const before = totals[operation] || { calls: 0, bytes: 0 }
          totals[operation] = { calls: before.calls + metric.Calls, bytes: before.bytes + metric.Bytes }
        }
        result.ledger[size][changed][phase] = totals
      }
    }
  } catch (error) { if (error.code !== 'ENOENT') throw error }
}
try {
  const rows = JSON.parse(await readFile(join(directory, 'phases/phases.json')))
  const keys = [...new Set(rows.map(row => `${row.Workload}/${row.SizeMiB}/${row.Phase}`))]
  for (const key of keys) {
    const group = rows.filter(row => `${row.Workload}/${row.SizeMiB}/${row.Phase}` === key)
    result.phases.push({ key, count: group.length, medianMS: median(group.map(row => row.ElapsedMS)), medianAllocatedMiB: median(group.map(row => row.Allocated / 2 ** 20)), heapMiB: group.map(row => row.Heap / 2 ** 20) })
  }
} catch (error) { if (error.code !== 'ENOENT') throw error }
await writeFile(join(directory, 'summary.json'), JSON.stringify(result, null, 2))
console.log(JSON.stringify({ cpuProfiles: result.cpu.length, measurementRows: result.measurements.length, phases: result.phases, ledger: result.ledger }, null, 2))
