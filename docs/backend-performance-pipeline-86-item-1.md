# Pipeline refactor #86 — item 1: bounded document metric ownership

## Changes and boundaries

Large (at least 64 KiB) request decode/encode calls own a private context workspace shared with nested native replay. It caches immutable-leaf byte/node/depth facts, bounded to 128 entries and 64 MiB retained input, never mutable containers or policy verdicts. Metrics are still applied for every occurrence at its current nesting depth and against each current budget. Smaller calls create no workspace. The owner ends with the outer call, is not attached to returned Requests, and uses a mutex for trusted modules forking work on the supplied context.

This first migration covers compatible runtime/typed-module boundaries, not all gateway stages or schema scans. Cache misses and errors retain the established exact error path; changed immutable leaves get distinct entries. There is no global payload cache. This intentionally bounded rollout avoids retaining large ingress documents through provider response lifetimes.

## Acceptance

Targeted race tests passed with `GOMAXPROCS=4 go test -C backend -tags=sonic -pgo=off -race -p=1 ./protocol ./protocol/builtin ./server -run '^Test(DocumentMetrics|RequestValueBoundaries|OperationInputRequestBudget|RequestWireSchemaTypedParity|NativeRequestCanShrink|LargeRequestSnapshotBudget|GatewayPreparedEncoding|GatewayChatWireContractFailure)' -count=1 -timeout=5m`.

`BenchmarkDocumentMetrics` compares four checks of a semantic request containing two references to the same 2 MiB leaf, including owner construction: median **1.487 ms → 0.173 ms (-88.3%)**, **0 → 272 B/op**, **0 → 3 allocs/op** (three 300 ms repeats). This is a repeated-check helper, not HTTP throughput. Tests exercise tighter budgets, repeated occurrences, mutations, bounded retention and concurrent context usage under race detection.

The five-case matrix and ABBA passed HTTP/content/usage acceptance. Timing controls remain noisy: Chat reverses direction across datasets; burst regresses in ABBA; cross-stream is essentially flat in ABBA. Image CPU improves in both but with very different magnitudes. Acceptance is for demonstrated repeated-scan elimination and preserved checks, not a stable project-wide CPU percentage. Sampled peaks and all regressions remain below. No full test/benchmark suite ran.

Evidence: [archive](artifacts/performance-pipeline-86/), including source patches, binary hashes, paired raw reports and validation logs. Selected HTTP invocation uses `--mode=measure --cases=chat-short,candidates4,burst4,stream256-cross,image2 --requests=32 --repeats=3 --idle-ms=1000`; paired runner uses `chat-short,burst4,stream256-cross,image2 64 3`.

## Incremental selected HTTP results: baseline → item-1

| Case | KiB/request | allocations/request | CPU ms/request | P50 ms | peak heap MiB | peak RSS MiB |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| chat-short | 143.6 → 142.6 | 2857 → 2851 | 4.38 → 8.17 | 2.63 → 6.14 | 15.45 → 15.92 | 67.27 → 68.20 |
| candidates4 | 254.9 → 261.2 | 5675 → 5693 | 12.50 → 12.76 | 10.01 → 9.99 | 15.23 → 14.79 | 69.16 → 68.05 |
| burst4 | 145.1 → 142.6 | 2861 → 2848 | 6.76 → 5.68 | 6.39 → 4.87 | 13.48 → 15.54 | 66.36 → 68.52 |
| stream256-cross | 20817.0 → 20823.5 | 137477 → 137473 | 161.69 → 182.22 | 98.42 → 137.11 | 10.64 → 10.83 | 67.09 → 67.04 |
| image2 | 21891.7 → 21835.3 | 5091 → 5092 | 156.14 → 150.66 | 131.33 → 130.59 | 33.92 → 31.31 | 88.65 → 89.73 |

32 requests × three repeats (image: four), four warm-ups, one-second idle: 396 counted requests/version. Separate client/gateway/provider processes; response/content/usage/call counts checked. Go 1.27.2, Sonic, PGO off, GOMAXPROCS=4, GOGC=100, GOMEMLIMIT=2GiB. CPU includes usage settlement; latency does not. Per-repeat medians and sampled peak maxima; no significance or universal latency/RSS claim. No forced GC or profiler in formal HTTP timing.

## Hash-verified ABBA confirmation

64 requests × three repeats × two processes/version (image stays four).

| Case | CPU ms/request | P50 ms | B/request change |
| --- | ---: | ---: | ---: |
| chat-short | 7.23 → 4.26 | 5.54 → 3.03 | +1.1% |
| burst4 | 3.05 → 4.45 | 3.14 → 4.69 | -1.5% |
| stream256-cross | 173.12 → 173.65 | 125.96 → 119.76 | -0.4% |
| image2 | 143.22 → 66.70 | 119.93 → 40.66 | -0.4% |
