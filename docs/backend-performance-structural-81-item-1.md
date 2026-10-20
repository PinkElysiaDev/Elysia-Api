# Structural performance follow-up — item 1: single-output nested encoding

Tracking [#81](https://github.com/PinkElysiaDev/Elysia-Api/issues/81), following merged #78–#80. Baseline: clean upstream `1ddf7af`.

## Implementation and coverage

`protocol.JSONOutput` snapshots closed object/array inputs, shares validated immutable leaves, normalizes leaf spelling once, computes exact output length and writes one owned final string. It contains no placeholder Values, mutable public nodes or global payload cache. Native reconciliation constructs deferred nested output for documents at least 64 KiB, retaining the established small-document encoder below that threshold. Equality, exact-number comparison, array identities, context cancellation and final resource checks still execute.

Initial request-adapter rollout targets large Anthropic user/assistant history without message-level overlays. Text, image/document sources and nested tool-result containers are deferred; complex block features use the existing renderer, and message-level extensions or other codecs select the old path before execution. This is a general construction engine with an explicitly limited adapter rollout, not a claim that every codec/metadata combination now uses single-output encoding.

## Targeted validation

Passed; no full suite or full benchmark run:

```sh
GOMAXPROCS=4 go test -C backend -tags=sonic -pgo=off -race -p=1 -count=1 -timeout=6m \
  ./protocol ./protocol/builtin ./server \
  -run '^Test(JSONOutput|DeferredRequestOutput|NativePreservation|NativeIdentity|MutationTransaction|RuntimeNative|NativeArray|NativeRequestCanShrink|LargeRequestSnapshotBudget|RequestWireSchemaTypedParity|BuiltinNativeRequestAndResponsePreservation|GatewayMediaToolResultCandidateIsolation|GatewayStructuralLimitsReject|GatewayConfiguredRequestLimit|GatewayChatWireContractFailure|GatewayPreparedEncodingBoundaries)'

GOMAXPROCS=4 go test -C backend -tags=sonic -pgo=off ./protocol -run '^$' \
  -fuzz '^FuzzJSONOutputParity$' -fuzztime=10s -parallel=2

GOMAXPROCS=4 GOGC=100 GOMEMLIMIT=2GiB go test -C backend -tags=sonic -pgo=off \
  ./protocol -run '^$' -bench '^BenchmarkNestedJSONOutput$' \
  -benchmem -benchtime=150ms -count=3 -timeout=3m
```

The first race command validated the initial deferred implementation. After retaining the old small-document fast path, protocol/builtin race tests were rerun with JSONOutput, deferred nested native/request parity, exact numeric/native identity, resource shrink/limits and large snapshot filters; the final seven-case HTTP acceptance below then passed. The fuzz run completed 87,725 executions with no mismatch. Logs are in [validation](artifacts/performance-structural-81/validation).

## Focused nested-container benchmark

Both algorithms run in the same binary and include construction. Three 150 ms repeats; figures are medians.

| Payload / nested object-array pairs | Legacy B/op | Deferred B/op | Allocation-volume change | Legacy/deferred allocs/op |
| --- | ---: | ---: | ---: | ---: |
| 64 KiB / 2 | 295666 | 75312 | -74.5% | 12 / 12 |
| 64 KiB / 8 | 1182661 | 76608 | -93.5% | 48 / 42 |
| 2 MiB / 2 | 8422138 | 2106934 | -75.0% | 12 / 12 |
| 2 MiB / 8 | 33688530 | 2108225 | -93.7% | 48 / 42 |

This is the closed-container construction boundary, not a complete gateway request. All helper raw logs are retained; timing varied substantially.

## Timing and regression investigation

The primary matrix had large timing fluctuations on controls, with nearly unchanged allocation counts. ABBA confirmation on the accepted binaries used 64 requests × three repeats × two processes/version/case for burst4 and cross-stream; media stayed four requests/repeat:

| Case | CPU ms/request before → after | P50 ms before → after | B/request change |
| --- | ---: | ---: | ---: |
| burst4 | 2.17 → 3.33 | 2.39 → 3.55 | -2.4% |
| stream256-cross | 126.52 → 122.51 | 92.30 → 88.08 | +0.4% |
| image2-media | 328.01 → 281.31 | 258.69 → 209.16 | -31.1% |

The large cross-stream slowdown did not reproduce in ABBA. Burst timing remains worse in this confirmation; it is not hidden or claimed as a speedup. The work is accepted for the substantial large-payload allocation reduction, not a universal throughput/latency improvement. No statistical significance or causal attribution to a single helper is claimed. Both initial/prototype and final confirmation summaries are retained.

## Incremental HTTP results: baseline → item-1-final

| Case | KiB/request (change) | allocations/request | CPU ms/request | response P50 ms | sampled peak heap MiB | sampled peak RSS MiB |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| chat-short | 149.9 → 146.0 (-2.6%) | 2854 → 2853 | 7.96 → 4.55 | 5.54 → 3.37 | 15.80 → 15.37 | 67.71 → 66.31 |
| candidates4 | 258.4 → 267.5 (+3.5%) | 5690 → 5677 | 13.64 → 7.93 | 10.15 → 5.92 | 14.72 → 15.35 | 67.92 → 68.48 |
| history256k | 11197.9 → 11268.4 (+0.6%) | 34444 → 34420 | 91.67 → 52.11 | 68.83 → 28.84 | 14.28 → 14.06 | 68.74 → 68.12 |
| burst4 | 152.3 → 143.1 (-6.0%) | 2863 → 2853 | 1.55 → 5.49 | 1.56 → 5.59 | 16.53 → 15.48 | 68.58 → 67.46 |
| stream256-cross | 20891.9 → 20966.7 (+0.4%) | 137444 → 137491 | 64.53 → 167.60 | 45.28 → 128.27 | 10.66 → 10.83 | 65.89 → 66.53 |
| image2 | 67840.5 → 38731.0 (-42.9%) | 5217 → 5119 | 303.92 → 130.70 | 227.47 → 82.04 | 33.46 → 35.44 | 93.38 → 88.85 |
| image2-media | 93684.5 → 64473.0 (-31.2%) | 5727 → 5699 | 336.50 → 329.59 | 227.88 → 262.01 | 39.21 → 39.56 | 98.75 → 98.60 |

Seven selected cases, 16 requests × three repeats (images fixed at four), four warm-ups and one-second natural idle observation: 264 formally counted requests/version/run. Complete response, usage, preserved request content and upstream call counts are validated by the isolated three-process harness. Allocation/CPU accounting includes usage settlement, HTTP latency does not. No profiler or forced GC runs during formal HTTP timing. Medians are per-repeat; sampled peaks are maxima and can miss brief spikes.

Go 1.27.2, Linux/amd64 Ryzen 7 8845H, Sonic, PGO off, GOMAXPROCS=4, GOGC=100, GOMEMLIMIT=2GiB. Sequential runs are not significance-tested; timings fluctuate and no universal latency/RSS improvement or production P99 is claimed. All regressions remain in the table.

Evidence: [before](artifacts/performance-structural-81/baseline/report.json), [after](artifacts/performance-structural-81/item-1-final/report.json). Reports record parent commit + source patch for pre-commit measurements and binary SHA256. Matching implementation is identified by the subsequent signed commit. New regression test sources are in that commit. Original measurement JSON is byte-preserving gzip-compressed; `compressed-measurements.json` records its uncompressed hashes. Binaries remain local.
