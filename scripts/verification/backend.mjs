import { execFileSync } from 'node:child_process'
import { createReadStream } from 'node:fs'
import { createInterface } from 'node:readline'
import { join } from 'node:path'
import { prepareRaceEnvironment } from './toolchain.mjs'

const defaultJobs = 2
const maximumJobs = 4
const testParallelism = 2
const testTimeout = '15m'
const commandTimeoutMillis = 16 * 60 * 1000
const testEnvironment = { GOMAXPROCS: '2', GOGC: '100', GOMEMLIMIT: '2GiB', GOWORK: 'off', GOTOOLCHAIN: 'local', GOFLAGS: '-p=2' }

// Prefix groups are disjoint; the final complement also includes fuzz seeds and
// newly added tests. Keep one server process at a time to bound aggregate RSS.
export const serverRaceShards = [
  { name: 'server-gateway', run: '^Test(Gateway|PackageGateway)' },
  { name: 'server-agent', run: '^TestAgent' },
  { name: 'server-protocol', run: '^Test(Protocol|Preset|RecoveredPresets)' },
  { name: 'server-other', skip: '^Test(Gateway|PackageGateway|Agent|Protocol|Preset|RecoveredPresets)' },
]

export function serverShardMatches(shard, name) {
  return (!shard.run || new RegExp(shard.run).test(name)) && (!shard.skip || !new RegExp(shard.skip).test(name))
}

function listPackages(root, env) {
  const module = 'github.com/elysia-api/backend'
  return execFileSync('go', ['list', './...'], { cwd: join(root, 'backend'), env, encoding: 'utf8', timeout: 120000, windowsHide: true })
    .trim().split(/\r?\n/).map(pkg => pkg === module ? '.' : `.${pkg.slice(module.length)}`)
}
const suites = {
  'gemini-stream': [
    ['builtin', './protocol/builtin', '^TestGemini(Package(Stream|History)|TerminalEmpty|FinalContract)'],
    ['gateway', './server', '^Test(Gateway(ParallelGeminiToolSignatures|StrictLateGeminiSignature|MessagesGeminiContinuationRoundTrip|ContinuationPersistenceAndIsolation)|PackageGatewayGeminiContinuationDelivery)$'],
  ],
  output: [
    ['builtin', './protocol/builtin', '^TestOutput(Constraint|NumericEnum|StringEnum)'],
    ['gateway', './server', '^TestGatewayOutputConstraint'],
  ],
  conversion: [
    ['protocol', './protocol'],
    ['builtin', './protocol/builtin'],
    ['gateway', './server', '^Test(Gateway|PackageGateway)'],
  ],
  fast: [],
  all: [],
}

/** Select one ordinary or race pass; full suites include the existing fuzz seeds.
 * Package discovery is injected so the pure plan can be tested without Go builds.
 */
export function buildBackendTestPlan(args, environment = process.env, packages = []) {
  let suite = 'conversion'
  let jobs = defaultJobs
  let isRace = false
  for (const arg of args) {
    if (arg === '--race') isRace = true
    else if (arg.startsWith('--suite=')) suite = arg.slice('--suite='.length)
    else if (arg.startsWith('--jobs=')) jobs = Number(arg.slice('--jobs='.length))
    else throw new Error(`Unknown backend check option: ${arg}`)
  }
  if (!Object.hasOwn(suites, suite)) throw new Error(`Check suite must be one of: ${Object.keys(suites).join(', ')}`)
  if (!Number.isInteger(jobs) || jobs < 1 || jobs > maximumJobs) throw new Error(`Jobs must be between 1 and ${maximumJobs}`)
  const env = { ...environment, ...testEnvironment, CGO_ENABLED: isRace ? '1' : '0' }
  if (isRace) env.GORACE = 'halt_on_error=1'
  const task = (name, pkgs, { run, skip, ...scheduling } = {}) => ({
    name, command: 'go', ...scheduling,
    args: ['test', ...(isRace ? ['-race'] : []), '-buildvcs=false', '-pgo=off', '-p', '2',
      `-parallel=${testParallelism}`, '-count=1', `-timeout=${testTimeout}`, '-json',
      ...(run ? ['-run', run] : []), ...(skip ? ['-skip', skip] : []), ...pkgs],
    options: { env, timeoutMillis: commandTimeoutMillis },
  })
  let tasks
  if (suite === 'fast' || suite === 'all') {
    const nonServer = packages.filter(pkg => pkg !== './server')
    if (!nonServer.length) throw new Error('Full and fast suites require the discovered Go package list')
    tasks = isRace
      ? nonServer.map(pkg => task(pkg === '.' ? 'root' : pkg.slice(2).replaceAll('/', '-'), [pkg], { allowNoTests: true, ...(['./protocol', './protocol/builtin'].includes(pkg) ? { exclusive: true } : {}) }))
      : [task('non-server', nonServer, { slots: jobs, allowNoTests: true })]
    if (suite === 'all') {
      tasks.push(...(isRace
        ? serverRaceShards.map(({ name, ...selection }) => task(name, ['./server'], { ...selection, exclusive: true, group: 'server' }))
        : [task('server', ['./server'], { group: 'server' })]))
    }
  } else {
    tasks = suites[suite].map(([name, pkg, pattern]) => task(name, [pkg], { run: pattern }))
  }
  return { suite, jobs, isRace, env, tasks }
}

async function countTests(path) {
  const counts = { pass: 0, fail: 0, skip: 0 }
  const lines = createInterface({ input: createReadStream(path), crlfDelay: Infinity })
  for await (const line of lines) {
    if (!line.startsWith('{')) continue
    const event = JSON.parse(line)
    if (event.Test && !event.Test.includes('/') && Object.hasOwn(counts, event.Action)) counts[event.Action]++
  }
  return counts
}

/** Run independent package checks with bounded concurrency and retained per-task logs. */
export async function verifyBackend(run, root, args = process.argv.slice(3)) {
  const packages = listPackages(root, { ...process.env, ...testEnvironment, CGO_ENABLED: args.includes('--race') ? '1' : '0' })
  let plan = buildBackendTestPlan(args, process.env, packages)
  if (plan.isRace) {
    const toolchain = await prepareRaceEnvironment(root)
    run.report.toolchain = toolchain.evidence
    plan = buildBackendTestPlan(args, toolchain.env, packages)
  }
  run.report.scope = { suite: plan.suite, jobs: plan.jobs, race: plan.isRace }
  let hasPassed = await run.executeTasks(plan.tasks, plan.jobs)
  run.report.testCounts = {}
  for (const task of run.report.tasks) {
    if (!task.report) continue
    const counts = await countTests(join(run.directory, task.name, `${task.name}.log`))
    run.report.testCounts[task.name] = counts
    if (task.status === 'passed' && counts.pass + counts.skip === 0 && !plan.tasks.find(selected => selected.name === task.name)?.allowNoTests) {
      task.status = 'failed'
      task.reason = 'No tests matched the selected suite'
      hasPassed = false
    }
  }
  await run.finish(hasPassed ? 'passed' : 'failed', 'One pass per selected test; ordinary and sharded race are explicit alternatives')
  if (!hasPassed) process.exitCode = 1
  return hasPassed
}
