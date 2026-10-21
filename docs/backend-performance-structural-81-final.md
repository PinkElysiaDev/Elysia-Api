# Structural performance follow-up: final results for items 1 → 2 → 4

Tracking: [#81](https://github.com/PinkElysiaDev/Elysia-Api/issues/81), following #77. Implementations were accepted and GPG-signed individually, then delivered in dependency order as [#82](https://github.com/PinkElysiaDev/Elysia-Api/pull/82) → [#83](https://github.com/PinkElysiaDev/Elysia-Api/pull/83) → [#84](https://github.com/PinkElysiaDev/Elysia-Api/pull/84). All three are now merged. This report and its evidence are a documentation-only follow-up; no implementation changes were made after these measurements.

**Outcome: large-request cumulative allocation is substantially lower, and preparation concurrency/reservations are explicitly bounded. Neither result establishes universally lower latency or RSS. In particular, the single-concurrency 31 MiB peak-memory measurements did not improve.**

## Source, scope and per-item acceptance

Fresh baseline: `1ddf7af` (the preceding #78–#80 stack was already merged). Integrated implementation: `053edf5`; upstream merge `8099841` has the same backend source. This is not a comparison against v1.6.2. Source tree identities, binary SHA256 values and all final metrics are in [summary.json](artifacts/performance-structural-81/summary.json).

| Requested item | Signed implementation | Implemented scope | Incremental evidence |
| --- | --- | --- | --- |
| 1 / #82 | `a433eb8` | Immutable closed-container output plans; large native reconciliation and initial large Anthropic request rollout | image2 B/request -42.9%; media logging -31.2%; 2 MiB/eight nested pairs helper B/op -93.7% |
| 2 / #83 | `669ef17` | Immutable ingress/preflight values through session/carrier/decode/operation/final checks; boundary-local media views | On top of item 1: image2 B/request -43.6%; media -26.1%; history256k -7.5% |
| 4 / #84 | `053edf5` | FIFO preparation admission before body reading, bounded queue/reservations, timeout/cancellation/shutdown and documented startup policy | Enforced default eight-slot limit; separate one-slot memory/queueing trade-off below |

Per-item commands, regressions and acceptance logs: [item 1](backend-performance-structural-81-item-1.md), [item 2](backend-performance-structural-81-item-2.md), [item 4](backend-performance-structural-81-item-4.md). Commit signatures use `TR0MX <ytang7543@gmail.com>`, fingerprint `F8C0FF15D6227656C0748FD47A8E1E265270109C`, verified locally before push and by GitHub.

The general output engine snapshots mutable container inputs and materializes nested output once. Its request-adapter rollout is deliberately limited to large ordinary Anthropic history; complex overlays and other codecs retain their established renderer. Immutable request reuse preserves syntax validity/storage, **not** a cached authorization/schema/resource verdict. Required final checks, precise numbers, unknown native fields, candidate isolation, cancellation, model quotas and usage durability remain in place.

## Measurement method and limits

- Same Linux/amd64 Ryzen 7 8845H, Go 1.27.2, Sonic, PGO off, GOMAXPROCS=4, GOGC=100, GOMEMLIMIT=2GiB. Builds, tests and measurements ran serially.
- Explicit affected-package test filters, differential fuzz and localized helper benchmarks only; no full test suite or full benchmark suite.
- Isolated client/gateway/provider processes; complete responses, preserved request content, usage and upstream call counts are checked. No production accounts or databases.
- Each seven-case HTTP matrix uses 16 requests × three repeats, images fixed at four, four warm-ups and one-second idle observation: 264 counted requests/version/run. First requests and per-request/timeline data are retained separately. Timed HTTP runs do not enable profiling or force GC.
- Allocation/CPU accounting includes usage settlement; HTTP latency ends at response consumption. Tables use per-repeat medians; sampled peaks are maxima and may miss brief spikes. Heap includes uncollected garbage; RSS is not live heap.
- Final profiling ran after all three item acceptances/signatures/pushes: four selected workloads/version, separate CPU and allocation runs, 40 raw profiles (eight CPU, 32 heap/allocation). CPU windows are 15 seconds; every profile contains more than ten seconds of accumulated CPU samples.
- The fresh image31 comparison uses one request per JSON/SSE repeat, three repeats, two warm-ups, single concurrency. It is **31 MiB of input base64 text**, not 31 MiB × four concurrency. Profile timings do not contribute to formal HTTP results.
- Sequential measurements and the localized ABBA investigations are not significance tests or production P99 estimates. Control timings fluctuate and some regressions remain; they are retained, not rewritten as universal speedups.
- The accepted integrated binary was rebuilt from clean signed source and matched the measured/profiled SHA256 exactly. New test-only admission metrics contribute collector overhead, so tiny adjacent-stage changes cannot be attributed entirely to the production limiter.

## Overall selected HTTP comparison

| Case | KiB/request (change) | allocations/request | CPU ms/request | response P50 ms | sampled peak heap MiB | sampled peak RSS MiB |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| chat-short | 149.9 → 145.1 (-3.2%) | 2854 → 2862 | 7.96 → 4.47 | 5.54 → 3.30 | 15.80 → 15.02 | 67.71 → 67.30 |
| candidates4 | 258.4 → 258.4 (-0.0%) | 5690 → 5691 | 13.64 → 7.20 | 10.15 → 5.82 | 14.72 → 14.32 | 67.92 → 67.15 |
| history256k | 11197.9 → 10460.2 (-6.6%) | 34444 → 34406 | 91.67 → 43.21 | 68.83 → 33.77 | 14.28 → 13.45 | 68.74 → 67.26 |
| burst4 | 152.3 → 145.2 (-4.6%) | 2863 → 2860 | 1.55 → 2.88 | 1.56 → 2.68 | 16.53 → 15.40 | 68.58 → 69.50 |
| stream256-cross | 20891.9 → 20794.5 (-0.5%) | 137444 → 137454 | 64.53 → 116.98 | 45.28 → 80.68 | 10.66 → 10.47 | 65.89 → 65.82 |
| image2 | 67840.5 → 21876.0 (-67.8%) | 5217 → 5074 | 303.92 → 100.91 | 227.47 → 86.96 | 33.46 → 37.32 | 93.38 → 91.32 |
| image2-media | 93684.5 → 47716.0 (-49.1%) | 5727 → 5668 | 336.50 → 288.73 | 227.88 → 260.49 | 39.21 → 42.57 | 98.75 → 99.17 |

## Fresh single-concurrency 31 MiB image comparison

| Transport | MiB/request (change) | CPU ms/request | response P50 ms | sampled peak heap MiB | sampled peak RSS MiB |
| --- | ---: | ---: | ---: | ---: | ---: |
| JSON | 1004.37 → 321.53 (-68.0%) | 951.45 → 755.10 | 910.11 → 744.71 | 418.77 → 418.89 | 494.93 → 510.19 |
| SSE | 1004.66 → 321.74 (-68.0%) | 969.75 → 726.63 | 922.76 → 748.86 | 407.35 → 449.82 | 472.38 → 510.50 |

### Regressions and interpretation

The 31 MiB cumulative allocation reduction is about **68%** in both transports, with lower observed CPU/P50. However JSON sampled peak heap is essentially unchanged and RSS rises; SSE peak heap rises about **10.4%** and RSS about **8.1%**. Reduced allocation does not imply an equivalent reduction in live heap, GC thresholds, sampled garbage or resident mappings. This run does not establish which retained owner accounts for each peak.

The seven-case table also includes substantial burst4/cross-stream timing regressions relative to the original sequential baseline and a media-logging P50 increase. Item-1 ABBA control investigations, including a retained burst regression, are published in its report. No stable universal timing improvement is inferred. Allocation improvements in image workloads reproduce across the adjacent stages and the fresh 31 MiB workload; candidate/stream allocation counts are essentially unchanged.

## Preparation admission: capacity benefit versus queueing cost

Admission limits cover HTTP generation preparation, not total process heap or all in-flight responses. The lease starts after auth/protocol selection, covers body reading, conversion/preflight and final request checks, releases before provider dispatch, and is reacquired on retry. Group/daily quotas are unchanged. Unknown length reserves the pinned body limit; declarations cannot grow a held reservation. Configuration is startup-frozen and requires restart. [Full policy and exclusions](backend-preparation-admission.md).

Two same-binary off/bounded/bounded/off experiments isolate policy effects from encoding changes. All requests completed without rejection/timeouts, content/usage/provider-call checks passed, and active/queued/reserved counters returned to zero:

| Experiment | Preparation peak off → bounded | Sampled heap MiB off → bounded | Sampled RSS MiB off → bounded | Observed P50 ms off → bounded |
| --- | ---: | ---: | ---: | ---: |
| 16 workers, default eight slots, 192 requests/policy | 16 → 8 | 288.94 → 287.08 | 358.07 → 364.27 | 137.82 → 129.66 |
| 8 workers, stricter one slot, 96 requests/policy | 8 → 1 | 150.73 → 35.59 | 217.68 → 94.75 | 71.86 → 611.05 |

Default admission enforces concurrency and reservation bounds but did not improve sampled RSS in this experiment. The stricter one-slot policy reduced sampled peak heap **76.4%** and RSS **56.5%**, while sharply increasing queue latency and observed CPU/request (43.28 → 107.72 ms). Those percentages are **not default-policy memory gains**. The unqueued acquire/release helper measured 32 B/op, one allocation/op; HTTP/context/logging/initialization are outside that helper boundary.

## Profile attribution and remaining costs

Identical one-request image31 allocation-profile runs show:

- `ParseValue` flat sampled allocation: **217.05 → 31.01 MiB**, consistent with retaining validated immutable input through compatible boundaries.
- Container-output `MakeNoZero` flat allocation: **558.14 → 124.03 MiB**.
- Original `EncodeRequest` subtree: approximately **652.27 MiB**; integrated `EncodeRequestValue`: approximately **124.52 MiB**. Names differ because the immutable API is now the internal boundary.
- `preserveMappedNative` subtree: **341.07 → 93.51 MiB**. Final wire validation still accounts for about 31.01 MiB in this run and still executes.

Profile subtrees overlap and include collector overhead, so these are not added into a claimed percentage or substituted for formal B/request. The final request still allocates about **322 MiB cumulatively**, roughly ten times input text; bounded reads, exact native comparisons/decoding, final transport copying and remaining native/container work still cost memory. Additional adapter coverage, wider resource-metric reuse, generated walkers, plan-cache contention work, active-upload timeout policy and prolonged soak testing remain follow-ups, not completed claims.

## Cancellation/recovery diagnosis

The matching original and integrated binaries each passed five 2 MiB × four concurrent stream-cancellation batches after receiving a data frame. Upstream counts matched. Requests, usage queue/payloads and integrated preparation active/queued/reserved counters returned to zero; reclaimed goroutines remained 12.

| Diagnostic | Original | Integrated |
| --- | ---: | ---: |
| Forced-GC heap range over five batches | 3.611–3.618 MiB | 3.612–3.630 MiB |
| RSS range at those recovery points | 129.38–162.61 MiB | 114.44–125.94 MiB |

This is a short cancellation/release diagnosis, not proof of natural idle reclamation, prolonged leak freedom or large-payload concurrency capacity. Forced-GC diagnosis is separate from formal HTTP measurements and natural idle windows.

## Reproduction and evidence

```sh
# Per-item acceptance: selected cases only, separate empty output directories.
node scripts/benchmark-performance.mjs --mode=measure \
  --cases=chat-short,candidates4,history256k,burst4,stream256-cross,image2,image2-media \
  --requests=16 --repeats=3 --idle-ms=1000 --output=NEW_EMPTY_DIRECTORY

# Final independent profiles + fresh single-concurrency image31 measurement;
# requires the matching binaries at the paths recorded in these local reports.
node docs/artifacts/performance-structural-81/final-profiles/runner.mjs \
  .tmp-dev/perf-structural/baseline/report.json .tmp-dev/perf-structural/item-4/report.json \
  NEW_EMPTY_DIRECTORY

# Same-binary admission controls: default eight slots, then stricter one slot.
node docs/artifacts/performance-structural-81/item-4-default-memory/runner.mjs \
  .tmp-dev/perf-structural/item-4/report.json NEW_EMPTY_DIRECTORY 8 16 32
node docs/artifacts/performance-structural-81/item-4-memory/runner.mjs \
  .tmp-dev/perf-structural/item-4/report.json NEW_EMPTY_DIRECTORY 1 8 16

node docs/artifacts/performance-structural-81/verify.mjs
go tool pprof -http=127.0.0.1:0 \
  docs/artifacts/performance-structural-81/final-profiles/after-cpu-image31/cpu.pprof
```

[Archive README](artifacts/performance-structural-81/README.md) · [machine-readable summary](artifacts/performance-structural-81/summary.json). Raw profiles embed symbols, so independent pprof inspection does not require the local test binaries. Binary hashes, source patches, original command logs, compressed byte-preserved measurements, CPU sample counts and checksums are preserved. Full tests/benchmarks, every protocol/metadata combination, real-provider and cross-platform release acceptance, and prolonged capacity testing were not run and are not claimed.
