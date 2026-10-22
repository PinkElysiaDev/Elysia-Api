import { readdir, readFile, writeFile } from 'node:fs/promises'
import { join, resolve } from 'node:path'

const root = resolve(process.argv[2] || '.tmp-dev/performance-review-2026-10-22')
const median = xs => { const a = [...xs].sort((a,b) => a-b); return a.length % 2 ? a[(a.length-1)/2] : (a[a.length/2-1]+a[a.length/2])/2 }
const rows = [], failed = []
for (const file of await readdir(join(root, 'steps'))) {
  if (!/^(confirm-)?(component|storage)(-server|-storage)?-\d+-(release|current)\.json$/.test(file) && !/^scan-(2|31)-\d+-(false|true)-current\.json$/.test(file) && !/^cli-hook-\d+-(false|true)-current\.json$/.test(file)) continue
  const run = JSON.parse(await readFile(join(root, 'steps', file)))
  if (run.code !== 0) { failed.push(run); continue }
  const version = file.endsWith('-release.json') ? 'release' : 'current'
  const log = await readFile(run.logPath, 'utf8')
  for (const line of log.split('\n')) {
    const match = line.match(/^(Benchmark\S+)-\d+\s+(\d+)\s+([\d.]+) ns\/op\s+(?:[\d.]+ MB\/s\s+)?([\d.]+) B\/op\s+([\d.]+) allocs\/op/)
    if (match) rows.push({ run: run.name, version, benchmark: (run.name.startsWith('confirm-') ? 'confirm/' : '') + match[1], iterations: +match[2], ns: +match[3], bytes: +match[4], allocations: +match[5] })
  }
}
const summaries = []
for (const name of new Set(rows.map(r => r.benchmark))) {
  const result = { name, versions: {} }
  for (const version of ['release', 'current']) {
    const values = rows.filter(r => r.benchmark === name && r.version === version)
    if (!values.length) continue
    result.versions[version] = { samples: values.length, ...Object.fromEntries(['ns','bytes','allocations'].map(key => [key, { median: median(values.map(r => r[key])), minimum: Math.min(...values.map(r => r[key])), maximum: Math.max(...values.map(r => r[key])) }])) }
  }
  summaries.push(result)
}
const toolRuns = [], heldRuns = []
for (const folder of await readdir(join(root, 'runs'))) {
  if (!/^(tools|held)/.test(folder)) continue
  const run = JSON.parse(await readFile(join(root, 'runs', folder, 'result.json')))
  let data
  const file = folder.startsWith('tools') ? 'tool-roundtrip.json' : 'held-progress.json'
  try { data = JSON.parse(await readFile(join(root, 'runs', folder, file))) } catch(e) {
    if(e.code !== 'ENOENT') throw e
    if(folder.startsWith('held')) {
      try { data = JSON.parse(await readFile(join(root,'runs',folder,'held.json'))) }
      catch(e) { if(e.code !== 'ENOENT') throw e; data = { unavailable: true, reason: 'Failed run did not publish final retention JSON; see preserved process log and profiles.' } }
    }
  }
  ;(folder.startsWith('tools') ? toolRuns : heldRuns).push({ run: folder, status: run.status, version: run.version, data })
}
await writeFile(join(root, 'components.json'), JSON.stringify({ rows, summaries, failed, toolRuns, heldRuns }, null, 2))
const lines = ['# 独立组件基准', '', '仅配对共享成功路径；CLI、schema 和缓存指标不混入生成请求。原始重复及范围见 components.json。', '', '| 基准 | 版本 | 配对样本 | ns/op | B/op | allocs/op |', '|---|---|---:|---:|---:|---:|']
for (const s of summaries) for(const [v,m] of Object.entries(s.versions)) lines.push(`| ${s.name} | ${v} | ${m.samples} | ${m.ns.median} | ${m.bytes.median} | ${m.allocations.median} |`)
await writeFile(join(root, 'components.md'), lines.join('\n')+'\n')
console.log({ benchmarks: summaries.length, samples: rows.length, failed: failed.length, toolRuns: toolRuns.length, heldRuns: heldRuns.length })
