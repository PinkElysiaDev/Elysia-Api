import test from 'node:test'
import assert from 'node:assert/strict'
import { parseBenchmarks } from './performance.mjs'
import { compareSamples, compareLoadSamples } from './compare.mjs'

test('benchmark evidence never invents missing samples', () => {
  const sample = parseBenchmarks('BenchmarkRequestWorkloads/same/short-12  8000  41000 ns/op  1.50 MB/s  20000 B/op  300 allocs/op\nPASS')
  assert.deepEqual(sample, [{ name: 'BenchmarkRequestWorkloads/same/short', values: [{ nanos: 41000, bytes: 20000, allocations: 300 }] }])
  assert.deepEqual(parseBenchmarks('FAIL'), [])
  assert.throws(() => compareSamples([], [1]), /missing/)
})

test('load comparisons invert throughput and reject request failures', () => {
  const measurement = scale => ({ path: 'p', workload: 'w', delayed: false, concurrency: 1, failures: 0, cpuPerRequestNS: 100, requestsPerSecond: scale * 100, allocatedBytesPerRequest: 100, allocationsPerRequest: 10, gcPauseNS: 0, latencyP50P95P99NS: [100, 200, 300], firstFrameP50P95P99NS: [10, 20, 30] })
  const first = Array.from({ length: 3 }, () => measurement(1))
  const second = Array.from({ length: 3 }, () => measurement(2))
  const comparison = compareLoadSamples(first, second)[0]
  assert.equal(comparison.metrics.requestsPerSecond.assessment, 'improvement')
  assert.equal(comparison.metrics.firstFrameP95.afterMedian, 20)
  assert.equal(comparison.metrics.gcPauseNS.assessment, 'below-measurement-resolution')
  second[0].failures = 1
  assert.equal(compareLoadSamples(first, second)[0].assessment, 'failed-requests')
})

test('repeatable effects and uncertain small changes stay distinct', () => {
  assert.equal(compareSamples([100, 101, 102], [70, 71, 72]).assessment, 'improvement')
  assert.equal(compareSamples([100, 101, 102], [120, 121, 122]).assessment, 'regression')
  assert.equal(compareSamples([98, 100, 102], [100, 101, 102]).assessment, 'inconclusive')
})
