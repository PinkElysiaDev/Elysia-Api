# Profiling-driven Go gateway optimization: final comparison

Collected 2026-10-20/21 for [issue #77](https://github.com/PinkElysiaDev/Elysia-Api/issues/77). Review stack: [#78](https://github.com/PinkElysiaDev/Elysia-Api/pull/78) → [#79](https://github.com/PinkElysiaDev/Elysia-Api/pull/79) → [#80](https://github.com/PinkElysiaDev/Elysia-Api/pull/80).

**Outcome: targeted allocation savings are reproducible, with substantial large-image peak-memory reductions. Timing and general resident-memory changes remain mixed; this is not a universal latency, RSS, or leak-freedom claim.**

## Source and delivery

| Batch | Signed implementation | Delivered scope | Incremental observations |
| --- | --- | --- | --- |
| 1 / #78 | `d0e7bc0` | Read-only operation metadata, prepared usage SQL, copy-on-change continuation history, lazy unknown-field maps | Chat B/request -26.6%, candidates4 -38.6%, stream256 -10.7% |
| 2 / #79 | `f603cf2` | Bounded immutable final-wire views, frame root-object reuse, fewer byte/string copies | image2 B/request -34.0%, history256k -8.4%, stream256 object count -8.7% |
| 3 / #80 | `fbee14a` | Single-buffer event-array fingerprints, bounded semantic workspace, cleared text-tracker scratch | stream256 B/request -11.3%, cross-stream -6.3%; initialized tracker append 0 B/op, 0 allocs/op |

Each batch passed selected race regressions and its nine-case HTTP acceptance before its implementation commit was GPG-signed, verified, and pushed. The signing identity is `TR0MX <ytang7543@gmail.com>`, fingerprint `F8C0FF15D6227656C0748FD47A8E1E265270109C`. GitHub also reports the implementation signatures as verified. PRs remain unmerged and must be reviewed in dependency order.

The comparison is **original current backend `8e732c7` (same backend source as `5a8115e`) → integrated `fbee14a`**, not v1.6.2 → integrated. Both use the same Go 1.27.2/Sonic build, PGO off, Linux/amd64 Ryzen 7 8845H, GOMAXPROCS=4, GOGC=100, GOMEMLIMIT=2GiB. Binary hashes and backend tree identities are in [summary.json](artifacts/performance-targeted-77/summary.json). The batch-3 acceptance binary and clean integrated profiling build have exactly the same SHA256; the baseline acceptance/profile binaries also match.

Per-batch details and all primary-matrix regressions: [batch 1](backend-performance-targeted-77-batch-1.md), [batch 2](backend-performance-targeted-77-batch-2.md), [batch 3](backend-performance-targeted-77-batch-3.md).

## Measurement boundaries

- No full test suite or full benchmark suite ran. Explicit test filters, two local helper benchmarks, selected HTTP fixtures, and six selected profiling workloads were used.
- Client, gateway, and provider are separate processes. Complete responses, preserved request content, usage, and upstream call counts are checked. No real accounts/providers or production databases are used.
- Every primary batch used nine HTTP cases, 16 requests × three repeats (image2 fixed at four), four warm-ups and one-second idle observation: 396 counted requests/version/run. Raw reports retain first-request and per-request data.
- After **all three batch acceptances and signed implementation pushes**, final CPU/allocation profiling ran, followed by original-vs-integrated ABBA HTTP confirmation, cancellation diagnostics, and the three original large-payload workloads on both matching binaries.
- The five-case final ABBA confirmation below uses 64 requests × five repeats × two processes/version/case; image2 remains four requests/repeat. This is 2,600 counted requests/version. Each case runs before/after/after/before, serially, without profiling or forced GC.
- The legacy supplement uses five repeats: nodes four requests per JSON/SSE repeat, events sixteen per SSE repeat, image31 one per JSON/SSE repeat (130 counted requests/version). **31 MiB is single-concurrency input base64 text**, not a four-concurrency capacity claim.
- Main CPU profiles are 20 seconds each; supplemental profiles 30 seconds each, with memory runs separate. All 12 CPU profiles have more than ten seconds of accumulated CPU samples. Allocation profiles are before/after differences; timed heap snapshots are not peak-triggered. Profiler overhead is included in profile totals but excluded from formal HTTP timing.
- Tables use per-repeat medians; sampled heap/RSS peaks are maxima. Allocation/CPU accounting includes usage settlement, whereas HTTP latency ends after response consumption. Sampling may miss brief spikes. Heap includes uncollected garbage; RSS is not live heap.
- ABBA reduces ordering bias but does not isolate other normal machine activity or establish significance. Small-sample P95/P99 and first-frame timing are not production tail latency or first-model-token guarantees.

## Final ABBA HTTP comparison

| Case | KiB/request (change) | allocations/request | CPU ms/request | complete-response P50 ms | sampled peak heap MiB | sampled peak RSS MiB |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| chat-short | 202.4 → 144.1 (-28.8%) | 3464 → 2850 | 5.98 → 6.37 | 3.23 → 4.70 | 16.13 → 15.56 | 68.61 → 68.03 |
| candidates4 | 410.9 → 259.2 (-36.9%) | 7344 → 5672 | 11.08 → 9.16 | 7.64 → 7.18 | 15.89 → 15.58 | 69.55 → 68.70 |
| stream256 | 9571.8 → 7126.4 (-25.5%) | 113404 → 98049 | 111.40 → 106.68 | 82.96 → 87.79 | 11.01 → 9.50 | 65.94 → 64.38 |
| stream256-cross | 23687.2 → 20744.7 (-12.4%) | 157131 → 137467 | 203.64 → 180.13 | 156.73 → 137.29 | 11.09 → 12.40 | 69.36 → 69.08 |
| image2 | 105497.7 → 67852.9 (-35.7%) | 6231 → 5192 | 324.33 → 251.99 | 220.66 → 164.61 | 61.89 → 37.64 | 118.73 → 93.77 |

## Original profiling workloads, freshly remeasured

| Case | MiB/request (change) | CPU ms/request | complete-response P50 ms | sampled peak heap MiB | sampled peak RSS MiB |
| --- | ---: | ---: | ---: | ---: | ---: |
| nodes-JSON | 105.93 → 82.15 (-22.4%) | 464.54 → 464.88 | 317.67 → 308.06 | 44.28 → 39.63 | 106.14 → 94.98 |
| nodes-SSE | 106.22 → 82.27 (-22.5%) | 559.12 → 371.36 | 389.36 → 292.97 | 46.70 → 51.77 | 101.07 → 106.31 |
| events-SSE | 10.64 → 8.56 (-19.6%) | 193.33 → 112.29 | 155.53 → 63.32 | 10.56 → 10.10 | 63.55 → 63.50 |
| image31-JSON | 1564.39 → 1004.60 (-35.8%) | 1278.13 → 1203.33 | 1124.30 → 1125.42 | 729.30 → 407.35 | 822.73 → 472.33 |
| image31-SSE | 1564.41 → 1004.53 (-35.8%) | 1453.78 → 1071.24 | 1265.41 → 1012.79 | 698.04 → 407.40 | 759.95 → 486.24 |

### Regressions and timing sensitivity

The five-case final ABBA run includes Chat CPU **+6.4%** and P50 **+45.5%**, same-protocol stream P50 **+5.8%**, and cross-stream peak heap **+11.9%**. The nodes-SSE supplement also has a higher sampled peak heap/RSS despite lower cumulative allocation. None is hidden or described as an across-the-board win.

Because Chat timing had already varied sharply between repeats, a separate, longer Chat-only ABBA confirmation used 64 requests × ten repeats × two processes/version (1,280 requests/version): B/request **203.6 → 144.3 KiB (-29.1%)**, CPU **8.65 → 5.03 ms**, P50 **6.53 → 3.61 ms**. The sign reversal means a stable short-request timing regression or improvement cannot be concluded from these runs. Allocation reductions remain stable. Both datasets, not a selected replacement, are preserved: [five-case run](artifacts/performance-targeted-77/final-paired/summary.json), [longer Chat confirmation](artifacts/performance-targeted-77/final-chat-confirmed/summary.json).

## Profiling attribution and remaining work

The full archive contains 60 raw profiles: 12 CPU and 48 heap/allocation profiles, covering short Chat, 256-frame stream, 2 MiB image, 1,800 text blocks, 512 small deltas, and 31 MiB image on both versions. Embedded symbols support independent pprof viewing without the local test binaries.

Selected sampled allocation observations from identical fixed-count memory workloads:

- image31 `ValidateWireOutput` subtree: **434.11 → 31.01 MiB**. Mandatory final validation still executes; large payload reads share immutable storage.
- image31 `ExtractContinuationCarriers`: **155.04 → 31.01 MiB**; session lookup **62.02 → 31.01 MiB**.
- image31 string-clone flat allocation: **589.15 → 62.02 MiB**. Its `EncodeRequest` subtree, however, remains roughly **651.7 MiB**: generic nested encoding/native reconstruction is not solved.
- stream256 `Value.Bytes` flat allocation: **13.50 → 5.00 MiB**; semantic-cache store **10.51 → 1.00 MiB**. Initialized tracker helper tests independently establish the zero-per-append allocation boundary; stream construction is not allocation-free.
- Read-only operation deep-copy work and repeated usage INSERT/UPSERT preparation were removed from their measured hot paths. Iterator callback cumulative samples must not be misread as the cost of iterating operation metadata.

These subtrees can overlap and profile samples include collector overhead. They are not summed into a promised percentage or substituted for the formal B/request measurements.

The final 31 MiB request still allocates about **1.0 GiB cumulatively**, roughly 32 times its input text. `MakeNoZero` and `ParseValue` alone still account for about 76.7% of flat sampled allocation in that profile; scanning/compaction/UTF-8 checks also remain prominent CPU work. A universal single-output nested request encoder, wider scan reuse/generated walkers, early global preparation-memory admission, plan-cache lock redesign, and flush policy remain explicit follow-ups, not completed changes. There is no new global payload pool, forced-GC production policy, durability weakening, or framework replacement.

## Cancellation and recovery diagnosis

After the batches, the original and integrated matching binaries each passed five batches of 2 MiB × four concurrent streams cancelled after a data frame. Upstream generation counts matched, active requests/queued items/retained queued payload returned to zero, and reclaimed goroutines remained 12.

| Diagnostic | Original | Integrated |
| --- | ---: | ---: |
| Forced-GC heap range over five batches | 3.626–3.635 MiB | 3.616–3.620 MiB |
| RSS range at those recovery points | 158.10–189.07 MiB | 146.18–157.50 MiB |

This checks release in these cancellation batches, not prolonged leak freedom or natural RSS reclamation. It does not validate 31 MiB × four concurrency. The natural one-second idle windows, separate forced-GC diagnostics, and timed heap profiles are intentionally not conflated.

## Reproduction and evidence

From the repository root, with the matching baseline/integrated sources and separate empty output directories:

```sh
# Focused profiles, not the full benchmark suite.
node scripts/benchmark-performance.mjs --mode=profile \
  --profiles=chat-short,stream256,image2 --seconds=20 \
  --requests=16 --repeats=3 --output=NEW_EMPTY_DIRECTORY

# Original nodes/events/image31 workloads; reuse the hash-checked profile binary.
node docs/artifacts/performance-v1.6.2-vs-5a8115e/comparison/profiling-supplement.mjs \
  . MAIN_PROFILE_DIRECTORY NEW_EMPTY_SUPPLEMENT_DIRECTORY

# Original-vs-integrated HTTP ABBA with the binaries identified in the reports.
node docs/artifacts/performance-targeted-77/final-paired/runner.mjs \
  .tmp-dev/perf-targeted/baseline/report.json .tmp-dev/perf-targeted/batch-3/report.json \
  NEW_EMPTY_DIRECTORY chat-short,candidates4,stream256,stream256-cross,image2 64 5

# Independent cancellation diagnosis; repeat for each matching binary.
GOMAXPROCS=4 GOGC=100 GOMEMLIMIT=2GiB \
ELYSIA_CONVERSION_HTTP_CANCEL=1 ELYSIA_CONVERSION_HTTP_MIB=2 \
ELYSIA_CONVERSION_HTTP_WORKERS=4 ELYSIA_CONVERSION_HTTP_LOG=off \
ELYSIA_CONVERSION_HTTP_OUTPUT=EXISTING_EMPTY_DIRECTORY \
  MATCHING_SERVER_TEST_BINARY -test.run='^TestConversionHTTPCancellationIsolated$' \
  -test.v -test.timeout=10m

node docs/artifacts/performance-targeted-77/verify.mjs
go tool pprof -http=127.0.0.1:0 \
  docs/artifacts/performance-targeted-77/final-legacy/cpu-image31/cpu.pprof
```

See the [archive README](artifacts/performance-targeted-77/README.md), [machine-readable summary](artifacts/performance-targeted-77/summary.json), per-batch reports, original command logs/manifests, compressed paired-run measurements, and immutable source patches. The original profiling archive remains untouched. Full tests/benchmarks, all protocol combinations, real-provider acceptance, native cross-platform release checks, and prolonged soak testing were not run and are not claimed.
