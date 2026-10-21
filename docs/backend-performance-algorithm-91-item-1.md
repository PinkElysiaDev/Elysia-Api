# Algorithm follow-up #91 — item 1: linear stream accumulation

Baseline `bdb02c6`. Text collection keeps an append-only builder and materializes JSON only at `CompletedNode`, terminal comparison, `Finish` or `Partial`. Snapshot-prefix checks, encoded spelling, resource accounting and isolated published partial results remain intact. Complete-message validation uses maintained open-child counts instead of scanning all historical items. Continuation ordinals advance an immutable prefix frontier; earlier open messages still prevent unstable ordinals from being published.

For fixed-size deltas and a bounded number of observations, repeated-prefix encoding changes from Θ(F²) bytes to Θ(F). Full snapshots requested after every delta still have their unavoidable output cost. Bookkeeping changes from worst-case quadratic historical scans to linear total prefix advancement plus constant-time parent completion checks. This migration does not change the separate logprob accumulation algorithm or public snapshot validation.

## Size-scaling evidence

Same fixture runs against baseline via Go overlay and the new source. Three 200 ms repeats, GOMAXPROCS=4; timing fluctuates substantially, so medians are observations, not significance claims. Full collection/replay and terminal validation are included, not just append cost.

| Frames × 256-byte delta | Time ms/op before → after | Allocated bytes/op before → after (median) |
| --- | ---: | ---: |
| 128 | 4.237 → 1.152 | 2,793,005 → 347,551 |
| 512 | 83.610 → 4.165 | 68,901,056 → 1,447,045 |
| 2048 | 755.920 → 21.093 | 1,217,722,144 → 6,703,156 |

At 2048 frames, cumulative allocation falls **99.45%**; this is not live heap. With 4× frames, new allocations grow about 4.2× / 4.6× instead of 24.7× / 17.7×. Boundary/encoder growth thresholds affect the exact ratios. A 2048-message completion/ordinal fixture improves median **306.97 → 55.08 ms/op**; allocations remain dominated by semantic event handling.

## Acceptance and reproduction

Targeted race: `GOMAXPROCS=4 go test -C backend -tags=sonic -pgo=off -race -p=1 ./protocol ./protocol/builtin ./server -run '^Test(Collector|ResponseCollector|EventReplay|MessageCollector|ContinuationOrdinal|BuiltinStreamMatrix|TypedStreamConversion|GatewayContinuation|GatewayResponsesEncryptedReasoningContinuation|GatewaySSEAndNDJSONBridge)' -count=1 -timeout=6m`.

Helpers: `go test -tags=sonic -pgo=off ./protocol -run '^$' -bench '^BenchmarkAlgorithm(Collector|MessageCompletion)$' -benchmem -benchtime=200ms -count=3`. Baseline overlay maps `backend/protocol/algorithm_stream_test.go` from this commit into a clean baseline worktree. The two new observation/mismatch regressions also pass on the baseline oracle.

HTTP cross-stream ABBA cumulative allocation **-48.1%**, CPU **-18.7%**, P50 **-7.9%**. Chat CPU **+1.9%**; the primary image CPU/heap regresses. No universal gain claimed. All five cases pass content, response, usage and provider-call checks. The HTTP stream fixture exercises collection; ordinary paths without collection cannot inherit its helper percentage. No full suites ran. [Raw evidence](artifacts/performance-algorithm-91/).

## Selected HTTP comparison: baseline → item-1

| Case | CPU ms/request | KiB/request | allocations/request | P50 ms | peak heap MiB | peak RSS MiB |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| chat-short | 7.22 → 8.02 | 139.1 → 140.7 | 2641 → 2650 | 5.73 → 5.91 | 15.99 → 16.02 | 68.01 → 67.48 |
| candidates4 | 13.28 → 7.52 | 246.7 → 251.3 | 5107 → 5111 | 9.71 → 4.59 | 14.21 → 14.51 | 68.07 → 67.77 |
| burst4 | 6.73 → 4.73 | 139.1 → 139.4 | 2640 → 2638 | 6.11 → 4.39 | 15.62 → 15.15 | 68.01 → 67.99 |
| stream256-cross | 186.58 → 133.90 | 20489.4 → 10634.4 | 133041 → 132208 | 132.27 → 107.52 | 10.54 → 10.19 | 67.27 → 64.79 |
| image2 | 145.42 → 176.51 | 21739.5 → 21832.5 | 4831 → 4841 | 134.98 → 143.93 | 35.36 → 37.35 | 94.18 → 92.64 |

32 requests × three repeats; images use four. Five cases, 396 counted requests/version plus warm-ups. Go 1.27.2/Sonic/PGO off, GOMAXPROCS=4, GOGC=100, GOMEMLIMIT=2GiB. Isolated client/gateway/provider; CPU/allocation include usage settlement, response latency does not. Medians across repeat measurements; peaks are sampled maxima. No profiler/forced GC in formal timing. No significance or project-wide speedup claim.

## Hash-verified ABBA

64 requests × three repeats × two processes/version (images four).

| Case | CPU ms/request | B/request | allocations/request | P50 ms |
| --- | ---: | ---: | ---: | ---: |
| chat-short | 6.183 → 6.300 | 144692 → 144875 | 2639 → 2643 | 5.543 → 5.343 |
| stream256-cross | 162.473 → 132.035 | 21038744 → 10917290 | 133028 → 132209 | 113.206 → 104.263 |
