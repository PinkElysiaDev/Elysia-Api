import { spawn, execFileSync } from 'node:child_process'
import { createWriteStream } from 'node:fs'
import { mkdir, readFile, writeFile } from 'node:fs/promises'
import { join, resolve } from 'node:path'
import { arch, platform, cpus } from 'node:os'
import { createHash } from 'node:crypto'

const hash = bytes => createHash('sha256').update(bytes).digest('hex')

async function sourceIdentity(root) {
  const git = args => execFileSync('git', args, { cwd: root, windowsHide: true })
  const baseCommit = git(['rev-parse', 'HEAD']).toString().trim()
  const paths = git(['ls-files', '--others', '--exclude-standard', '-z']).toString().split('\0').filter(path => /\.(go|mjs|yml)$/.test(path))
  const untracked = {}
  for (const path of paths) untracked[path] = hash(await readFile(join(root, path)))
  return { baseCommit, trackedDiffSHA256: hash(git(['diff', '--binary', baseCommit])), untrackedSourceSHA256: untracked }
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
    const compiler = await readFile(join(cwd, 'protocol/definition.go'), 'utf8').catch(error => { if (error.code === 'ENOENT') return ''; throw error })
    const step = { name, command, args, startedAt: new Date().toISOString(), log: logPath, status: 'not_run', source: await sourceIdentity(this.root), compilerVersion: compiler.match(/CompilerVersion\s*=\s*"([^"]+)"/)?.[1] }
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

  /** Runs isolated child reports with bounded slots; exclusive tasks form barriers. */
  async executeTasks(tasks, slots = 4) {
    const names = new Set()
    for (const task of tasks) {
      if (!/^[\w-]+$/.test(task.name) || names.has(task.name) || (task.after || []).some(name => !names.has(name))) throw new Error('Tasks require unique names and preceding dependencies')
      task.slots = task.exclusive ? slots : task.slots || 1
      if (!Number.isInteger(task.slots) || task.slots < 1 || task.slots > slots) throw new Error('Invalid task slot count')
      names.add(task.name)
    }
    const queuedAt = performance.now()
    const results = new Map()
    const running = new Map()
    let used = 0
    let interrupted = false
    const interrupt = () => { interrupted = true }
    process.once('SIGINT', interrupt)
    process.once('SIGTERM', interrupt)
    const start = async task => {
      const waitedMillis = Math.round(performance.now() - queuedAt)
      const child = await VerificationRun.create(this.root, join(this.directory, task.name), task.name)
      child.report.scheduling = { slots: task.slots, exclusive: !!task.exclusive, waitedMillis }
      if (interrupted) {
        await child.finish('not_run', 'Interrupted before process launch')
        return { name: task.name, status: 'not_run', reason: 'interrupted' }
      }
      const passed = await child.execute(task.name, task.command, task.args || [], task.options)
      await child.finish(passed ? 'passed' : 'failed', 'Isolated verification task')
      return { name: task.name, status: child.report.status, report: join(child.directory, 'report.json'), ...child.report.scheduling }
    }
    try {
      const pending = [...tasks]
      while (pending.length || running.size) {
        for (let index = 0; index < pending.length;) {
          const task = pending[index]
          const dependencies = task.after || []
          if (interrupted || dependencies.some(name => results.has(name) && results.get(name).status !== 'passed')) {
            results.set(task.name, { name: task.name, status: 'not_run', reason: interrupted ? 'interrupted' : 'dependency failed' })
            pending.splice(index, 1)
            continue
          }
          if (task.exclusive && running.size) break
          const isBlocked = dependencies.some(name => !results.has(name)) || used + task.slots > slots || [...running.values()].some(entry => task.group && entry.task.group === task.group)
          if (isBlocked) { if (task.exclusive) break; index++; continue }
          pending.splice(index, 1)
          used += task.slots
          const promise = start(task).catch(error => ({ name: task.name, status: 'failed', reason: error.message })).then(result => {
            results.set(task.name, result)
            used -= task.slots
            running.delete(task.name)
          })
          running.set(task.name, { task, promise })
          if (task.exclusive) break
        }
        if (running.size) await Promise.race([...running.values()].map(entry => entry.promise))
      }
    } finally {
      process.off('SIGINT', interrupt)
      process.off('SIGTERM', interrupt)
    }
    this.report.tasks = tasks.map(task => results.get(task.name))
    await this.save()
    return this.report.tasks.every(task => task.status === 'passed')
  }
}
