import { createReadStream } from 'node:fs'
import { createInterface } from 'node:readline'
import { join } from 'node:path'
import { prepareRaceEnvironment } from './toolchain.mjs'

const defaultJobs = 2
const maximumJobs = 4
const testParallelism = 2
const testTimeout = '60m'
const commandTimeoutMillis = 65 * 60 * 1000
const testEnvironment = { GOMAXPROCS: '2', GOGC: '100', GOMEMLIMIT: '2GiB', GOWORK: 'off', GOTOOLCHAIN: 'local', GOFLAGS: '' }
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
  all: [['all', './...']],
}

/** Select one ordinary or race pass; full suites include the existing fuzz seeds. */
export function buildBackendTestPlan(args, environment = process.env) {
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
  const tasks = suites[suite].map(([name, pkg, pattern]) => ({
    name, command: 'go',
    args: ['test', ...(isRace ? ['-race'] : []), '-buildvcs=false', '-pgo=off', '-p', String(suite === 'all' ? jobs : 1),
      `-parallel=${testParallelism}`, '-count=1', `-timeout=${testTimeout}`, '-json', ...(pattern ? ['-run', pattern] : []), pkg],
    options: { env, timeoutMillis: commandTimeoutMillis },
  }))
  return { suite, jobs, isRace, tasks }
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
  let plan = buildBackendTestPlan(args)
  if (plan.isRace) {
    const toolchain = await prepareRaceEnvironment(root)
    run.report.toolchain = toolchain.evidence
    plan = buildBackendTestPlan(args, toolchain.env)
  }
  run.report.scope = { suite: plan.suite, jobs: plan.jobs, race: plan.isRace }
  let hasPassed = await run.executeTasks(plan.tasks, plan.jobs)
  run.report.testCounts = {}
  for (const task of run.report.tasks) {
    if (!task.report) continue
    const counts = await countTests(join(run.directory, task.name, `${task.name}.log`))
    run.report.testCounts[task.name] = counts
    if (task.status === 'passed' && counts.pass + counts.skip === 0) {
      task.status = 'failed'
      task.reason = 'No tests matched the selected suite'
      hasPassed = false
    }
  }
  await run.finish(hasPassed ? 'passed' : 'failed', 'One pass per selected package; ordinary and race are explicit alternatives')
  if (!hasPassed) process.exitCode = 1
  return hasPassed
}
