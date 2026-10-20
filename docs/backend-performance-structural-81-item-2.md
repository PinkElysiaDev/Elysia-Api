# Structural performance follow-up — item 2: immutable request reuse

Tracking [#81](https://github.com/PinkElysiaDev/Elysia-Api/issues/81). Depends on [PR #82](https://github.com/PinkElysiaDev/Elysia-Api/pull/82), accepted implementation `a433eb8`.

## Implementation

- Add immutable `DecodeRequestValue`, `EncodeRequestValue` and `CheckOperationValueInput` boundaries. Existing public byte APIs still copy/validate as before; operation-schema absence and unknown-operation behavior remain unchanged.
- Parse ingress once, reuse that Value for session metadata, continuation-carrier extraction, request decode and operation checks. Changed carrier documents own their new output; decoded token strings and long-lived resources remain detached.
- Keep the first eligible candidate's preflight result immutable. Forwarding rechecks request/scope/revision/context/budget equality, applies authored wire rules to the actual Value, repeats route/body/final-wire/operation checks, then creates mutable transport bytes once.
- Native request replay consumes its already validated original Value rather than copying it back to bytes and parsing again. Media encoding uses invocation-local immutable read views; maps are independent and small fields detach.

The reused scan result is JSON syntax validity carried by immutable Value, not a cached authorization/schema/semantic verdict. Resource/schema checks still execute at every required boundary, including repeated occurrences and tighter target/request limits. No new cross-request cache, public mutable alias or diagnostic suppression is introduced.

## Targeted validation

Passed; no full suite or benchmark:

```sh
GOMAXPROCS=4 go test -C backend -tags=sonic -pgo=off -race -p=1 -count=1 -timeout=6m \
  ./protocol ./protocol/builtin ./server \
  -run '^Test(RequestValueBoundaries|OperationInputRequestBudget|OperationSchemaIsVerified|RuntimeScopeResolver|RequestViews|RequestResources|ContinuationValue|ContinuationExtraction|RequestWireSchemaTypedParity|DeferredRequestOutput|NativeRequestCanShrink|GatewayPreparedEncoding|GatewayChatWireContractFailure|GatewayContinuationPersistenceAndIsolation|GatewayLateOutputLimitNeverCallsUpstream|GatewayCanceledRequestDoesNotReachUpstream)'

node scripts/benchmark-performance.mjs --mode=measure \
  --cases=chat-short,candidates4,history256k,burst4,stream256-cross,image2,image2-media \
  --requests=16 --repeats=3 --idle-ms=1000 --output=NEW_EMPTY_DIRECTORY
```

Tests explicitly compare byte/Value schema and budget errors at exact/over-limit boundaries, cancellation, mutable candidate separation, public byte ownership, scope-resolver rejection, carrier parity and native target shrink. Authored-wire rejection, continuation and private preflight invalidation remain covered. [Race log](artifacts/performance-structural-81/validation/item-2-tests.log).

## Outcome and limits

image2 B/request **-43.6%** on top of item 1; media logging **-26.1%**; 256 KiB history **-7.5%**. These are cumulative-allocation improvements, not a guarantee of lower peak memory or latency: image2 P50 increased 82.04 → 97.04 ms and peak RSS 88.85 → 91.64 MiB in this small sequential run; history P50 also increased. All results remain below. Broader resource-metric sharing/generated walkers remain outside this item; no required budget scan was removed.

## Incremental HTTP results: item-1-final → item-2

| Case | KiB/request (change) | allocations/request | CPU ms/request | response P50 ms | sampled peak heap MiB | sampled peak RSS MiB |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| chat-short | 146.0 → 144.5 (-1.0%) | 2853 → 2851 | 4.55 → 4.57 | 3.37 → 3.15 | 15.37 → 16.16 | 66.31 → 67.59 |
| candidates4 | 267.5 → 255.0 (-4.7%) | 5677 → 5673 | 7.93 → 6.85 | 5.92 → 5.51 | 15.35 → 15.32 | 68.48 → 68.37 |
| history256k | 11268.4 → 10419.8 (-7.5%) | 34420 → 34409 | 52.11 → 45.45 | 28.84 → 33.11 | 14.06 → 12.71 | 68.12 → 67.12 |
| burst4 | 143.1 → 143.1 (-0.0%) | 2853 → 2849 | 5.49 → 3.61 | 5.59 → 3.44 | 15.48 → 15.94 | 67.46 → 67.54 |
| stream256-cross | 20966.7 → 20940.2 (-0.1%) | 137491 → 137465 | 167.60 → 100.70 | 128.27 → 74.26 | 10.83 → 10.31 | 66.53 → 66.13 |
| image2 | 38731.0 → 21838.6 (-43.6%) | 5119 → 5074 | 130.70 → 107.21 | 82.04 → 97.04 | 35.44 → 35.48 | 88.85 → 91.64 |
| image2-media | 64473.0 → 47661.1 (-26.1%) | 5699 → 5630 | 329.59 → 252.52 | 262.01 → 202.42 | 39.56 → 38.21 | 98.60 → 97.11 |

Seven selected cases, 16 requests × three repeats (images fixed at four), four warm-ups and one-second natural idle observation: 264 formally counted requests/version/run. Complete response, usage, preserved request content and upstream call counts are validated by the isolated three-process harness. Allocation/CPU accounting includes usage settlement, HTTP latency does not. No profiler or forced GC runs during formal HTTP timing. Medians are per-repeat; sampled peaks are maxima and can miss brief spikes.

Go 1.27.2, Linux/amd64 Ryzen 7 8845H, Sonic, PGO off, GOMAXPROCS=4, GOGC=100, GOMEMLIMIT=2GiB. Sequential runs are not significance-tested; timings fluctuate and no universal latency/RSS improvement or production P99 is claimed. All regressions remain in the table.

Evidence: [before](artifacts/performance-structural-81/item-1-final/report.json), [after](artifacts/performance-structural-81/item-2/report.json). Reports record parent commit + source patch for pre-commit measurements and binary SHA256. Matching implementation is identified by the subsequent signed commit. New regression test sources are in that commit. Original measurement JSON is byte-preserving gzip-compressed; `compressed-measurements.json` records its uncompressed hashes. Binaries remain local.
