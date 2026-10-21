# Pipeline ownership and typed traversal: final report (#86)

## Outcome

All four implementation items were delivered in order with targeted acceptance, English linked PRs and GPG-signed commits. **The series improves specific CPU helpers, large-payload CPU, allocation counts and held-stream retention; it does not establish project-wide CPU or peak-memory improvement.** Final Chat/cross-stream CPU regressions and large-image peak-memory regressions remain visible below. Per-item acceptance means the scoped behavior and evidence passed review, not that every performance dimension improved.

| Item | PR / signed implementation | Implemented boundary |
| --- | --- | --- |
| 1: document metrics | [#87](https://github.com/PinkElysiaDev/Elysia-Api/pull/87), `647ab51` | Bounded call-local immutable-leaf facts across large request decode/encode and nested native replay |
| 2: typed traversal | [#88](https://github.com/PinkElysiaDev/Elysia-Api/pull/88), `caa441e` | Generated resource walkers for closed models; reflective error/unsupported oracle retained |
| 3: streaming handoff | [#89](https://github.com/PinkElysiaDev/Elysia-Api/pull/89), `917041d` | Owned typed frame batches, exact aggregate checks, frame-local reads and short scalar ownership |
| 4: lifetime handoff | [#90](https://github.com/PinkElysiaDev/Elysia-Api/pull/90), `89673ff` | Retry preparation ownership → small delivery plan; outbound-model-only provenance snapshot |

Fresh baseline is merged `deploy` at `8099841` (already includes #82–#84). Integrated source is `89673ffb684324f543229513b83de4bebff5ae20`. This report is not compared with v1.6.2 or the original #81 pre-optimization source. A clean signed-source rebuild matched the measured/profiled final binary SHA256 **`4b85f2640209e17e190dc06c27c8b705be9ea9a42e97200cb8957a0c11311374`**. All four implementation signatures were verified locally before push and by GitHub, using `TR0MX <ytang7543@gmail.com>`, fingerprint `F8C0FF15D6227656C0748FD47A8E1E265270109C`.

## Method and acceptance

- Go 1.27.2, Linux/amd64 Ryzen 7 8845H, Sonic, PGO off, GOMAXPROCS=4, GOGC=100, GOMEMLIMIT=2GiB. Builds/tests/measurements run serially.
- Explicit affected-package race filters; independent JSON-token/generated/reflective checks; 36,262 typed-meter and 258,753 scalar fuzz executions. No full test or full benchmark suite.
- Each adjacent primary HTTP comparison has five selected cases, 396 counted requests/version plus warm-ups. Final ABBA uses hash-verified baseline/final binaries: 64 requests × three repeats × two processes/version/case, images fixed at four (1,560 total counted requests/version across five cases).
- Client, gateway and synthetic provider run in separate processes. Full response/content preservation, usage settlement and provider-call counts are checked. Timed runs do not enable profiling or force GC.
- CPU and allocation accounting include usage settlement, response latency does not. Tables use per-repeat medians and sampled peak maxima. No significance test, production traffic-weighted score or universal speedup is claimed.
- Final profiling was collected after all four implementation acceptances and signed pushes: separate CPU/allocation runs for Chat, cross-stream256, image2 and image31. **40 raw profiles: eight CPU and 32 heap/allocation**; all CPU profiles contain >10 accumulated sample seconds (13.82–17.16 s).
- Fresh image31 JSON/SSE uses 31 MiB input base64 text, single concurrency, one request × three repeats/transport and two warm-ups. It is a small-sample separate comparison, not a load-capacity claim.
- Forced-GC held-stream diagnosis is separate from normal HTTP timing and sampled peaks. Earlier failed regression output and all conflicting timing datasets remain archived.

Individual reports: [item 1](backend-performance-pipeline-86-item-1.md), [item 2](backend-performance-pipeline-86-item-2.md), [item 3](backend-performance-pipeline-86-item-3.md), [item 4](backend-performance-pipeline-86-item-4.md).

## Integrated ABBA: fresh baseline → all four items

| Case | CPU ms/request (change) | P50 ms | KiB/request (change) | allocations/request | peak heap MiB | peak RSS MiB |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| chat-short | 5.71 → 5.82 (+2.0%) | 4.15 → 4.06 | 145.4 → 139.3 (-4.2%) | 2849 → 2643 | 16.01 → 14.97 | 67.63 → 67.27 |
| candidates4 | 10.56 → 10.27 (-2.8%) | 9.22 → 8.99 | 257.9 → 248.2 (-3.8%) | 5676 → 5102 | 14.85 → 15.16 | 68.51 → 68.66 |
| burst4 | 3.35 → 4.55 (+35.8%) | 3.30 → 4.89 | 144.5 → 140.5 (-2.8%) | 2850 → 2640 | 16.46 → 16.60 | 69.27 → 69.20 |
| stream256-cross | 178.86 → 188.32 (+5.3%) | 124.34 → 145.30 | 20782.0 → 20473.0 (-1.5%) | 137476 → 133039 | 12.20 → 11.55 | 68.33 → 68.40 |
| image2 | 152.28 → 108.36 (-28.8%) | 136.59 → 68.38 | 21805.9 → 21876.6 (+0.3%) | 5086 → 4821 | 37.60 → 31.38 | 95.62 → 92.65 |

## Fresh 31 MiB single-concurrency measurement

| Transport | CPU ms/request (change) | P50 ms | MiB/request | peak heap MiB | peak RSS MiB |
| --- | ---: | ---: | ---: | ---: | ---: |
| JSON | 844.49 → 750.71 (-11.1%) | 828.10 → 785.25 | 321.77 → 321.47 | 326.00 → 449.92 | 431.61 → 509.35 |
| SSE | 932.43 → 765.50 (-17.9%) | 910.32 → 830.40 | 321.98 → 321.42 | 312.10 → 449.76 | 381.70 → 509.38 |

## Regression investigation and interpretation

### Burst timing

The 64-request final ABBA showed burst CPU **+35.8%** and P50 **+48.2%**, despite fewer allocations. Because adjacent runs also fluctuated, a longer dedicated ABBA used **1,024 requests × five repeats × two processes/version**, ten repeat samples and **10,240 requests/version**, with the same already-verified binaries:

| Metric | Baseline → integrated |
| --- | ---: |
| CPU ms/request | 1.1532 → 1.1728 (**+1.7%**) |
| P50 ms | 1.0207 → 1.0232 (**+0.2%**) |
| B/request | 146,467 → 143,277 (**-2.2%**) |
| allocations/request | 2,842 → 2,636 (**-7.2%**) |
| sampled peak heap MiB | 15.20 → 15.59 |
| sampled peak RSS MiB | 86.48 → 87.06 |

The large slowdown does not persist at the same magnitude in this longer run. This neither erases the short-run result nor proves no regression: a small CPU increase remains. Both reports are retained. Final cross-stream CPU **+5.3%** and P50 **+16.9%** remain unresolved; reduced transient allocations have not translated into a reliable end-to-end stream CPU gain.

### Large-image peaks versus retained state

Image31 CPU is lower, but cumulative allocation is essentially unchanged at about 321 MiB/request. Normal sampled peak heap rises **38.0% (JSON) / 44.1% (SSE)**; RSS rises **18.0% / 33.5%**. This is a material regression in that dataset, not a claimed peak-memory win. Cache-retained leaves, changed allocation timing and GC schedules are hypotheses, not established attribution. Further retained-object/GC-phase analysis is needed before tuning ownership limits.

By contrast, the identical held-stream fixture on item 3 versus item 4 showed **36.021 → 19.915 MiB (-44.7%)** median live heap after diagnostic GC while four 2 MiB streams remained active, and RSS **113.916 → 95.375 MiB (-16.3%)**. Preparation counters were zero while held; cancellation drained requests, queued usage and payload counters. Settled heap was **3.612 → 3.628 MiB**. These measurements answer a different question—what stays reachable during a stalled stream—and do not cancel the normal peak regression or establish long-term leak freedom.

## Architecture and code-quality changes

### Explicit ownership rather than implicit reuse

`backend/protocol/document_metrics.go` owns bounded immutable-leaf metrics for one outer execution, including native replay. It caches no mutable semantic containers or policy verdicts; current budgets and each occurrence remain counted. Capacity is limited to 128 entries/64 MiB, with no owner created below 64 KiB. Lifetime is call-local, not attached to a public Request or a global cache. This is an initial compatible-boundary migration, not a universal document index or cross-stage schema cache.

### One generated model contract

`backend/internal/semanticmeter/main.go` derives resource walkers from actual model fields/tags and rejects unsupported contracts. `go generate -C backend ./protocol` regenerates; `TestGeneratedMeterCurrent` checks freshness using the bootstrap reflective build. Exact error/unsupported behavior remains on the existing oracle. Cloning and normalization are still reflective. The 64-message helper improves **19,377 → 8,490 ns/op (-56.2%)**, zero allocations in both versions; whole-request CPU does not inherit that percentage.

### Typed frames with real validation boundaries

`TypedFrameEncoder` hands owned frames to eligible runtime paths without materializing/reparsing a JSON array. It runs stateful encoders once, retains normalization and the aggregate batch byte/node/depth budget, and preserves authored JSON/schema/native replay paths. Plain short ASCII scalars allocate their exact representation; complex text still uses Sonic. This reduces cross-stream allocation counts about **3.2%** in the final comparison; it is not removal of final-wire/lifecycle validation or a flush-policy change.

### Separate preparation and delivery lifetimes

`backend/server/gateway_lifetime.go` makes the delivery owner incapable of retaining request history or alternative candidates. Retriable failures keep unattempted preparations; final dispatch and successful response headers end preparation ownership. Chat response-model provenance retains only the sent model, not the whole outbound document. A targeted encrypted-reasoning continuation test caught an early snapshot of recovery state; the corrected code refreshes recovery context after continuation capture. Both logs are retained. Task/WebSocket owners are not migrated by this series.

## What profiling still shows

- Image31 remains dominated by string scanning: final `indexbytebody` 38.2%, `countbody` 15.0%, UTF-8 validation 10.5% cumulative. Compatible-call metric reuse did not eliminate scans in every capability, conversion, native and final validation boundary.
- Chat now executes generated metering (`visitTyped`, 13.0% cumulative), but the subtree still performs real raw scans; normalization remains about 7.9% cumulative. Generating a walker does not remove the work it must perform.
- Cross-stream still has syscall cost (11.0% flat), GC/object scanning (`scanObject`, 11.1% cumulative), copying and normalization. Allocation counts fall modestly; unchanged actual-wire and lifecycle checks remain material work.
- These profile percentages are within each timed sample window. Subtrees overlap and window request counts differ; percentages are not added into an estimated speedup or substituted for CPU/request.

Remaining work should first explain cross-stream timing and image31 peak regressions, then consider extending metric ownership with measured retention limits, generated normalization/clone traversal, and other codec/output coverage. Plan-lock/flush changes still need specific contention/write evidence. This iteration is a set of tested architecture improvements, **not completion of a whole-project performance rewrite**.

## Reproduction and evidence

```sh
node scripts/benchmark-performance.mjs --mode=measure \
  --cases=chat-short,candidates4,burst4,stream256-cross,image2 \
  --requests=32 --repeats=3 --idle-ms=1000 --output=NEW_EMPTY_DIRECTORY

node docs/artifacts/performance-pipeline-86/paired-runner.mjs \
  .tmp-dev/perf-pipeline/baseline/report.json .tmp-dev/perf-pipeline/item-4/report.json \
  NEW_EMPTY_DIRECTORY chat-short,candidates4,burst4,stream256-cross,image2 64 3

node docs/artifacts/performance-pipeline-86/paired-runner.mjs \
  .tmp-dev/perf-pipeline/baseline/report.json .tmp-dev/perf-pipeline/item-4/report.json \
  NEW_EMPTY_DIRECTORY burst4 1024 5

node docs/artifacts/performance-pipeline-86/final-profiles/runner.mjs \
  .tmp-dev/perf-pipeline/baseline/report.json .tmp-dev/perf-pipeline/item-4/report.json \
  NEW_EMPTY_DIRECTORY

node docs/artifacts/performance-pipeline-86/verify.mjs
go tool pprof -http=127.0.0.1:0 \
  docs/artifacts/performance-pipeline-86/final-profiles/after-cpu-stream256-cross/cpu.pprof
```

Runners require matching local binaries at paths recorded in their input reports; profiles embed symbols for independent inspection. [Archive README](artifacts/performance-pipeline-86/README.md) · [machine summary](artifacts/performance-pipeline-86/summary.json). Source patches identify pre-commit measurements; the accepted signed commit and clean-binary equality identify final production code. No full suites, real-provider production tests, traffic-weighted overall CPU metric, long-duration capacity guarantee or all-platform release acceptance are claimed.
