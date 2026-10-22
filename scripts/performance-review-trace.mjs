import { execFileSync } from 'node:child_process'
import { readFile, readdir, writeFile } from 'node:fs/promises'
import { resolve, join } from 'node:path'

// Short traces explain waits separately from formal CPU/latency measurements.
const root = resolve(process.argv[2] || '.tmp-dev/performance-review-2026-10-22')
const results = []
for (const name of await readdir(join(root, 'runs'))) {
  const directory = join(root, 'runs', name)
  if (!(await readdir(directory)).includes('execution.trace')) continue
  const run = JSON.parse(await readFile(join(directory, 'result.json')))
  const binary = run.binary || join(root, 'binaries', `${run.version}.test.exe`)
  for (const kind of ['sched', 'sync', 'net', 'syscall']) {
    const file = join(directory, `trace-${kind}.pprof`)
    const bytes = execFileSync('go', ['tool', 'trace', `-pprof=${kind}`, join(directory, 'execution.trace')], { windowsHide: true, maxBuffer: 64 << 20, stdio: ['ignore', 'pipe', 'pipe'] })
    await writeFile(file, bytes)
    const top = execFileSync('go', ['tool', 'pprof', '-top', '-nodecount=50', binary, file], { encoding: 'utf8', windowsHide: true, maxBuffer: 16 << 20, stdio: ['ignore', 'pipe', 'pipe'] })
    await writeFile(join(directory, `trace-${kind}-top.txt`), top)
    results.push({ run: name, status: run.status, version: run.version, kind, file, top: `runs/${name}/trace-${kind}-top.txt`, note: 'Aggregated wait time across goroutines; not wall latency or CPU. Trace overhead is excluded from formal measurements.' })
  }
}
await writeFile(join(root, 'trace-index.json'), JSON.stringify(results, null, 2))
console.log({ traces: results.length / 4 })
