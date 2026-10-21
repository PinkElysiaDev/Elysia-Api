# Pipeline refactor #86 — item 3: typed frame-batch handoff

Depends on #88 (`caa441e`). Built-in stream encoders now expose owned typed frame batches. Eligible schema-free batch mappings hand frames directly to transport processing instead of serializing a JSON array and reparsing it; stateful encoders execute once. Aggregate array byte/node/depth budgets, raw normalization and exact failure paths remain enforced. Public Value APIs, authored mappings/schema boundaries and per-event native replay retain their established execution. Same-frame root reads use bounded immutable readers. Plain short ASCII scalar construction now owns exactly its output instead of requesting a generic codec workspace; large/escaped/Unicode/invalid-byte text retains Sonic behavior.

This is a concrete typed handoff within the existing stream pipeline, not a claim that every codec rule or final validation is fused. Native lifecycle replay, actual final-wire validation, usage tails, cancellation, buffering, flush frequency and backpressure remain intact.

## Acceptance

- Targeted race: `GOMAXPROCS=4 go test -C backend -tags=sonic -pgo=off -race -p=1 ./protocol ./protocol/builtin ./server -run '^Test(FrameBatch|EventFrame|NativeFrame|TypedStreamConversionAndEncodingParity|BuiltinStreamMatrixPreservesTextAndUsageTails|BuiltinStreamExtensionsStayNative|ResponsesNativeReplayFailureSequence|ResponsesTurnStateCompleteStreams|WireStreamLifecycle|GatewaySSEAndNDJSONBridge|GatewayChatWireContractFailure|GatewayCanceledRequest)' -count=1 -timeout=6m`.
- The existing typed-vs-JSON-only codec matrix checks text/tool event sequences across codecs, errors, unchanged caller inputs and exact frame output. Added aggregate batch boundary checks retain nil/absent/escaping/precise-number behavior.
- Scalar differential fuzz against Sonic: **258,753 executions**, ten seconds/two workers.
- Scalar helper median **505.7 → 277.1 ns/op**, approximately **42 → 24 B/op**, **2 → 1 allocations/op**; three 300 ms repeats. Timings fluctuated; not a gateway result.
- Cross-stream ABBA allocations/request **137,256 → 133,036 (-3.1%)**, B/request **-1.4%**, CPU essentially flat **176.96 → 176.73 ms**. Chat CPU -5.2%; burst CPU **+18.2%**. Primary candidate CPU/P50 and some peaks regress. All datasets retained below; acceptance is lower transient materialization/allocations, not proven universal CPU improvement.

No full suite or full benchmark. [Evidence](artifacts/performance-pipeline-86/).

## Incremental selected HTTP results: item-2 → item-3

| Case | KiB/request | allocations/request | CPU ms/request | P50 ms | peak heap MiB | peak RSS MiB |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| chat-short | 140.2 → 141.5 | 2684 → 2639 | 6.71 → 5.18 | 5.70 → 2.42 | 15.74 → 15.78 | 66.65 → 67.49 |
| candidates4 | 249.1 → 255.7 | 5181 → 5105 | 9.04 → 10.95 | 8.35 → 8.86 | 15.43 → 14.64 | 67.65 → 68.57 |
| burst4 | 144.9 → 145.6 | 2689 → 2646 | 6.32 → 3.74 | 6.08 → 3.32 | 16.52 → 13.32 | 69.58 → 67.88 |
| stream256-cross | 20755.4 → 20417.4 | 137264 → 133037 | 208.13 → 176.04 | 153.74 → 119.48 | 10.61 → 10.59 | 67.23 → 66.86 |
| image2 | 21836.3 → 21677.3 | 4891 → 4825 | 135.66 → 102.82 | 100.77 → 83.82 | 37.74 → 35.24 | 96.80 → 92.85 |

32 requests × three repeats (image: four), four warm-ups, one-second idle: 396 counted requests/version. Separate client/gateway/provider processes; response/content/usage/call counts checked. Go 1.27.2, Sonic, PGO off, GOMAXPROCS=4, GOGC=100, GOMEMLIMIT=2GiB. CPU includes usage settlement; latency does not. Per-repeat medians and sampled peak maxima; no significance or universal latency/RSS claim. No forced GC or profiler in formal HTTP timing.

## Hash-verified ABBA confirmation

64 requests × three repeats × two processes/version (image stays four).

| Case | CPU ms/request | P50 ms | B/request change |
| --- | ---: | ---: | ---: |
| chat-short | 7.39 → 7.01 | 5.83 → 5.42 | -1.3% |
| burst4 | 4.03 → 4.76 | 4.21 → 4.81 | -1.9% |
| stream256-cross | 176.96 → 176.73 | 134.49 → 132.04 | -1.4% |
