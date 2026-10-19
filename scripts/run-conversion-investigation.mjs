import { resolve, join, dirname } from 'node:path'
import { fileURLToPath } from 'node:url'
import { mkdir, readFile } from 'node:fs/promises'
import { VerificationRun } from './verification/runner.mjs'
import { execFileSync } from 'node:child_process'

// This runner intentionally serializes measured workloads. Browser rendering,
// builds and diagnostics must not overlap the unprofiled timing matrix.
const root = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const output = join(root, '.tmp-dev/conversion-flames')
const selected = process.argv[2] || 'build'
const run = await VerificationRun.create(root, join(output, `execution-${selected}`), selected)
const env = { ...process.env, GOGC: '100', GOMEMLIMIT: '8GiB' }
const server = join(output, 'server-matrix.test.exe')
const builtin = join(output, 'builtin.test.exe')
const protocol = join(output, 'protocol.test.exe')
const node = process.execPath
let outcome = 'passed'
const invoke = async (name, command, args, options = {}) => {
  if (!await run.execute(name, command, args, { env, ...options })) {
    await run.finish('failed', name)
    process.exit(1)
  }
}
if (selected === 'build') {
  await invoke('server', 'go', ['test', '-c', '-pgo=off', '-o', server, './server'])
  await invoke('builtin', 'go', ['test', '-c', '-pgo=off', '-o', builtin, './protocol/builtin'])
  await invoke('protocol', 'go', ['test', '-c', '-pgo=off', '-o', protocol, './protocol'])
} else if (selected === 'memory' || selected === 'measure' || selected === 'matrix') {
  let cases = selected === 'memory' ? 'image2,image31,nodes,events' : 'image2,image8,image16,image31,nodes,events,small,small4,small32,image2w4,image8w4,image31w4,image2body,image2media,image31body,image31media,candidates4,candidates16'
  if (selected === 'matrix') {
    const protocols = ['chat','responses','anthropic','gemini']
    cases = protocols.flatMap(source => protocols.map(target => `${source}-${target}`)).concat(protocols.map(target => `chat-${target}-tools`)).join(',')
  }
  await invoke(selected, node, ['scripts/profile-conversion.mjs', 'run', `--mode=${selected === 'matrix' ? 'measure' : selected}`, `--cases=${cases}`, `--binary=${server}`, '--tag=final-'], { cwd: root, timeoutMillis: 7200000 })
  outcome = JSON.parse(await readFile(join(output, 'report.json'))).status
} else if (selected === 'phases' || selected === 'retention' || selected === 'boundaries') {
  const directory = join(output, 'phases'); await mkdir(directory, { recursive: true })
  const available = process.platform === 'win32' ? Number(execFileSync('powershell', ['-NoProfile', '-Command', '(Get-CimInstance Win32_OperatingSystem).FreeVirtualMemory / 1MB'], { encoding: 'utf8', windowsHide: true }).trim()) : 0
  const sizes = available >= 2 ? '2,8,16,31' : available >= 1 ? '2' : ''
  if (sizes === '' && selected !== 'boundaries') { await run.finish('environment_blocked', `Available commitment ${available} GiB`); process.exit(0) }
  if (sizes === '2' && selected !== 'boundaries') { run.report.blockedSizes = { sizesMiB: [8,16,31], availableCommitGiB: available, requiredCommitGiB: 2 }; outcome = 'partially_blocked' }
  const testName = selected === 'boundaries' ? 'TestConversionInvestigationBoundaries' : 'TestConversionInvestigationPhases'
  await invoke(selected, builtin, [`-test.run=^${testName}$`, '-test.v', '-test.timeout=30m'], {
    env: { ...env, ELYSIA_INVESTIGATE: selected, ELYSIA_INVESTIGATE_OUTPUT: directory, ELYSIA_INVESTIGATE_SIZES: sizes }, cwd: join(root, 'backend/protocol/builtin'),
  })
} else if (selected === 'micro') {
  await invoke('scanner-equivalence', protocol, ['-test.run=^TestInvestigationStringScan$', '-test.v'])
  await invoke('scanner-cache-bench', protocol, ['-test.run=^$', '-test.bench=BenchmarkInvestigation', '-test.benchmem', '-test.benchtime=300ms', '-test.count=5'])
  await invoke('scanner-fuzz', 'go', ['test', '-pgo=off', './protocol', '-run=^$', '-fuzz=^FuzzInvestigationStringScan$', '-fuzztime=10s', '-parallel=2'])
} else if (selected === 'ledger') {
  const directory = join(root, '.tmp-dev/conversion-ledger')
  await invoke('prepare', node, ['scripts/profile-ledger.mjs', directory], { cwd: root })
  for (const size of [2, 31]) {
    const available = process.platform === 'win32' ? Number(execFileSync('powershell', ['-NoProfile', '-Command', '(Get-CimInstance Win32_OperatingSystem).FreeVirtualMemory / 1MB'], { encoding: 'utf8', windowsHide: true }).trim()) : 0
    if (available < (size === 31 ? 2 : 1)) { run.report.steps.push({ name: `ledger-${size}MiB`, status: 'environment_blocked', availableCommitGiB: available }); outcome = 'partially_blocked'; continue }
    await invoke(`ledger-${size}MiB`, 'go', ['test', '-pgo=off', './protocol/builtin', '-run=^TestDiagnosticLedger$', '-count=1', '-v'], {
      cwd: join(directory, 'backend'), env: { ...env, ELYSIA_LEDGER_MIB: String(size), ELYSIA_LEDGER_OUTPUT: join(output, `ledger-${size}MiB.json`) },
    })
  }
} else if (selected === 'checks' || selected === 'checks-focused') {
  if (selected === 'checks') await invoke('go-tests', 'go', ['test', '-pgo=off', '-p=1', './protocol/...', './server', '-count=1', '-timeout=10m'])
  else await invoke('continuation', 'go', ['test', '-pgo=off', './server', '-run=^TestGatewayThinkingToolContinuation$', '-count=1', '-timeout=5m'])
  await invoke('vet', 'go', ['vet', '-p=1', './protocol/...', './server'])
  await invoke('snapshot', node, ['--test', 'scripts/source-snapshot.test.mjs'], { cwd: root })
  await invoke('cancellation', server, ['-test.run=^TestConversionHTTPCancellationIsolated$', '-test.v', '-test.timeout=10m'], {
    env: { ...env, ELYSIA_CONVERSION_HTTP_CANCEL: '1', ELYSIA_CONVERSION_HTTP_MIB: '2', ELYSIA_CONVERSION_HTTP_WORKERS: '4' },
  })
} else throw new Error('Choose build, memory, measure, matrix, phases, retention, boundaries, micro, ledger, checks or checks-focused')
await run.finish(outcome)
