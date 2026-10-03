import { spawn, execFileSync } from 'node:child_process'
import { createWriteStream } from 'node:fs'
import { mkdir, readFile, writeFile } from 'node:fs/promises'
import { join, resolve } from 'node:path'
import { arch, platform, cpus } from 'node:os'
import { createHash } from 'node:crypto'

const hash = bytes => createHash('sha256').update(bytes).digest('hex')

async function sourceIdentity(root) {
  const git = args => execFileSync('git', args, { cwd: root, windowsHide: true })
  const paths = git(['ls-files', '--others', '--exclude-standard', '-z']).toString().split('\0').filter(path => /\.(go|mjs|yml)$/.test(path))
  const untracked = {}
  for (const path of paths) untracked[path] = hash(await readFile(join(root, path)))
  return { trackedDiffSHA256: hash(git(['diff', '--binary', 'HEAD'])), untrackedSourceSHA256: untracked }
}

function stopProcess(child) {
  if (!child.pid) return
  if (process.platform !== 'win32') { child.kill('SIGTERM'); return }
  // Only terminate the process tree created by this step. Killing go.exe alone
  // leaves its test binary running and can contaminate subsequent measurements.
  const killer = spawn('taskkill', ['/PID', String(child.pid), '/T', '/F'], { windowsHide: true, stdio: 'ignore' })
  killer.on('error', () => child.kill('SIGTERM'))
}

/** Runs bounded verification commands and checkpoints evidence after each step. */
export class VerificationRun {
  static async create(root, directory, mode) {
    root = resolve(root)
    directory = resolve(directory)
    await mkdir(directory, { recursive: true })
    const compiler = await readFile(join(root, 'backend/protocol/definition.go'), 'utf8')
    const run = new VerificationRun()
    run.root = root
    run.directory = directory
    run.report = {
      schemaVersion: 1, mode, startedAt: new Date().toISOString(), status: 'not_run',
      commit: execFileSync('git', ['rev-parse', 'HEAD'], { cwd: root, encoding: 'utf8', windowsHide: true }).trim(),
      compilerVersion: compiler.match(/CompilerVersion\s*=\s*"([^"]+)"/)[1],
      goVersion: execFileSync('go', ['version'], { encoding: 'utf8', windowsHide: true }).trim(),
      platform: platform(), architecture: arch(), cpu: cpus()[0]?.model, steps: [],
      source: await sourceIdentity(root),
    }
    await run.save()
    return run
  }

  async save() {
    await writeFile(join(this.directory, 'report.json'), JSON.stringify(this.report, null, 2))
  }

  async execute(name, command, args, { env = process.env, cwd = join(this.root, 'backend'), timeoutMillis = 1800000 } = {}) {
    const logPath = join(this.directory, `${name}.log`)
    const step = { name, command, args, startedAt: new Date().toISOString(), log: logPath, status: 'not_run' }
    this.report.steps.push(step)
    await this.save()
    const log = createWriteStream(logPath)
    const started = performance.now()
    const child = spawn(command, args, { cwd, env, stdio: ['ignore', 'pipe', 'pipe'], windowsHide: true })
    child.stdout.pipe(log, { end: false })
    child.stderr.pipe(log, { end: false })
    let timedOut = false
    let interrupted = false
    const interrupt = () => { interrupted = true; stopProcess(child) }
    process.once('SIGINT', interrupt)
    process.once('SIGTERM', interrupt)
    const timer = setTimeout(() => { timedOut = true; stopProcess(child) }, timeoutMillis)
    const result = await new Promise(resolve => {
      child.once('error', error => resolve({ error: error.message }))
      child.once('close', (code, signal) => resolve({ code, signal }))
    })
    clearTimeout(timer)
    process.off('SIGINT', interrupt)
    process.off('SIGTERM', interrupt)
    await new Promise(resolve => log.end(resolve))
    Object.assign(step, { ...result, timedOut, interrupted, elapsedMillis: Math.round(performance.now() - started), finishedAt: new Date().toISOString(), status: interrupted ? 'inconclusive' : result.code === 0 && !timedOut ? 'passed' : 'failed' })
    await this.save()
    console.log(`${name}: ${step.status} (${step.elapsedMillis} ms); ${logPath}`)
    return step.status === 'passed'
  }

  async finish(status, reason) {
    Object.assign(this.report, { status, reason, finishedAt: new Date().toISOString() })
    await this.save()
    console.log(`Verification evidence: ${join(this.directory, 'report.json')}`)
  }
}
