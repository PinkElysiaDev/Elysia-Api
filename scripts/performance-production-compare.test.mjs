import assert from 'node:assert/strict'
import test from 'node:test'
import { mkdtemp, mkdir, readFile, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join, resolve } from 'node:path'
import { execFile } from 'node:child_process'
import { promisify } from 'node:util'
import { createHash } from 'node:crypto'
import { compareSamples } from './verification/compare.mjs'
import { compareProductionPhases, validateProductionPhase } from './performance-production-compare.mjs'

const hash = letter => letter.repeat(64)
const baselineHash = '9dd2b44b844ccf758d561eb9825d33167c7f3036eaed886c44bce2194a6ca055'
const baselineSourceHash = 'c6c398515bf0ca229861207085a09d18f1a72a3eeaf3ee14e678fa1c31c04fa5'
const requests = { short: 4096, history: 256, tools: 1024, burst: 4096, stream: 128, image: 128, 'cross-json': 512, 'cross-stream': 128 }
const packages = Object.fromEntries(['anthropic-messages', 'google-generate-content', 'openai-chat-completions', 'openai-responses'].map((name, index) => [name, String(index).repeat(64)]))

function fixture(phase = 'standalone') {
  const preflight = {
    phase, rounds: 5, runtime: { GOMAXPROCS: '2', GOGC: '100', GOMEMLIMIT: '2GiB', CGO_ENABLED: '0', GOTOOLCHAIN: 'local' },
    driverSHA256: 'a449b44c32a85b505392044b9d00e8f6e8785b33448d4f1829507aac2beb5d25',
    providerSHA256: 'e6f143b21a6a1ed778660ba9cd311994415d80ecc4791199496bad3da590b3ad',
    providerSourceSHA256: '0b33f2f85e0f3225737e701609c3e160323aa0fd8a439fe3fd0fd272d9ae3589',
    baselineBinarySHA256: baselineHash, baselineSourceSHA256: baselineSourceHash,
    driverCWD: 'frozen/d0/C/source/backend/server', processRunnerSHA256: hash('a'), allocationRunnerSHA256: hash('b'),
    scenarios: Object.fromEntries(Object.entries(requests).map(([name, count]) => [name, { requests: count }])),
    conditions: [
      { name: 'backend-p0-default', archive: 'frozen/d0', version: 'C-default', commit: 'cabd6b04-source-c6c398515bf0', sourceSHA256: baselineSourceHash, binarySHA256: baselineHash,
        instrumentedSHA256: 'f675a69821dafdc7f42bb4fac691ab6a8d2c4e06fa401ef4af567484cefeb19f' },
      { name: 'candidate-production', version: 'C-production', commit: 'frozen-candidate', sourceSHA256: hash('d'), binarySHA256: hash('e'), instrumentedSHA256: hash('f') },
    ],
  }
  const artifacts = []
  for (const [scene, count] of Object.entries(requests)) {
    const isStream = scene === 'stream' || scene === 'cross-stream'
    const source = scene === 'image' ? 'anthropic-messages' : 'openai-chat-completions'
    const target = scene === 'image' || scene.startsWith('cross-') ? 'anthropic-messages' : source
    for (const condition of preflight.conditions) for (let round = 1; round <= 5; round++) {
      const isProduction = condition.version === 'C-production'
      const report = { passed: true, kind: 'offline', sourceHash: packages[source], targetHash: packages[target],
        compilerVersion: 'compiler', sourceSamplesHash: 'samples-' + source, targetSamplesHash: 'samples-' + target,
        contextHash: 'context', policyHash: 'policy' }
      const proof = { Execution: isProduction ? 'production' : 'legacy', Commit: condition.commit, Packages: packages }
      if (isProduction) Object.assign(proof, { Compiler: 'compiler', Policies: { [[report.sourceHash, report.targetHash, report.contextHash].join(':')]: 'policy' } })
      const configuration = { protocols: packages, bindings: [] }
      if (isProduction) Object.assign(configuration, {
        execution: proof, route: { source, target, transport: isStream ? 'sse' : 'http_json', ingressPath: '/gateway/spec-path' },
        samples: { [source]: 'samples-' + source, [target]: 'samples-' + target },
        bindings: [{ kind: 'source', sourceId: 's', binding: { protocolId: target, revisionHash: packages[target], transports: ['http_json', 'sse'] }, combinations: [report] }],
      })
      const run = { status: 'passed', phase, mode: 'measure', version: condition.version, stage: condition.name,
        scenario: scene, health: { commit: condition.commit }, configuration,
        binarySHA256: phase === 'standalone' ? condition.binarySHA256 : condition.instrumentedSHA256,
        driverSHA256: preflight.driverSHA256, driverCWD: preflight.driverCWD,
        provider: { sha256: preflight.providerSHA256, identity: { Execution: 'packages', Compiler: 'fixed', Commit: 'fixed', Packages: { [target]: 'fixed-package' } } },
        runnerSHA256: preflight.processRunnerSHA256, allocationRunnerSHA256: preflight.allocationRunnerSHA256,
        children: [{ exitCode: 0 }, { exitCode: 0 }],
        metricsAvailability: { Allocated: phase === 'counters', Allocations: phase === 'counters', GCPauseNS: phase === 'counters' },
        environment: { ...preflight.runtime, ELYSIA_CONVERSION_HTTP_EXECUTION: 'packages', ELYSIA_FLAME_MODE: 'measure',
          ELYSIA_FLAME_REQUESTS: String(count), ELYSIA_FLAME_REPEATS: '1', ELYSIA_FLAME_FRAMES: '256', ELYSIA_FLAME_FRAME_BYTES: '256',
          ELYSIA_FLAME_RECOVERY_MS: '1000', ELYSIA_FLAME_VERIFY_EXACT: '1', ELYSIA_CONVERSION_HTTP_MIB: '2',
          ELYSIA_CONVERSION_HTTP_CANDIDATES: '1', ELYSIA_CONVERSION_HTTP_LOG: 'off', ELYSIA_CONVERSION_HTTP_WORKERS: scene === 'burst' ? '4' : '1',
          ELYSIA_FLAME_WARMUP: scene === 'image' || isStream ? '8' : '32', ELYSIA_FLAME_STREAM: isStream ? 'sse' : 'json',
          ELYSIA_FLAME_WORKLOAD: scene === 'image' ? 'image' : 'small', ELYSIA_FLAME_REQUEST: scene === 'history' ? 'history256k' : scene === 'tools' ? 'tools32' : 'short',
          ELYSIA_FLAME_GATEWAY_PATH: '/gateway/spec-path' },
      }
      if (isProduction) run.finalConfiguration = structuredClone(configuration)
      const started = 1000000 + artifacts.length * 1000
      const row = { Mode: 'measure', StartedUnixNS: started, FinishedUnixNS: started + 100,
        Requests: count, UpstreamCalls: count, Repeat: 0, Workers: scene === 'burst' ? 4 : 1,
        Stream: isStream, SizeMiB: 2, ElapsedMS: isProduction ? 80 : 100,
        Observations: Array(count).fill({ LatencyNS: 1000 }), Recovery: [{ IdleMS: 1000, Metrics: { WorkingSet: 100, Heap: 50 } }],
        Metrics: { CPUTimeNS: isProduction ? 110 : 100, Allocated: 1000, Allocations: 200, GCPauseNS: 0, GCCycles: 0, PeakWorkingSet: 200 },
        LatencyP50P95P99NS: [100, 200, 300], FirstFrameP50P95P99NS: [80, 180, 280] }
      const name = `${scene}-${round}-${condition.name}`
      artifacts.push({ name, run, rows: [row], reloaded: structuredClone(configuration), execution: proof,
        measurementSHA256: createHash('sha256').update(JSON.stringify([row])).digest('hex') })
    }
  }
  return { preflight, artifacts }
}

test('requires all eight scenarios and exactly five rounds with unchanged identities', () => {
  const { preflight, artifacts } = fixture()
  const result = validateProductionPhase(preflight, artifacts)
  assert.equal(result.records.length, 80)
  assert.deepEqual(Object.keys(result.groups), Object.keys(requests))
  assert.throws(() => validateProductionPhase(preflight, artifacts.slice(1)), /Expected 80 windows/)
  assert.throws(() => validateProductionPhase(preflight, [...artifacts.slice(1), artifacts[1]]), /Unexpected or duplicate/)
})

test('failed measurements, stale proofs and altered workloads are never skipped', () => {
  const changes = [
    artifact => { artifact.run.setupOnly = true },
    artifact => { artifact.run.status = 'failed' },
    artifact => { artifact.run.children[0].exitCode = 1 },
    artifact => { artifact.run.driverSHA256 = hash('9') },
    artifact => { artifact.run.binarySHA256 = hash('9') },
    artifact => { artifact.run.driverCWD = 'mutable/tree' },
    artifact => { artifact.run.environment.ELYSIA_FLAME_REQUESTS = '1' },
    artifact => { artifact.rows[0].UpstreamCalls-- },
    artifact => { artifact.rows[0].Metrics.CPUTimeNS = Number.NaN },
    artifact => { artifact.rows[0].Metrics.PeakWorkingSet = -1 },
    artifact => { artifact.run.finalConfiguration = undefined },
    artifact => { artifact.execution.Execution = 'legacy' },
    artifact => { artifact.execution.Policies = {} },
  ]
  for (const change of changes) {
    const { preflight, artifacts } = fixture()
    change(artifacts.find(artifact => artifact.run.version === 'C-production'))
    assert.throws(() => validateProductionPhase(preflight, artifacts))
  }
})

test('retains zero clock-tick observations while rejecting invalid timings', () => {
  const { preflight, artifacts } = fixture()
  artifacts[0].rows[0].Observations[0] = { LatencyNS: 0 }
  const before = structuredClone(artifacts)
  const result = validateProductionPhase(preflight, artifacts)
  assert.equal(result.records[0].zeroLatencyObservations, 1)
  assert.equal(result.records[1].zeroLatencyObservations, 0)
  assert.deepEqual(artifacts, before)
  for (const latency of [-1, Number.NaN, Number.POSITIVE_INFINITY, undefined, null, '0']) {
    const changed = structuredClone(artifacts)
    changed[0].rows[0].Observations[0] = { LatencyNS: latency }
    assert.throws(() => validateProductionPhase(preflight, changed), /Invalid request observation/)
  }
})

test('combination report order is immaterial but every report and nested check remains exact', () => {
  const { preflight, artifacts } = fixture()
  const candidates = artifacts.filter(artifact => artifact.run.version === 'C-production' && artifact.run.scenario === 'short')
  for (const artifact of candidates) {
    const reports = artifact.run.configuration.bindings[0].combinations
    reports.push({ ...structuredClone(reports[0]), contextHash: 'other-context', checks: [{ passed: true }, { passed: false }] })
    artifact.run.finalConfiguration = structuredClone(artifact.run.configuration)
    artifact.reloaded = structuredClone(artifact.run.configuration)
  }
  const reordered = candidates[1]
  reordered.run.configuration.bindings[0].combinations.reverse()
  reordered.run.finalConfiguration = structuredClone(reordered.run.configuration)
  reordered.reloaded = structuredClone(reordered.run.configuration)
  const before = structuredClone(artifacts)
  assert.equal(validateProductionPhase(preflight, artifacts).records.length, 80)
  assert.deepEqual(artifacts, before)
  for (const change of [
    reports => { reports[0].contextHash = 'changed-context' },
    reports => { reports.push(structuredClone(reports[0])) },
    reports => { reports[0].checks.reverse() },
  ]) {
    const changed = structuredClone(artifacts)
    const artifact = changed.find(candidate => candidate.name === reordered.name)
    change(artifact.run.configuration.bindings[0].combinations)
    artifact.run.finalConfiguration = structuredClone(artifact.run.configuration)
    artifact.reloaded = structuredClone(artifact.run.configuration)
    assert.throws(() => validateProductionPhase(preflight, changed), /Identity drift between rounds/)
  }
})

test('matching profile order including wildcard transport remains significant', () => {
  const { preflight, artifacts } = fixture()
  const candidates = artifacts.filter(artifact => artifact.run.version === 'C-production' && artifact.run.scenario === 'short')
  for (const artifact of candidates) {
    const reports = artifact.run.configuration.bindings[0].combinations
    reports.push({ ...structuredClone(reports[0]), transport: 'http_json', profile: 'restricted' })
    artifact.run.finalConfiguration = structuredClone(artifact.run.configuration)
    artifact.reloaded = structuredClone(artifact.run.configuration)
  }
  assert.equal(validateProductionPhase(preflight, artifacts).records.length, 80)
  const reordered = candidates[1]
  reordered.run.configuration.bindings[0].combinations.reverse()
  reordered.run.finalConfiguration = structuredClone(reordered.run.configuration)
  reordered.reloaded = structuredClone(reordered.run.configuration)
  assert.throws(() => validateProductionPhase(preflight, artifacts), /Identity drift between rounds/)
})

test('distinct window names cannot reuse one measured observation', () => {
  for (const phase of ['standalone', 'counters']) {
    const { preflight, artifacts } = fixture(phase)
    const first = artifacts[0], next = artifacts[1]
    next.rows = structuredClone(first.rows)
    next.measurementSHA256 = createHash('sha256').update(JSON.stringify(next.rows)).digest('hex')
    assert.notEqual(first.name, next.name)
    assert.throws(() => validateProductionPhase(preflight, artifacts), /Reused measurement/)
  }
})

test('retains the existing estimator, reverses throughput, and distinguishes unavailable metrics', () => {
  const { preflight, artifacts } = fixture()
  const validated = validateProductionPhase(preflight, artifacts)
  const report = compareProductionPhases(validated)
  assert.deepEqual(report.comparisons.short.cpuMS, compareSamples(Array(5).fill(100 / 4096 / 1e6), Array(5).fill(110 / 4096 / 1e6)))
  assert.equal(report.comparisons.short.cpuMS.assessment, 'regression')
  assert.equal(report.comparisons.short.throughput.assessment, 'improvement')
  assert.equal(report.comparisons.short.gcCycles.assessment, 'below-measurement-resolution')
  assert.equal(report.comparisons.short.bytes.assessment, 'not-collected')
  assert.equal(report.comparisons.short.firstFrameMS.assessment, 'not-applicable')
  assert.equal(report.comparisons.stream.firstFrameMS.assessment, 'inconclusive')
})

test('counter phases and one equal-size confirmation retain all original observations', () => {
  const { preflight, artifacts } = fixture('counters')
  const validated = validateProductionPhase(preflight, artifacts)
  const confirmationArtifacts = artifacts.map(artifact => {
    const rows = structuredClone(artifact.rows)
    rows[0].StartedUnixNS += 1000000
    rows[0].FinishedUnixNS += 1000000
    return { ...artifact, rows, measurementSHA256: createHash('sha256').update(JSON.stringify(rows)).digest('hex') }
  })
  const confirmation = validateProductionPhase(preflight, confirmationArtifacts)
  const report = compareProductionPhases(validated, confirmation)
  assert.equal(report.records.length, 160)
  assert.equal(report.confirmationCombined, true)
  assert.deepEqual(report.comparisons.short.bytes.samples, [10, 10])
  assert.equal(report.comparisons.short.gcPauseNS.assessment, 'below-measurement-resolution')
  assert.throws(() => compareProductionPhases(validated, validated), /Confirmation reused/)
  const changed = { ...validated, preflight: { ...preflight, processRunnerSHA256: hash('8') } }
  assert.throws(() => compareProductionPhases(validated, changed), /Confirmation changed/)
})

test('CLI reads complete evidence and rejects reuse of the initial batch as confirmation', async () => {
  const directory = await mkdtemp(join(tmpdir(), 'elysia-production-comparison-'))
  try {
    const input = join(directory, 'standalone')
    await mkdir(input)
    const { preflight, artifacts } = fixture()
    await writeFile(join(input, 'cutover-preflight.json'), JSON.stringify(preflight))
    for (const artifact of artifacts) {
      const folder = join(input, artifact.name)
      await mkdir(folder)
      for (const [name, content] of Object.entries({ 'run.json': artifact.run, 'measurements.json': artifact.rows,
        'reloaded-identity.json': artifact.reloaded, 'execution.json': artifact.execution })) {
        await writeFile(join(folder, name), JSON.stringify(content))
      }
    }
    const command = resolve('scripts/performance-production-compare.mjs')
    const output = join(directory, 'comparison.json')
    await promisify(execFile)(process.execPath, [command, '--standalone=' + input, '--output=' + output], { windowsHide: true })
    const report = JSON.parse(await readFile(output, 'utf8'))
    assert.equal(report.phases.standalone.records.length, 80)
    assert.equal(report.phases.standalone.comparisons.short.cpuMS.assessment, 'regression')
    await assert.rejects(promisify(execFile)(process.execPath, [command, '--standalone=' + input,
      '--standalone-confirmation=' + input, '--output=' + join(directory, 'duplicate.json')], { windowsHide: true }),
    /Confirmation requires a distinct initial directory/)
  } finally {
    await rm(directory, { recursive: true, force: true })
  }
})
