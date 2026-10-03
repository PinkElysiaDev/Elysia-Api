import test from 'node:test'
import assert from 'node:assert/strict'
import { parseBenchmarks } from './performance.mjs'
import { compareSamples } from './compare.mjs'

test('benchmark evidence never invents missing samples', () => {
  const sample = parseBenchmarks('BenchmarkRequestWorkloads/same/short-12  8000  41000 ns/op  1.50 MB/s  20000 B/op  300 allocs/op\nPASS')
  assert.deepEqual(sample, [{ name: 'BenchmarkRequestWorkloads/same/short', values: [{ nanos: 41000, bytes: 20000, allocations: 300 }] }])
  assert.deepEqual(parseBenchmarks('FAIL'), [])
  assert.throws(() => compareSamples([], [1]), /missing/)
})

test('repeatable effects and uncertain small changes stay distinct', () => {
  assert.equal(compareSamples([100, 101, 102], [70, 71, 72]).assessment, 'improvement')
  assert.equal(compareSamples([100, 101, 102], [120, 121, 122]).assessment, 'regression')
  assert.equal(compareSamples([98, 100, 102], [100, 101, 102]).assessment, 'inconclusive')
})
