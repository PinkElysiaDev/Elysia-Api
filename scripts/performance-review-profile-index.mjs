import { readFile, writeFile, readdir } from 'node:fs/promises'
import { execFileSync } from 'node:child_process'
import { join, resolve } from 'node:path'

const root = resolve(process.argv[2] || '.tmp-dev/performance-review-2026-10-22')
const result = []
const pprof = args => execFileSync('go', ['tool', 'pprof', ...args], { encoding: 'utf8', windowsHide: true, maxBuffer: 32 << 20 })
for (const folder of await readdir(join(root, 'runs'))) {
  const directory = join(root, 'runs', folder)
  const files = await readdir(directory)
  if (!files.includes('cpu.pprof')) continue
  let run
  try { run = JSON.parse(await readFile(join(directory, 'result.json'))) } catch (e) { if (e.code === 'ENOENT') continue; throw e }
  const binary = run.binary || join(root, 'binaries', `${run.version}.test.exe`), profile = join(directory, 'cpu.pprof')
  const top = pprof(['-top', '-nodecount=80', binary, profile])
  const cumulative = pprof(['-top', '-cum', '-nodecount=80', binary, profile])
  await writeFile(join(directory, 'cpu-top.txt'), top)
  await writeFile(join(directory, 'cpu-cumulative.txt'), cumulative)
  const sample = top.match(/Total samples = ([\d.]+)(ms|s|mins?)/)
  const seconds = sample ? Number(sample[1]) * (sample[2] === 'ms' ? .001 : sample[2] === 's' ? 1 : 60) : null
  const rows = JSON.parse(await readFile(join(directory, 'measurements.json')))
  result.push({ run: folder, version: run.version, revision: run.revision, binary, profile, seconds, adequateSamples: seconds !== null && seconds >= 10, measurement: rows.map(r => ({ requests: r.Requests, elapsedMS: r.ElapsedMS, gatewayCPUSeconds: r.Metrics.CPUTimeNS / 1e9 })), note: 'Profile and load windows may differ at the edge; profiling runs are not formal latency measurements.' })
}
await writeFile(join(root, 'profile-index.json'), JSON.stringify(result, null, 2))
console.log({ profiles: result.length, insufficient: result.filter(x => !x.adequateSamples).map(x => ({ run: x.run, seconds: x.seconds })) })
