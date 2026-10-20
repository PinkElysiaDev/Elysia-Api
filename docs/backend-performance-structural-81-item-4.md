# Structural performance follow-up — item 4: global preparation admission

Tracking [#81](https://github.com/PinkElysiaDev/Elysia-Api/issues/81). Depends on [PR #83](https://github.com/PinkElysiaDev/Elysia-Api/pull/83), accepted item-2 implementation `669ef17`.

## Policy and ownership

The HTTP inference ingress acquires an instance-owned FIFO reservation after authentication/protocol selection and before body reading. It covers candidate preparation, final validation and transport-body construction, releases before upstream I/O, and reacquires for retries. Errors release idempotently; queued cancellation/timeout/shutdown does not consume the body. Model-group and daily quotas remain unchanged.

Startup defaults: eight active preparations, 32 queued, max(64 MiB, configured maxBodyBytes) aggregate input-body reservations, five-second queue timeout. Explicit zero queued means fail-fast; explicit disabled policy is available for troubleshooting. Unknown length reserves the pinned body limit; known declarations are checked against actual bounded reads rather than trusted or grown while holding capacity. Overload is protocol-shaped 503/Retry-After, impossible reservations 413, cancelled clients retain 499 accounting. [Configuration and limitations](backend-preparation-admission.md).

This is not a strict heap/RSS or all-in-flight memory bound. Active slow-read policy remains with HTTP/connection timeouts. Agent/task-worker/WebSocket lanes are not claimed to be covered. The policy is frozen per server instance and requires restart; it is not a new hot-editable WebUI setting.

## Targeted validation and helper overhead

Passed; no full suite or benchmark:

```sh
GOMAXPROCS=4 go test -C backend -tags=sonic -pgo=off -race -p=1 -count=1 -timeout=6m \
  ./internal/admission ./config ./internal/architecture ./server \
  -run '^Test(Preparation|PackageBoundaries|GatewayConfiguredRequestLimit|GatewayStructuralLimitsRejectBeforeSending|GatewayPreparedEncodingBoundaries|GatewaySSEAndNDJSONBridge|GatewayCanceledRequest|ShutdownCancelsActiveHTTPHandlerBeforeClosingStore|RequestedShutdownBeforeListenClosesAllResources)'

GOMAXPROCS=4 go test -C backend -tags=sonic -pgo=off ./internal/admission -run '^$' \
  -bench '^BenchmarkPreparationAdmission$' -benchmem -benchtime=300ms -count=5
```

Coverage includes FIFO/concurrent and byte limits, queue-full, timeout, grant/cancel races, repeated release, closure, config Load/Reload/Update validation and persistence, untrusted/unknown body lengths, auth-before-read, queued HTTP cancellation/shutdown, and real HTTP release-before-provider-I/O. The initialized unqueued acquire/release helper measured **32 B/op, one allocation/op**, median **97.16 ns/op** across five repeats; it excludes HTTP/context/logging/initialization and is not a whole-request claim.

## Same-binary controlled memory attribution

Both experiments use the exact item-4 accepted-matrix binary, in off/bounded/bounded/off order. All responses, usage/content preservation and provider-call counts are checked; no profiling or forced GC is enabled. Each process has three measured repeats and four warm-ups, with one-second idle observation. Full logs/metrics and policy settings are archived. These policies are compared on the same source, not against an earlier encoding implementation.

| Experiment / policy | Requests per policy | Peak preparation / queue | Peak reserved body MiB | Sampled peak heap MiB | Sampled peak RSS MiB | CPU ms/request | response P50 ms |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 16 workers, off | 192 | 16 / 0 | 32.00 | 288.94 | 358.07 | 38.58 | 137.82 |
| 16 workers, default 8 slots | 192 | 8 / 8 | 16.00 | 287.08 | 364.27 | 36.32 | 129.66 |
| 8 workers, off | 96 | 8 / 0 | 16.00 | 150.73 | 217.68 | 43.28 | 71.86 |
| 8 workers, stricter 1 slot | 96 | 1 / 7 | 2.00 | 35.59 | 94.75 | 107.72 | 611.05 |

The default policy **enforced** its eight-slot/byte-reservation bounds, with no rejection/timeouts and all counters returned to zero, but did not meaningfully reduce sampled heap/RSS in this run (RSS slightly increased). The stricter one-slot policy reduced sampled peak heap **76.4%** and RSS **56.5%**, at the substantial cost of queue latency and observed CPU/request. This is an explicit memory/concurrency trade-off, not a universal speedup or a claim that default limits reduce RSS by those percentages. No sustained capacity or leak-freedom claim is made.

Evidence: [default experiment](artifacts/performance-structural-81/item-4-default-memory/summary.json), [one-slot experiment](artifacts/performance-structural-81/item-4-memory/summary.json). Reproduction with the matching local binary identified in the report:

```sh
node docs/artifacts/performance-structural-81/item-4-default-memory/runner.mjs \
  .tmp-dev/perf-structural/item-4/report.json NEW_EMPTY_DIRECTORY 8 16 32
node docs/artifacts/performance-structural-81/item-4-memory/runner.mjs \
  .tmp-dev/perf-structural/item-4/report.json NEW_EMPTY_DIRECTORY 1 8 16
```

## Normal-path incremental results

The original seven-case matrix was repeated unchanged with default admission. Allocation-volume overhead is mostly small (image2 +0.2%, Chat +0.4%, burst4 +1.4%); timings and sampled peaks remain mixed, including image2-media P50/RSS increases. New test-only admission metrics are included in collector overhead, so tiny differences are not presented as pure limiter costs. Full results follow.

## Incremental HTTP results: item-2 → item-4

| Case | KiB/request (change) | allocations/request | CPU ms/request | response P50 ms | sampled peak heap MiB | sampled peak RSS MiB |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| chat-short | 144.5 → 145.1 (+0.4%) | 2851 → 2862 | 4.57 → 4.47 | 3.15 → 3.30 | 16.16 → 15.02 | 67.59 → 67.30 |
| candidates4 | 255.0 → 258.4 (+1.3%) | 5673 → 5691 | 6.85 → 7.20 | 5.51 → 5.82 | 15.32 → 14.32 | 68.37 → 67.15 |
| history256k | 10419.8 → 10460.2 (+0.4%) | 34409 → 34406 | 45.45 → 43.21 | 33.11 → 33.77 | 12.71 → 13.45 | 67.12 → 67.26 |
| burst4 | 143.1 → 145.2 (+1.4%) | 2849 → 2860 | 3.61 → 2.88 | 3.44 → 2.68 | 15.94 → 15.40 | 67.54 → 69.50 |
| stream256-cross | 20940.2 → 20794.5 (-0.7%) | 137465 → 137454 | 100.70 → 116.98 | 74.26 → 80.68 | 10.31 → 10.47 | 66.13 → 65.82 |
| image2 | 21838.6 → 21876.0 (+0.2%) | 5074 → 5074 | 107.21 → 100.91 | 97.04 → 86.96 | 35.48 → 37.32 | 91.64 → 91.32 |
| image2-media | 47661.1 → 47716.0 (+0.1%) | 5630 → 5668 | 252.52 → 288.73 | 202.42 → 260.49 | 38.21 → 42.57 | 97.11 → 99.17 |

Seven selected cases, 16 requests × three repeats (images fixed at four), four warm-ups and one-second natural idle observation: 264 formally counted requests/version/run. Complete response, usage, preserved request content and upstream call counts are validated by the isolated three-process harness. Allocation/CPU accounting includes usage settlement, HTTP latency does not. No profiler or forced GC runs during formal HTTP timing. Medians are per-repeat; sampled peaks are maxima and can miss brief spikes.

Go 1.27.2, Linux/amd64 Ryzen 7 8845H, Sonic, PGO off, GOMAXPROCS=4, GOGC=100, GOMEMLIMIT=2GiB. Sequential runs are not significance-tested; timings fluctuate and no universal latency/RSS improvement or production P99 is claimed. All regressions remain in the table.

Evidence: [before](artifacts/performance-structural-81/item-2/report.json), [after](artifacts/performance-structural-81/item-4/report.json). Reports record parent commit + source patch for pre-commit measurements and binary SHA256. Matching implementation is identified by the subsequent signed commit. New regression test sources are in that commit. Original measurement JSON is byte-preserving gzip-compressed; `compressed-measurements.json` records its uncompressed hashes. Binaries remain local.
