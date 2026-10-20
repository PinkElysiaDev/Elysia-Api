# Profiling-driven optimization — batch 3

Tracking: [#77](https://github.com/PinkElysiaDev/Elysia-Api/issues/77). Depends on [PR #79](https://github.com/PinkElysiaDev/Elysia-Api/pull/79), which depends on #78.

## Changes and ownership boundaries

- Event-batch fingerprints use one temporary codec byte encoding, rather than materializing an immutable Value, detaching oversized strings, then copying it back to bytes. Digests remain byte-for-byte equivalent. Cross-protocol output does not fingerprint a batch for native replay when replay is ineligible; ordinary externally mutable EncodeFrame calls still check changes every time.
- A segment-local semantic cache lazily allocates a two-entry workspace and spills wider segments to its existing bounded map. Metrics and normalization updates share entries, limits account for every occurrence, and item/byte budgets still bound the whole workspace. No cross-request pool or retained global payload cache is added.
- Each active text tracker owns one 256-byte workspace for hash writes and snapshot sums. Scratch plaintext is cleared after use. Established append/snapshot paths allocate nothing; tracker construction/hash initialization are outside that claim.

## Focused helper measurements

The same benchmark source ran on detached batch-2 commit `f603cf2` and this implementation; five repeats, 500 ms per helper, Sonic/PGO-off, GOMAXPROCS=4, GOGC=100, GOMEMLIMIT=2GiB. No full benchmark suite ran.

| Exact helper boundary | B/op before → after | allocs/op before → after | median ns/op before → after |
| --- | ---: | ---: | ---: |
| Normalize one 256-byte delta Event | 1072 → 528 (-50.7%) | 7 → 6 | 7841 → 5564 |
| Append 256 bytes to an initialized TextTracker | 256 → 0 | 1 → 0 | 1088 → 120.9 |

Raw logs: [before](artifacts/performance-targeted-77/batch-3/micro-before.log), [after](artifacts/performance-targeted-77/batch-3/micro-after.log). Timing varied substantially between repeats; byte/object counts were stable. These are helper measurements, not whole-request zero-allocation or production latency claims.

## Targeted validation

Passed:

```sh
GOMAXPROCS=4 GOGC=100 GOMEMLIMIT=2GiB go test -C backend -tags=sonic -pgo=off \
  ./protocol -run '^$' -bench '^BenchmarkProfilingWorkspace$' \
  -benchmem -benchtime=500ms -count=5 -timeout=3m

GOMAXPROCS=4 go test -C backend -tags=sonic -pgo=off -race -p=1 -count=1 -timeout=6m \
  ./protocol ./protocol/builtin ./server \
  -run '^Test(SemanticDigest|SemanticCache|TextTrackerWorkspace|TypedMeter|ResourceBudget|ConvertedFrame|CompoundFrameRoundTrip|NativeFrameCannot|SingleNativeEventEdit|StreamSnapshot|StreamText|LongReplay|TypedEventConversionBoundaries|FinalWireValidationTypedParity|TypedStreamConversionAndEncodingParity|BuiltinStreamingTextDoesNotAccumulate|BuiltinStreamExtensionsStayNative|ResponsesNativeReplayFailureSequence|GatewayStreamWorkspace|GatewaySSEAndNDJSON|GatewayBrokenStream|StreamCancellationUsage)'

node scripts/benchmark-performance.mjs --mode=measure \
  --cases=chat-short,responses-short,tools32,candidates4,history256k,burst4,stream256,stream256-cross,image2 \
  --requests=16 --repeats=3 --idle-ms=1000 --output=NEW_EMPTY_DIRECTORY
```

## Timing-regression confirmation

The primary matrix again contained large short-run timing changes on paths with essentially unchanged allocation counts. Before acceptance, chat-short/history256k/burst4 were repeated in ABBA order, 64 requests × five repeats per process, two processes per version/case (1,920 counted requests per version across these three cases).

| Confirmatory case | CPU ms/request before → after | response P50 ms before → after | allocation-volume change |
| --- | ---: | ---: | ---: |
| chat-short | 7.24 → 4.59 | 5.88 → 3.31 | +1.1% |
| history256k | 67.41 → 69.68 (+3.4%) | 56.96 → 56.91 | +0.04% |
| burst4 | 4.20 → 2.93 | 4.36 → 2.57 | +2.2% |

The initial history P50 increase did not reproduce in the larger paired run. A modest history CPU increase remains, and non-stream allocation improvements are not claimed for this batch. Full confirmation evidence: [summary](artifacts/performance-targeted-77/batch-3-confirmed/summary.json), `report.json.gz` and its runner alongside it.

## Explicit follow-ups, not completed optimizations

This batch implements single-buffer event-container fingerprinting, not a universal deferred/nested wire encoder. A generic single-output request encoder, generated semantic walkers, wider immutable scan reuse, global preparation-memory admission, plan-cache lock redesign, and alternative flush policies require separate evidence/design and remain deferred. The one-second recovery timelines do not establish natural reclamation or absence of leaks. Final profiling and a separate cancellation/recovery diagnosis will be run only after all three batch acceptances and signed implementation commits.

## Incremental results: batch-2 → batch-3

| Case | KiB/request | allocation change | allocs/request | CPU ms/request | response P50 ms | sampled peak heap MiB | sampled peak RSS MiB |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| chat-short | 147.1 → 143.3 | -2.6% | 2852 → 2852 | 4.28 → 6.74 | 2.30 → 4.36 | 15.95 → 16.24 | 67.72 → 67.12 |
| responses-short | 168.3 → 174.8 | +3.8% | 3265 → 3264 | 9.86 → 7.00 | 6.91 → 5.02 | 15.86 → 16.42 | 69.69 → 68.82 |
| tools32 | 616.0 → 610.7 | -0.9% | 10263 → 10260 | 28.04 → 26.40 | 25.06 → 24.12 | 16.92 → 15.88 | 68.04 → 67.16 |
| candidates4 | 256.5 → 256.3 | -0.1% | 5682 → 5682 | 12.71 → 9.55 | 9.94 → 9.51 | 15.20 → 14.49 | 68.16 → 68.17 |
| history256k | 11255.1 → 11218.6 | -0.3% | 34423 → 34431 | 53.62 → 68.09 | 19.84 → 54.15 | 13.69 → 13.86 | 67.62 → 67.66 |
| burst4 | 144.6 → 145.0 | +0.3% | 2855 → 2861 | 4.12 → 7.06 | 4.22 → 6.83 | 15.34 → 15.57 | 68.59 → 68.55 |
| stream256 | 8036.5 → 7128.9 | -11.3% | 100126 → 98053 | 106.89 → 102.73 | 80.94 → 83.67 | 9.18 → 9.09 | 62.59 → 61.92 |
| stream256-cross | 22342.2 → 20935.4 | -6.3% | 144028 → 137488 | 198.98 → 201.80 | 147.73 → 149.69 | 10.81 → 10.74 | 66.35 → 66.19 |
| image2 | 67968.6 → 67863.7 | -0.2% | 5204 → 5183 | 314.29 → 238.34 | 251.99 → 177.61 | 36.99 → 37.57 | 93.85 → 92.58 |

Medians of three repeats; sampled peaks are maxima across repeats. Sixteen requests/repeat, except image2 is fixed at four. Four warm-up requests. The HTTP harness validates complete responses, usage, preserved request content, and upstream call counts. Allocation/CPU accounting includes asynchronous usage settlement; HTTP latency does not. Formal measurements do not enable profiling or force GC.

Go 1.27.2, Linux/amd64, Sonic build, PGO off, GOMAXPROCS=4, GOGC=100, GOMEMLIMIT=2GiB. Runs are sequential, not randomized A/B; CPU/latency changes are observations, not significance-tested speedups. Small-sample tail percentiles are not production P99. Sampled heap includes garbage and RSS is not live heap. No blanket peak-memory improvement is claimed.

Evidence: [before](artifacts/performance-targeted-77/batch-2/report.json), [after](artifacts/performance-targeted-77/batch-3/report.json). Measurements record their parent commit and dirty source patch; the subsequent signed implementation commit identifies the reviewed source. New regression test source is in that commit. Binary SHA256, commands, runtime/environment, per-request measurements, timelines, and original harness manifests are retained; binaries remain local. `archive-manifest.json` covers the migrated files; original manifests retain historical paths and do not cover the additional targeted test log.
