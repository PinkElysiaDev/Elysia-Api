import { readFile, readdir, realpath, writeFile } from 'node:fs/promises'
import { createHash } from 'node:crypto'
import { resolve, join } from 'node:path'
import { pathToFileURL } from 'node:url'
import { isDeepStrictEqual } from 'node:util'
import { compareSamples } from './verification/compare.mjs'

const rounds = 5
const runtime = { GOMAXPROCS: '2', GOGC: '100', GOMEMLIMIT: '2GiB', CGO_ENABLED: '0', GOTOOLCHAIN: 'local' }
const driverHash = 'a449b44c32a85b505392044b9d00e8f6e8785b33448d4f1829507aac2beb5d25'
const providerHash = 'e6f143b21a6a1ed778660ba9cd311994415d80ecc4791199496bad3da590b3ad'
const providerSourceHash = '0b33f2f85e0f3225737e701609c3e160323aa0fd8a439fe3fd0fd272d9ae3589'
const baselineHash = '9dd2b44b844ccf758d561eb9825d33167c7f3036eaed886c44bce2194a6ca055'
const baselineInstrumentedHash = 'f675a69821dafdc7f42bb4fac691ab6a8d2c4e06fa401ef4af567484cefeb19f'
const baselineSourceHash = 'c6c398515bf0ca229861207085a09d18f1a72a3eeaf3ee14e678fa1c31c04fa5'
const versions = ['C-default', 'C-production']
const requests = { short: 4096, history: 256, tools: 1024, burst: 4096, stream: 128, image: 128, 'cross-json': 512, 'cross-stream': 128 }
const streamScenes = new Set(['stream', 'cross-stream'])
const packageIDs = ['anthropic-messages', 'google-generate-content', 'openai-chat-completions', 'openai-responses']
const counterMetrics = new Set(['bytes', 'allocations', 'gcPauseNS'])
const combinationIdentityFields = ['sourceHash', 'targetHash', 'contextHash', 'policyHash', 'compilerVersion', 'kind', 'bindingHash', 'sourceSamplesHash', 'targetSamplesHash']
const normalizePath = path => path.replaceAll('\\', '/').replace(/\/+$/, '')
const metrics = {
  cpuMS: row => row.Metrics.CPUTimeNS / row.Requests / 1e6,
  throughput: row => row.Requests * 1000 / row.ElapsedMS,
  p50MS: row => row.LatencyP50P95P99NS[0] / 1e6,
  p95MS: row => row.LatencyP50P95P99NS[1] / 1e6,
  p99MS: row => row.LatencyP50P95P99NS[2] / 1e6,
  firstFrameMS: row => row.FirstFrameP50P95P99NS[0] / 1e6,
  bytes: row => row.Metrics.Allocated / row.Requests,
  allocations: row => row.Metrics.Allocations / row.Requests,
  gcPauseNS: row => row.Metrics.GCPauseNS / row.Requests,
  gcCycles: row => row.Metrics.GCCycles,
  peakRSS: row => row.Metrics.PeakWorkingSet,
  recoveryRSS: row => row.Recovery.at(-1).Metrics.WorkingSet,
  recoveryHeap: row => row.Recovery.at(-1).Metrics.Heap,
}

function requireEvidence(condition, reason) {
  if (!condition) throw new Error(reason)
}

function orderConfigurationEvidence(configuration) {
  return { ...configuration, bindings: configuration.bindings.map(binding => ({
    ...binding,
    combinations: binding.combinations?.toSorted((first, second) => {
      const firstKey = JSON.stringify(combinationIdentityFields.map(name => first[name]))
      const secondKey = JSON.stringify(combinationIdentityFields.map(name => second[name]))
      return firstKey < secondKey ? -1 : firstKey > secondKey ? 1 : 0
    }),
  })) }
}

function sameConfiguration(first, second) {
  return isDeepStrictEqual(orderConfigurationEvidence(first), orderConfigurationEvidence(second))
}

function checkEnvironment(run) {
  const env = run.environment
  for (const [key, value] of Object.entries(runtime)) requireEvidence(env[key] === value, `Runtime changed: ${key}`)
  const expected = {
    ELYSIA_CONVERSION_HTTP_EXECUTION: 'packages', ELYSIA_FLAME_MODE: 'measure',
    ELYSIA_FLAME_REQUESTS: String(requests[run.scenario]), ELYSIA_FLAME_REPEATS: '1',
    ELYSIA_FLAME_FRAMES: '256', ELYSIA_FLAME_FRAME_BYTES: '256', ELYSIA_FLAME_RECOVERY_MS: '1000',
    ELYSIA_FLAME_VERIFY_EXACT: '1', ELYSIA_CONVERSION_HTTP_MIB: '2', ELYSIA_CONVERSION_HTTP_CANDIDATES: '1',
    ELYSIA_CONVERSION_HTTP_LOG: 'off', ELYSIA_CONVERSION_HTTP_WORKERS: run.scenario === 'burst' ? '4' : '1',
    ELYSIA_FLAME_WARMUP: run.scenario === 'image' || streamScenes.has(run.scenario) ? '8' : '32',
    ELYSIA_FLAME_STREAM: streamScenes.has(run.scenario) ? 'sse' : 'json',
    ELYSIA_FLAME_WORKLOAD: run.scenario === 'image' ? 'image' : 'small',
    ELYSIA_FLAME_REQUEST: run.scenario === 'history' ? 'history256k' : run.scenario === 'tools' ? 'tools32' : 'short',
  }
  for (const [key, value] of Object.entries(expected)) requireEvidence(env[key] === value, `Workload changed: ${run.scenario}/${key}`)
}

function checkProduction(run, proof) {
  const configuration = run.configuration
  requireEvidence(proof.Execution === 'production' && proof.Compiler && Object.keys(proof.Policies ?? {}).length > 0,
    'Production execution lacks compiler/policy evidence')
  requireEvidence(isDeepStrictEqual(Object.keys(proof.Packages).sort(), packageIDs), 'Production defaults changed')
  requireEvidence(isDeepStrictEqual(proof, configuration.execution) && isDeepStrictEqual(proof.Packages, configuration.protocols),
    'Production execution differs from active configuration')
  requireEvidence(isDeepStrictEqual(configuration, run.finalConfiguration), 'Final production identity changed or is missing')
  requireEvidence(configuration.route.ingressPath === run.environment.ELYSIA_FLAME_GATEWAY_PATH, 'Production ingress differs from Spec')
  const { source, target, transport } = configuration.route
  const matching = configuration.bindings.filter(entry => entry.kind === 'source' && entry.sourceId === 's' && !entry.unbound)
  requireEvidence(matching.length === 1, 'Production source binding is missing or ambiguous')
  const binding = matching[0]
  requireEvidence(binding.binding.protocolId === target && binding.binding.revisionHash === proof.Packages[target]
    && binding.binding.transports.includes(transport), 'Production binding revision/transport changed')
  const matchingPolicies = binding.combinations.filter(report => report.passed && report.kind === 'offline'
    && report.sourceHash === proof.Packages[source] && report.targetHash === proof.Packages[target]
    && report.compilerVersion === proof.Compiler && report.sourceSamplesHash === configuration.samples[source]
    && report.targetSamplesHash === configuration.samples[target] && (!report.transport || report.transport === transport)
    && proof.Policies[[report.sourceHash, report.targetHash, report.contextHash].join(':')] === report.policyHash)
  requireEvidence(matchingPolicies.length > 0, 'Production route policy evidence is stale')
}

function checkRun(artifact, preflight, condition) {
  const { run, rows, reloaded, execution } = artifact
  const label = artifact.name
  requireEvidence(run.setupOnly !== true && run.performanceAcceptance !== false, `Setup-only workload is not performance evidence: ${label}`)
  requireEvidence(run.status === 'passed' && run.phase === preflight.phase && run.mode === 'measure', `Failed or mismatched window: ${label}`)
  requireEvidence(run.stage === condition.name && run.health.commit === condition.commit && execution.Commit === condition.commit,
    `Source identity changed: ${label}`)
  const binaryHash = preflight.phase === 'standalone' ? condition.binarySHA256 : condition.instrumentedSHA256
  requireEvidence(run.binarySHA256 === binaryHash && run.driverSHA256 === driverHash && run.provider.sha256 === providerHash
    && run.driverCWD === preflight.driverCWD, `Binary or fixture identity changed: ${label}`)
  requireEvidence(run.runnerSHA256 === preflight.processRunnerSHA256 && run.allocationRunnerSHA256 === preflight.allocationRunnerSHA256,
    `Measurement harness changed: ${label}`)
  requireEvidence(run.children.length === 2 && run.children.every(child => child.exitCode === 0), `Measured child failed: ${label}`)
  requireEvidence(isDeepStrictEqual(run.configuration, reloaded), `Reload identity changed: ${label}`)
  requireEvidence(isDeepStrictEqual(execution.Packages, run.configuration.protocols), `Execution packages changed: ${label}`)
  if (run.version === 'C-production') checkProduction(run, execution)
  else requireEvidence(execution.Execution === 'legacy', 'D0 was relabelled as production')
  checkEnvironment(run)
  requireEvidence(rows.length === 1, `Expected exactly one measured repeat: ${label}`)
  const row = rows[0]
  requireEvidence(row.Requests === requests[run.scenario] && row.UpstreamCalls === row.Requests && row.Repeat === 0
    && row.Mode === 'measure' && row.Workers === (run.scenario === 'burst' ? 4 : 1)
    && row.Stream === streamScenes.has(run.scenario) && row.SizeMiB === 2 && row.ElapsedMS > 0,
  `Measured workload mismatch: ${label}`)
  requireEvidence(row.Observations.length === row.Requests && row.Recovery.at(-1)?.IdleMS >= 1000,
    `Missing request/recovery observations: ${label}`)
  requireEvidence(row.Observations.every(observation => Number.isFinite(observation.LatencyNS) && observation.LatencyNS >= 0),
    `Invalid request observation: ${label}`)
  // The fixed Windows clock can place a completed request within one tick.
  const zeroLatencyObservations = row.Observations.filter(observation => observation.LatencyNS === 0).length
  const hasCounters = preflight.phase === 'counters'
  requireEvidence(['Allocated', 'Allocations', 'GCPauseNS'].every(name => run.metricsAvailability[name] === hasCounters),
    `Metric availability changed: ${label}`)
  const samples = {}
  for (const [name, readMetric] of Object.entries(metrics)) {
    if (!hasCounters && counterMetrics.has(name) || name === 'firstFrameMS' && !row.Stream) continue
    const value = readMetric(row)
    requireEvidence(Number.isFinite(value) && value >= 0, `Invalid metric: ${label}/${name}`)
    samples[name] = value
  }
  return { path: label, version: run.version, scenario: run.scenario, binarySHA256: run.binarySHA256,
    measurementSHA256: artifact.measurementSHA256, zeroLatencyObservations,
    provider: run.provider.identity, execution, configuration: run.configuration, samples }
}

/** Validate complete five-round evidence before computing any ratios. */
export function validateProductionPhase(preflight, artifacts) {
  requireEvidence(['standalone', 'counters'].includes(preflight.phase) && preflight.rounds === rounds, 'Invalid measurement phase or round count')
  requireEvidence(isDeepStrictEqual(preflight.runtime, runtime) && preflight.driverSHA256 === driverHash
    && preflight.providerSHA256 === providerHash && preflight.providerSourceSHA256 === providerSourceHash
    && preflight.baselineBinarySHA256 === baselineHash && preflight.baselineSourceSHA256 === baselineSourceHash,
  'Cutover fixture/runtime identity differs from D0')
  requireEvidence(isDeepStrictEqual(Object.keys(preflight.scenarios).sort(), Object.keys(requests).sort()), 'Expected all eight scenarios')
  requireEvidence(preflight.conditions.length === 2 && versions.every(version => preflight.conditions.filter(item => item.version === version).length === 1),
    'Expected exactly D0 and one production candidate')
  const baseline = preflight.conditions.find(item => item.version === 'C-default')
  requireEvidence(baseline.name === 'backend-p0-default' && baseline.commit === 'cabd6b04-source-c6c398515bf0'
    && baseline.sourceSHA256 === baselineSourceHash && baseline.binarySHA256 === baselineHash && baseline.instrumentedSHA256 === baselineInstrumentedHash,
    'Baseline is not the agreed D0 archive')
  requireEvidence(normalizePath(preflight.driverCWD) === normalizePath(baseline.archive) + '/C/source/backend/server',
    'Fixed driver is not using the D0 fixture archive')
  for (const condition of preflight.conditions) requireEvidence(condition.commit && /^[a-f0-9]{64}$/.test(condition.sourceSHA256)
    && /^[a-f0-9]{64}$/.test(condition.binarySHA256) && /^[a-f0-9]{64}$/.test(condition.instrumentedSHA256), 'Incomplete frozen identity')
  const expected = new Map()
  for (const scenario of Object.keys(requests)) for (let round = 1; round <= rounds; round++) for (const condition of preflight.conditions) {
    expected.set(`${scenario}-${round}-${condition.name}`, { scenario, condition })
  }
  requireEvidence(artifacts.length === expected.size, `Expected ${expected.size} windows, got ${artifacts.length}`)
  const records = []
  const measurements = new Set()
  for (const artifact of artifacts) {
    const match = expected.get(artifact.name)
    requireEvidence(match && artifact.run.scenario === match.scenario && artifact.run.version === match.condition.version,
      `Unexpected or duplicate window: ${artifact.name}`)
    requireEvidence(/^[a-f0-9]{64}$/.test(artifact.measurementSHA256), `Missing raw measurement identity: ${artifact.name}`)
    requireEvidence(!measurements.has(artifact.measurementSHA256), `Reused measurement: ${artifact.name}`)
    measurements.add(artifact.measurementSHA256)
    records.push(checkRun(artifact, preflight, match.condition))
    expected.delete(artifact.name)
  }
  const groups = {}
  for (const record of records) {
    const group = groups[record.scenario] ??= {}
    const samples = group[record.version] ??= []
    if (samples.length) requireEvidence(isDeepStrictEqual(samples[0].execution, record.execution)
      && sameConfiguration(samples[0].configuration, record.configuration) && isDeepStrictEqual(samples[0].provider, record.provider),
    `Identity drift between rounds: ${record.scenario}/${record.version}`)
    samples.push(record)
  }
  for (const group of Object.values(groups)) requireEvidence(isDeepStrictEqual(group['C-default'][0].provider, group['C-production'][0].provider),
    'D0 and production used different upstream fixtures')
  return { preflight, records, groups }
}

async function loadPhase(directory) {
  const read = async path => JSON.parse(await readFile(path, 'utf8'))
  const preflight = await read(join(directory, 'cutover-preflight.json'))
  const artifacts = []
  for (const entry of await readdir(directory, { withFileTypes: true })) {
    if (entry.name === 'cutover-preflight.json') continue
    requireEvidence(entry.isDirectory(), `Unexpected measurement artifact: ${entry.name}`)
    const folder = join(directory, entry.name)
    const [run, rawMeasurements, reloaded, execution] = await Promise.all([
      read(join(folder, 'run.json')), readFile(join(folder, 'measurements.json')),
      read(join(folder, 'reloaded-identity.json')), read(join(folder, 'execution.json')),
    ])
    artifacts.push({ name: entry.name, run, rows: JSON.parse(rawMeasurements.toString('utf8')), reloaded, execution,
      measurementSHA256: createHash('sha256').update(rawMeasurements).digest('hex') })
  }
  return validateProductionPhase(preflight, artifacts)
}

/** Compare one initial batch and at most one equally sized confirmation. */
export function compareProductionPhases(initial, confirmation) {
  if (confirmation) requireEvidence(isDeepStrictEqual(initial.preflight, confirmation.preflight), 'Confirmation changed source, harness or workload identity')
  if (confirmation) {
    const originalMeasurements = new Set(initial.records.map(record => record.measurementSHA256))
    requireEvidence(confirmation.records.every(record => !originalMeasurements.has(record.measurementSHA256)),
      'Confirmation reused an original measurement')
  }
  const comparisons = {}
  for (const scenario of Object.keys(requests)) {
    if (confirmation) for (const version of versions) {
      const before = initial.groups[scenario][version][0], after = confirmation.groups[scenario][version][0]
      requireEvidence(isDeepStrictEqual(before.execution, after.execution) && sameConfiguration(before.configuration, after.configuration)
        && isDeepStrictEqual(before.provider, after.provider), `Confirmation execution drift: ${scenario}/${version}`)
    }
    const groups = versions.map(version => [...initial.groups[scenario][version], ...(confirmation?.groups[scenario][version] ?? [])])
    comparisons[scenario] = {}
    for (const name of Object.keys(metrics)) {
      if (initial.preflight.phase !== 'counters' && counterMetrics.has(name)) {
        comparisons[scenario][name] = { assessment: 'not-collected', reason: 'standalone process has no allocation counter instrumentation' }
        continue
      }
      if (name === 'firstFrameMS' && !streamScenes.has(scenario)) {
        comparisons[scenario][name] = { assessment: 'not-applicable', reason: 'JSON response has no stream frame timing' }
        continue
      }
      const result = compareSamples(groups[0].map(row => row.samples[name]), groups[1].map(row => row.samples[name]))
      if (name === 'throughput') {
        if (result.assessment === 'regression') result.assessment = 'improvement'
        else if (result.assessment === 'improvement') result.assessment = 'regression'
      }
      comparisons[scenario][name] = result
    }
  }
  return { pair: versions, estimator: 'Seeded bootstrap ratio of independent medians; unchanged compareSamples estimator.',
    confirmationCombined: Boolean(confirmation), preflight: initial.preflight,
    records: [...initial.records, ...(confirmation?.records ?? [])].map(({ configuration, ...record }) => record), comparisons }
}

async function main() {
  const options = new Map()
  const names = new Set(['standalone', 'counters', 'standalone-confirmation', 'counters-confirmation', 'output'])
  for (const argument of process.argv.slice(2)) {
    const match = /^--([^=]+)=(.+)$/.exec(argument)
    if (!match || !names.has(match[1]) || options.has(match[1])) throw new Error('Use --standalone=DIR --counters=DIR --output=FILE [--standalone-confirmation=DIR] [--counters-confirmation=DIR]')
    options.set(match[1], resolve(match[2]))
  }
  requireEvidence(options.has('output') && (options.has('standalone') || options.has('counters')), 'An output and initial measurement directory are required')
  const report = { phases: {} }
  for (const phase of ['standalone', 'counters']) {
    const directory = options.get(phase), confirmation = options.get(phase + '-confirmation')
    requireEvidence(!confirmation || directory && confirmation !== directory, 'Confirmation requires a distinct initial directory')
    if (!directory) continue
    if (confirmation) requireEvidence(await realpath(directory) !== await realpath(confirmation), 'Confirmation requires a distinct initial directory')
    const initial = await loadPhase(directory)
    requireEvidence(initial.preflight.phase === phase, 'Directory measurement phase differs from CLI selection')
    report.phases[phase] = compareProductionPhases(initial, confirmation ? await loadPhase(confirmation) : undefined)
  }
  if (report.phases.standalone && report.phases.counters) requireEvidence(isDeepStrictEqual(report.phases.standalone.preflight.conditions,
    report.phases.counters.preflight.conditions), 'Standalone and counters use different frozen candidates')
  await writeFile(options.get('output'), JSON.stringify(report, null, 2) + '\n', { flag: 'wx' })
  console.log(options.get('output'))
}

if (process.argv[1] && pathToFileURL(resolve(process.argv[1])).href === import.meta.url) await main()
