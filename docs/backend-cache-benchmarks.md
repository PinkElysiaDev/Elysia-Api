# Otter cache migration: focused benchmarks

## Scope and setup

- Baseline: `60e8bb3` (the singleflight migration in PR #51).
- Replacement: `github.com/maypok86/otter/v2 v2.3.0` for affinity and usage response caches.
- Environment: Linux/amd64, Go 1.27.2, AMD Ryzen 7 8845H.
- Parameters: `-benchtime=300ms -count=6 -cpu=1,8 -benchmem`.
- Each implementation ran separately; benchmarks were not run concurrently with tests or builds.
- Results below are medians reported by `benchstat`. Parallel `ns/op` measures aggregate throughput, not individual request latency.

The benchmarks are in `backend/server/cache_bench_test.go`:

- **Affinity:** 64 or 4,096 existing keys; either all reads or 90% reads / 10% updates. Both implementations refresh expiry on writes only. The old implementation receives `time.Now()` at its call boundary; Otter reads the clock internally.
- **Usage hit:** 128 prewarmed query keys, below the 256-entry capacity target.
- **Usage churn:** uniform random requests over 1,024 query keys, above the capacity target. Each worker uses a deterministic random seed.
- Usage workloads include Gin routing, query normalization, response capture/replay, and a fixed 1 KiB response. They exclude SQLite and network latency.
- Usage benchmarks also record handler executions (`loads/op`), entries at the end of the timed workload, and retained entries after maintenance. These diagnostics were added to both implementations after the initial comparison exposed different eviction behavior; usage results below use that instrumented comparison.

The comparison measures the complete migration, including removal of the affinity cache's global mutex and use of composite keys. It is not a comparison of eviction algorithms in isolation.

## Results

### Affinity cache

| Workload | CPUs | Before, ns/op | After, ns/op | Change |
| --- | ---: | ---: | ---: | ---: |
| Read hit, 64 keys | 1 | 69.70 | 68.94 | -1.09% |
| Read hit, 64 keys | 8 | 618.40 | 9.897 | -98.40% |
| 90% read / 10% write, 64 keys | 1 | 115.10 | 80.86 | -29.75% |
| 90% read / 10% write, 64 keys | 8 | 1,118.50 | 22.20 | -98.02% |
| Read hit, 4,096 keys | 1 | 77.33 | 74.75 | -3.34% |
| Read hit, 4,096 keys | 8 | 587.80 | 10.28 | -98.25% |
| 90% read / 10% write, 4,096 keys | 1 | 2,531.00 | 93.48 | -96.31% |
| 90% read / 10% write, 4,096 keys | 8 | 18,140.50 | 18.49 | -99.90% |

All affinity differences had `p=0.002`, with six samples. The old contended implementation had substantial dispersion (up to ±59%), so the large parallel ratios should not be treated as general gateway speedups. The robust finding is removal of serialized reads and the O(n) expiry scan on every write.

Read-only workloads remained at **0 B/op**. Mixed workloads increased from approximately **1 B/op** to **8–9 B/op**, due to Otter's update bookkeeping. Integer `allocs/op` output rounds these fractional per-operation allocations down to zero; it does not mean updates allocate nothing.

### Usage response cache

| Workload | CPUs | Before, ns/op | After, ns/op | Change |
| --- | ---: | ---: | ---: | ---: |
| Hit | 1 | 1,566 | 1,663 | **+6.20%** |
| Hit | 8 | 646.2 | 640.2 | No statistically significant difference |
| Churn | 1 | 2,247 | 1,802 | -19.83%, affected by temporary oversizing |
| Churn | 8 | 1,608 | 1,048 | -34.83% |

The single-thread hit regression had `p=0.004`; the parallel hit comparison had `p=0.394`. Both churn comparisons had `p=0.002` (six samples).

| Churn diagnostic | Before, 1 CPU | After, 1 CPU | Before, 8 CPUs | After, 8 CPUs |
| --- | ---: | ---: | ---: | ---: |
| Handler executions / operation | 0.7512 | 0.07055 | 0.7476 | 0.7165 |
| Entries at workload end | 256 | 918.5 | 256 | 259 |
| Entries after maintenance | 256 | 256 | 256 | 256 |

Entry counts are medians of end-of-workload samples, not measured peak memory. Otter's asynchronous maintenance can temporarily exceed `MaximumSize`, especially when one CPU is saturated. The one-CPU churn improvement therefore **is not an equal-capacity performance gain**. It includes fewer handler executions because more responses remain temporarily available. The 8-CPU result also includes eviction-policy and hit-rate effects.

Hit responses remained at 21 allocations/op and approximately 2,576 B/op. Parallel churn increased from 25 to 26 allocations/op, with total bytes approximately unchanged. The reduced allocation volume for single-thread churn is also affected by oversizing and should not be generalized.

## Implementation choices

- Affinity retains its existing TTL-only policy: five minutes after the latest successful write, without a new capacity eviction policy. Reads do not extend the lifetime.
- Usage responses retain a 15-second write-based TTL and use a **256-entry target**, replacing insertion-order eviction with Otter's adaptive W-TinyLFU policy.
- Otter's default asynchronous executor is retained. An inline-maintenance experiment kept churn at 256 entries but increased median cost to approximately 2,850 ns/op on one CPU and 14,255 ns/op on eight CPUs. It was not adopted.
- Explicit `singleflight.Group.Do` and generation validation remain responsible for request coalescing and invalidation. Cache hits no longer acquire the publication mutex; cache writes and invalidation still serialize.
- Old entries are invalidated before the new generation is published, preventing a new-generation lookup from observing an old cached response during a flush.
- Server shutdown and shared test fixtures stop Otter's maintenance goroutines explicitly. Usage caching stays lazily initialized, including after shutdown of an unused cache.

## Reproduction

From `backend/`, on each implementation:

```sh
go test ./server -run '^$' \
  -bench '^Benchmark(AffinityCache|UsageResponseCache)$' \
  -benchmem -benchtime=300ms -count=6 -cpu=1,8 > cache-results.txt

benchstat before.txt after.txt
```

The baseline predates `cache_bench_test.go`: copy the benchmark file to a detached baseline checkout, remove cache-close calls, restore the old affinity methods' `time.Now()` argument, and collect usage entry counts under the old cache mutex instead of through Otter. Keep workload sizes, random seeds, load counters, response bodies, and measurement parameters identical. Baseline executables and raw measurements from this run are retained under `/tmp/opencode/elysia-*` in the development environment.

Focused validation (no full backend suite):

```sh
go test -race ./server \
  -run 'Test(Affinity|ApplyAffinity|UsageCache|CacheShutdown|RouteCache|AdminUsage|ResetUsage|UsageSeq|DoShutdown|Shutdown|RequestedShutdown|DeleteGroupCascade|GatewayWebSocketIndependentProtocolsAndUsage)' \
  -count=1 -timeout=3m
```

This passed in 30.264 seconds. Coverage includes TTL boundaries, write-only renewal, group removal, cache generations, panic recovery, uncacheable responses, token revocation, WebSocket affinity, and shutdown.
