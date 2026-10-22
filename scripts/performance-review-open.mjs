import { readFile } from 'node:fs/promises'
import { join, resolve } from 'node:path'
import { spawn } from 'node:child_process'

// Uses argv, not a shell command. The matching captured binary is selected from
// the run identity, including separately built supplemental collectors.
const [directory = '.tmp-dev/performance-review-2026-10-22', run, profile = 'cpu.pprof', sample = 'cpu'] = process.argv.slice(2)
if (!run || /[\\/]/.test(run) || /[\\/]/.test(profile)) throw new Error('Usage: performance-review-open.mjs OUTPUT RUN [PROFILE] [cpu|alloc_space|alloc_objects|inuse_space|inuse_objects]')
if (!['cpu', 'alloc_space', 'alloc_objects', 'inuse_space', 'inuse_objects'].includes(sample)) throw new Error('Unknown sample type')
const root = resolve(directory), result = JSON.parse(await readFile(join(root, 'runs', run, 'result.json')))
const binary = result.binary || join(root, 'binaries', `${result.version}.test.exe`)
const p = spawn('go', ['tool', 'pprof', '-http=127.0.0.1:0', '-relative_percentages', `-sample_index=${sample}`, binary, join(root, 'runs', run, profile)], { windowsHide: true, stdio: 'inherit' })
p.once('error', e => { throw e })
p.once('close', code => { process.exitCode = code ?? 1 })
