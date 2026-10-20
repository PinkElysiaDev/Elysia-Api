import { spawn, execFileSync } from 'node:child_process'
import { readFile, writeFile, mkdir, readdir, copyFile } from 'node:fs/promises'
import { createHash } from 'node:crypto'
import { resolve, join } from 'node:path'

const [rootArg, evidenceArg, outputArg] = process.argv.slice(2)
if (!outputArg) throw new Error('Usage: node profiling-supplement.mjs ROOT MAIN_EVIDENCE OUTPUT')
const root = resolve(rootArg), evidence = resolve(evidenceArg), output = resolve(outputArg)
const main = JSON.parse(await readFile(join(evidence, 'report.json')))
if (main.status !== 'passed') throw new Error('Main collection must pass first')
const binary = join(evidence, 'server.test')
const sha = data => createHash('sha256').update(data).digest('hex')
if (sha(await readFile(binary)) !== main.binarySHA256) throw new Error('Binary identity changed')
await mkdir(output, { recursive: true })
if ((await readdir(output)).length) throw new Error('Output must be empty')
const env = { ...process.env, GOMAXPROCS: '4', GOGC: '100', GOMEMLIMIT: '2GiB' }
for (const key of Object.keys(env)) if (/^ELYSIA_(FLAME_|CONVERSION_HTTP_|LOG_MEMORY_BASELINE)/.test(key)) delete env[key]
const report = { status: 'running', created: new Date().toISOString(), commit: main.commit, binarySHA256: main.binarySHA256, mainEvidence: evidence, steps: [] }
const save = () => writeFile(join(output, 'report.json'), JSON.stringify(report, null, 2) + '\n')
await copyFile(new URL(import.meta.url), join(output, 'runner.mjs'))
try {
  // Same original dev72 payloads, without the Windows-only memory gate.
  // This machine has 32 GiB; requests are sequential, never 31 MiB x 4.
  for (const [name, workload, size, requests, stream] of [
    ['nodes', 'nodes', 2, 4, ''], ['events', 'events', 2, 16, 'sse'], ['image31', 'image', 31, 1, ''],
  ]) for (const mode of ['measure', 'memory', 'cpu']) {
    const directory = join(output, `${mode}-${name}`)
    await mkdir(directory)
    const settings = {
      ELYSIA_FLAME_WORKLOAD: workload, ELYSIA_FLAME_MODE: mode, ELYSIA_FLAME_OUTPUT: directory,
      ELYSIA_CONVERSION_HTTP_MIB: String(size), ELYSIA_CONVERSION_HTTP_WORKERS: '1',
      ELYSIA_CONVERSION_HTTP_CANDIDATES: '1', ELYSIA_CONVERSION_HTTP_LOG: 'off',
      ELYSIA_FLAME_REQUESTS: String(requests), ELYSIA_FLAME_REPEATS: '5', ELYSIA_FLAME_WARMUP: '4',
      ELYSIA_FLAME_COLD: '1', ELYSIA_FLAME_VERIFY_EXACT: '1', ELYSIA_FLAME_SECONDS: '30',
      ELYSIA_FLAME_STREAM: stream, ELYSIA_FLAME_RECOVERY_MS: mode === 'measure' ? '1000' : '',
      ELYSIA_FLAME_TIMELINE: mode === 'measure' ? '1' : '',
    }
    const argv = ['-test.run=^TestConversionFlamegraph$', '-test.v', '-test.timeout=20m']
    console.log(`[${mode}-${name}] ${binary} ${argv.join(' ')}`)
    const started = Date.now(), chunks = []
    const child = spawn(binary, argv, { cwd: join(root, 'backend'), env: { ...env, ...settings }, stdio: ['ignore', 'pipe', 'pipe'] })
    child.stdout.on('data', data => chunks.push(data)); child.stderr.on('data', data => chunks.push(data))
    const code = await new Promise((resolve, reject) => { child.once('error', reject); child.once('close', resolve) })
    const log = Buffer.concat(chunks)
    await writeFile(join(output, `${mode}-${name}.log`), log)
    report.steps.push({ name, mode, argv, settings, code, elapsedMS: Date.now() - started })
    await save()
    if (code !== 0) throw new Error(log.toString().slice(-6000))
    if (mode !== 'measure') for (const sample of mode === 'cpu' ? ['cpu'] : ['alloc_space', 'alloc_objects', 'inuse_space']) {
      const args = ['tool', 'pprof', '-top', '-nodecount=0', '-nodefraction=0', `-sample_index=${sample}`]
      if (sample.startsWith('alloc_')) args.push('-base', join(directory, 'alloc-before.pprof'))
      args.push(binary, join(directory, sample === 'cpu' ? 'cpu.pprof' : sample === 'inuse_space' ? 'heap-drained.pprof' : 'alloc-after.pprof'))
      await writeFile(join(directory, `${sample}-top.txt`), execFileSync('go', args, { encoding: 'utf8', maxBuffer: 16 << 20 }))
    }
  }
  report.status = 'passed'
} catch (error) {
  report.status = 'failed'; report.error = String(error.stack || error); process.exitCode = 1
} finally {
  report.finished = new Date().toISOString(); await save()
  const files = []
  async function visit(directory) {
    for (const entry of await readdir(directory, { withFileTypes: true })) {
      const file = join(directory, entry.name)
      if (entry.isDirectory()) await visit(file)
      else if (entry.name !== 'manifest.json') files.push({ path: file.slice(output.length + 1), sha256: sha(await readFile(file)) })
    }
  }
  await visit(output)
  await writeFile(join(output, 'manifest.json'), JSON.stringify(files, null, 2) + '\n')
  console.log(`Evidence: ${output}; status: ${report.status}`)
}
